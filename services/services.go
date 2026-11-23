package services

import (
	"context"
	"time"

	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/oauth2"
	"coachify-account-api/pkg/chat"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/invitation"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/pkg/tracker"
	"coachify-account-api/repositories"
	"coachify-account-api/utils"

	jwt "github.com/appleboy/gin-jwt/v2"
)

type Services struct {
	AuthService             AuthService
	CoachService            CoachService
	CoachProfile            CoachProfileService
	AdminService            AdminService
	TeamService             TeamService
	GymService              GymService
	AnalyticsScopeService   AnalyticsScopeService
	RegistrationLinkService RegistrationLinkService
	UserStatus              UserStatusLookup
	EntitlementService      EntitlementService
	GymOwnerInviteService   GymOwnerInviteService
	GymMembership           GymMembershipReader
	GymPolicy               GymPolicyService
	GymMembers              GymMembersService
	GymClients              GymClientService
	InviteLinks             InviteLinkService
	Quota                   QuotaGuard
	// GymLeaver serves POST /me/gyms/:gym_id/leave; nil unless GYM_DISPATCH_ENABLED (and CLIENT_GYM_RELATIONS).
	GymLeaver GymClientLeaver
	// Seats serves the seat endpoints (404 while GYM_SEATS_ENABLED is off).
	Seats SeatService
	// ContextEntitlements serves GET /internal/users/:id/entitlements; nil (a 404) unless GYM_SEATS_ENABLED.
	ContextEntitlements ContextEntitlementResolver
	// GymAddons serves the internal coaching add-on endpoints; nil unless GYM_MEMBERSHIP_PLANS_ENABLED (and a payments key).
	GymAddons GymAddonService
}

// DispatchWiring carries the P6 collaborators; nil while GYM_DISPATCH_ENABLED is off.
type DispatchWiring struct {
	Repo    *repositories.GymDispatchRepository
	Tracker *tracker.TrackerClient
}

func InitServices(config utils.AppConfig,
	middleware *jwt.GinJWTMiddleware,
	userRepo *repositories.UserRepository,
	coachRepo *repositories.CoachRepository,
	registrationLinkRepo *repositories.RegistrationLinkRepository,
	pwChecker core.PasswordChecker,
	activationManager core.ActivationManager,
	identfier *identifier.IdentifierClient,
	notification *notification.NotificationClient,
	invitation *invitation.InvitationClient,
	providers map[oauth2.ProviderType]oauth2.Provider,
	payment *payments.PaymentClient,
	chatClient *chat.ChatClient,
	teamRepo *repositories.TeamRepository,
	adminAuditRepo *repositories.AdminAuditRepository,
	gymRepo *repositories.GymRepository,
	entitlementRepo *repositories.EntitlementRepository,
	entitlementAuditRepo *repositories.AdminAuditRepository,
	gymOwnerInviteRepo *repositories.GymOwnerInviteRepository,
	gymMemberRepo *repositories.GymMemberRepository,
	gymPolicyRepo *repositories.GymPolicyRepository,
	gymClientRepo *repositories.GymClientRepository,
	inviteLinkRepo *repositories.InviteLinkRepository,
	dispatch *DispatchWiring,
) *Services {
	coachService := NewCoachService(
		coachRepo,
		notification,
		identfier,
		activationManager,
		config.BaseURL.URL,
	)
	authService := NewAuthService(
		userRepo,
		coachService,
		pwChecker,
		middleware,
		activationManager,
		identfier,
		notification,
		invitation,
		providers,
		payment,
		registrationLinkRepo,
		chatClient,
		teamRepo,
	)
	// Plans come from payments-api; a nil client (tests) means every user resolves without a plan.
	var planSource EntitlementPlanSource
	if payment != nil {
		planSource = payment
	}
	entitlementService := NewEntitlementService(entitlementRepo, userRepo, coachService, planSource, entitlementAuditRepo)
	authService.WithEntitlements(entitlementService)
	// The flat hierarchy has coaches without a head coach, which only gym_members can represent.
	membersRead := config.Gym.MembersRead
	if config.Gym.FlatHierarchy {
		membersRead = GymMembersReadNew
	}
	membership := NewGymMembershipReader(membersRead,
		NewLegacyMembershipAdapter(gymRepo, teamRepo), NewRepoMembershipReader(gymMemberRepo))
	// With client gym relations on, client resources resolve against gym_clients (the gym-derived grants need an active row).
	var clientResolver ResourceResolver
	if config.Gym.ClientGymRelations {
		clientResolver = NewResourceResolver(NewClientScopeSource(gymClientRepo))
	}
	chain := NewChainAuthorizer(membership, gymPolicyRepo, clientResolver)
	authz := NewCachingAuthorizer(chain, DefaultAuthzCacheTTL)
	// Every membership mirror write drops cached decisions and bumps the gym's authz_version.
	memberStore := NewInvalidatingMemberStore(gymMemberRepo, authz, gymPolicyRepo)
	authService.WithGymMembers(memberStore)
	// Seats (P8a). Built always so the endpoints answer 404 when off; every other hook stays nil unless enabled.
	seatsOn := config.Gym.SeatsEnabled
	var gymSummaries *GymSummaryCache
	if seatsOn && payment != nil {
		gymSummaries = NewGymSummaryCache(payment, gymSummaryTTL)
	} else {
		gymSummaries = NewGymSummaryCache(nil, gymSummaryTTL)
	}
	var ctxCache *CachedContextResolver // set below when seats are on; the purge closure reads it lazily
	seatSvc := NewSeatService(authz, gymMemberRepo, gymSummaries, SeatSettings{
		Enabled: seatsOn, Grandfather: config.Gym.SeatGrandfather,
		OverflowGrace: time.Duration(config.Gym.SeatOverflowGraceDays) * 24 * time.Hour, Inval: authz, Bump: gymPolicyRepo,
		Purge: func() {
			if ctxCache != nil {
				ctxCache.Invalidate()
			}
		},
	})
	var joinSeats JoinSeats
	var seatGuard SeatGuard
	var planLimit GymClientLimit
	var contextEnts ContextEntitlementResolver
	var policyOpts []GymPolicyOption
	if seatsOn {
		joinSeats, seatGuard = seatSvc, seatSvc
		policyOpts = append(policyOpts, WithSeatStanding(seatSvc))
		planLimit = &gymPlanLimit{summaries: gymSummaries, grandfather: config.Gym.SeatGrandfather}
		entitlementService.WithSeatOnlyCheck(seatSvc)
		authService.WithSeatOnlySignup(true)
		personalFeatures := func(ctx context.Context, userID string) ([]string, bool, *models.ApiError) {
			eff, apiErr := entitlementService.resolve(ctx, userID)
			if apiErr != nil {
				return nil, false, apiErr
			}
			// An unknown plan (payments down) counts as "has a plan": fail open.
			return enabledFeatures(eff.Features), eff.Plan != nil || eff.planUnknown, nil
		}
		ctxCache = NewCachedContextResolver(NewEntitlementSources(NewPersonalSource(entitlementService, seatSvc),
			NewGymSource(seatSvc, personalFeatures, entitlementService.loadDefaults)), contextEntitlementsTTL)
		contextEnts = ctxCache
	}
	gymOwnerInviteService := NewGymOwnerInviteService(
		gymOwnerInviteRepo,
		userRepo,
		adminAuditRepo,
		notification,
		time.Duration(config.Gym.OwnerInviteTTLHours)*time.Hour,
		config.Gym.ClientAppURL,
	)
	authService.WithGymOwnerInvites(gymOwnerInviteService)
	var gymJoinMailer GymJoinMailer
	if notification != nil {
		gymJoinMailer = notification
	}
	quota := NewQuotaGuard(entitlementService, gymClientRepo, planLimit)
	gymClientDeps := GymClientDeps{
		Authz: authz, Members: membership, Store: gymClientRepo, Team: teamRepo, Links: coachRepo, Audit: teamRepo,
		Quota: quota, Inval: authz, Bump: gymPolicyRepo,
	}
	plansOn := config.Gym.MembershipPlansEnabled && payment != nil && config.PaymentAPI.APIKey != ""
	var premium PremiumAnnotator
	if plansOn {
		gymClientDeps.Billing = NewPaymentsMembershipBilling(payment)
		premium = NewPremiumAnnotator(payment, userRepo)
		gymClientDeps.Premium = premium
	}
	if notification != nil {
		gymClientDeps.Mailer, gymClientDeps.Users = notification, userRepo
	}
	if seatsOn {
		gymClientDeps.Seats = seatSvc
	}
	if chatClient != nil && gymRepo != nil {
		gymClientDeps.Chat, gymClientDeps.Gyms = chatClient, gymRepo
	}
	gymClients := NewGymClientService(gymClientDeps, config.Gym.ClientGymRelations)
	// Dispatch (P6): the transactional service replaces the P4 one behind the same routes. Off: nothing changes.
	var gymLeaver GymClientLeaver
	var departures CoachDepartures
	var addonAssigner AddonAssigner
	if config.Gym.DispatchEnabled && config.Gym.ClientGymRelations && dispatch != nil && dispatch.Repo != nil {
		dd := GymDispatchDeps{Repo: dispatch.Repo, Users: userRepo}
		if dispatch.Tracker != nil {
			dd.Tracker = dispatch.Tracker
		}
		if chatClient != nil {
			dd.Chat = chatClient
		}
		if notification != nil {
			dd.Mailer = notification
		}
		if plansOn {
			dd.Billing = NewPaymentsMembershipBilling(payment)
		}
		d := NewGymDispatch(gymClientDeps, dd)
		gymClients, gymLeaver, departures, addonAssigner = d, d, d, d
		authService.WithCoachDepartures(d)
	}
	// Owner chats need the chat client; a nil client (tests) disables them.
	var gymChat *GymChatProvisioner
	if chatClient != nil {
		gymChat = NewGymChatProvisioner(NewGymChatStore(gymRepo, teamRepo), chatClient, nil)
	}
	gymService := NewGymService(userRepo, gymRepo, GymSettings{
		Authz:         authz,
		AuthzEnforce:  config.Gym.AuthzEnforce,
		InviteTTL:     time.Duration(config.Gym.InviteTTLHours) * time.Hour,
		InvitesPerDay: config.Gym.InvitesPerDay,
		ClientURL:     config.Gym.ClientAppURL,
		Mailer:        gymJoinMailer,
		Chat:          gymChat,
		Members:       memberStore,
		Team:          teamRepo,

		FlatHierarchy: config.Gym.FlatHierarchy,
		Projector:     teamRepo,
		Departures:    departures,
		Premium:       premium,
		Seats:         joinSeats,
		Inval:         authz,
		Bump:          gymPolicyRepo,
	})
	authService.WithGymJoins(gymService)
	registrationLinkService := NewRegistrationLinkService(
		registrationLinkRepo,
		userRepo,
	).WithEntitlements(entitlementService)

	if config.Gym.ClientGymRelations {
		coachService.WithGymRelations(coachRepo, membership)
		if premium != nil {
			coachService.WithPremium(premium)
		}
	}
	// Coaching add-ons: the internal endpoints payments-api calls (resolve a coach, a purchase happened).
	var gymAddons GymAddonService
	if plansOn {
		ad := GymAddonDeps{Members: membership, Users: userRepo, Assigner: addonAssigner}
		if seatsOn {
			ad.Seats = seatSvc
		}
		if gymRepo != nil {
			ad.Gyms = gymRepo
		}
		if notification != nil {
			ad.Mailer = notification
		}
		if tc, err := tracker.NewTrackerClient(config.TrackerAPI); err == nil {
			ad.Notifier = tc
		}
		gymAddons = NewGymAddonService(ad)
	}

	// Unified invite links: always built (404 while INVITE_LINKS_V2 is off); only the flag wires them into signup.
	var asyncObs []RedeemObserver
	if chatClient != nil {
		asyncObs = append(asyncObs, NewEnsureConversationObserver(chatClient))
	}
	var emailInvites emailInviteAcceptor
	if invitation != nil {
		emailInvites = invitation
	}
	inviteKinds := []InvitationKind{
		NewClientPersonalKind(entitlementService, coachService),
		NewClientGymKind(authz, membership, gymRepo, gymClients, gymClientRepo, config.Gym.ClientGymRelations),
		NewLegacyEmailKind(emailInvites),
	}
	inviteKinds = append(inviteKinds, NewMemberKinds(gymService, gymService, seatGuard, membership)...)
	var inviteMailer InviteMailer
	if notification != nil {
		inviteMailer = notification
	}
	inviteLinks := NewInviteLinkService(InviteLinkDeps{
		Mailer: inviteMailer,
		Store: inviteLinkRepo, RegLinks: registrationLinkRepo, Users: userRepo, Gyms: gymRepo, Members: membership,
		Authz: authz, Joiner: gymService, Registry: NewInviteKindRegistry(inviteKinds...),
		Observers: NewInviteObservers(nil, asyncObs), ClientURL: config.Gym.ClientAppURL,
	}, config.Gym.InviteLinksV2)
	if config.Gym.InviteLinksV2 {
		authService.WithInviteLinks(inviteLinks)
	}

	return &Services{
		AuthService:             authService,
		CoachService:            coachService,
		CoachProfile:            NewCoachProfileService(userRepo, coachRepo, config.Gym.ClientGymRelations),
		AdminService:            NewAdminService(userRepo, coachRepo, adminAuditRepo, authService),
		TeamService:             NewTeamService(userRepo, teamRepo, WithGymMembership(gymRepo), WithGymChat(gymChat), WithGymMembers(memberStore), WithReadCache(15*time.Second), WithCoachDepartures(departures)),
		GymService:              gymService,
		AnalyticsScopeService:   NewAnalyticsScopeService(userRepo, repositories.NewAnalyticsRepository(teamRepo, gymRepo), config.Analytics.ScopeMaxCoaches),
		RegistrationLinkService: registrationLinkService,
		UserStatus:              userRepo,
		EntitlementService:      entitlementService,
		GymOwnerInviteService:   gymOwnerInviteService,
		GymMembership:           membership,
		GymMembers:              NewGymMembersService(authz, memberStore, teamRepo, config.Gym.FlatHierarchy),
		GymClients:              gymClients,
		InviteLinks:             inviteLinks,
		Quota:                   quota,
		GymLeaver:               gymLeaver,
		Seats:                   seatSvc,
		ContextEntitlements:     contextEnts,
		GymAddons:               gymAddons,
		GymPolicy:               NewGymPolicyService(authz, chain, membership, gymPolicyRepo, gymRepo, userRepo, authz, config.Gym.AuthzEnforce, policyOpts...),
	}
}

// UserStatusLookup returns an account's current status; the auth middleware uses it to
// reject blocked accounts whose JWT is still valid.
type UserStatusLookup interface {
	GetStatusByExternalID(ctx context.Context, externalID string) (db.UserStatus, error)
}
