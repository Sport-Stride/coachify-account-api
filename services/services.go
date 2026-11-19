package services

import (
	"context"
	"time"

	"coachify-account-api/core"
	"coachify-account-api/models/db"
	"coachify-account-api/oauth2"
	"coachify-account-api/pkg/chat"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/invitation"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/repositories"
	"coachify-account-api/utils"

	jwt "github.com/appleboy/gin-jwt/v2"
)

type Services struct {
	AuthService             AuthService
	CoachService            CoachService
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
	})
	authService.WithGymJoins(gymService)
	registrationLinkService := NewRegistrationLinkService(
		registrationLinkRepo,
		userRepo,
	).WithEntitlements(entitlementService)

	if config.Gym.ClientGymRelations {
		coachService.WithGymRelations(coachRepo, membership)
	}
	quota := NewQuotaGuard(entitlementService, gymClientRepo, nil)
	gymClients := NewGymClientService(GymClientDeps{
		Authz: authz, Members: membership, Store: gymClientRepo, Team: teamRepo, Links: coachRepo, Audit: teamRepo,
		Quota: quota, Inval: authz, Bump: gymPolicyRepo,
	}, config.Gym.ClientGymRelations)

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
	inviteKinds = append(inviteKinds, NewMemberKinds(gymService, gymService, nil, membership)...)
	inviteLinks := NewInviteLinkService(InviteLinkDeps{
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
		AdminService:            NewAdminService(userRepo, coachRepo, adminAuditRepo, authService),
		TeamService:             NewTeamService(userRepo, teamRepo, WithGymMembership(gymRepo), WithGymChat(gymChat), WithGymMembers(memberStore), WithReadCache(15*time.Second)),
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
		GymPolicy:               NewGymPolicyService(authz, chain, membership, gymPolicyRepo, gymRepo, userRepo, authz, config.Gym.AuthzEnforce),
	}
}

// UserStatusLookup returns an account's current status; the auth middleware uses it to
// reject blocked accounts whose JWT is still valid.
type UserStatusLookup interface {
	GetStatusByExternalID(ctx context.Context, externalID string) (db.UserStatus, error)
}
