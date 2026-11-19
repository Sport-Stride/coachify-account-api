package utils

import (
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/viper"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/facebook"
	"golang.org/x/oauth2/google"
)

type AppConfig struct {
	Port                            int
	BaseURL                         BaseURLConfig
	Environment                     string
	MongoDB                         MongoConfig
	Notification                    NotificationConfig
	IdentifierAPI                   IdentifierAPIConfig
	InvitationAPI                   InvitationAPIConfig
	NotificationAPI                 NotificationAPIConfig
	ConfirmationCodeTemplatePending string
	ResendConfirmationTemplate      string
	ResetPasswordTemplate           string
	GymOwnerWelcomeTemplate         string
	GymOwnerInviteTemplate          string
	GymJoinInviteTemplate           string
	GymAssignmentTemplate           string
	ConfirmResetPasswordTemplate    string
	ChatAPI                         ChatAPIConfig
	TrackerAPI                      TrackerAPIConfig
	FacebookOAuth                   *oauth2.Config
	FacebookEncryptionKey           string
	GoogleOAuth                     *oauth2.Config
	GoogleEncryptionKey             string
	CoachifyEncryptionKey           string
	CoachifySecretKey               string
	WelcomeTemplate                 string
	PaymentAPI                      PaymentAPIConfig
	Gym                             GymConfig
	Analytics                       AnalyticsConfig
	InternalAPIKey                  string
	InternalAPIKeyRequired          bool
}

// AnalyticsConfig holds the analytics scope limits.
type AnalyticsConfig struct {
	ScopeMaxCoaches int
}

// GymConfig holds the optional gym invitation limits.
type GymConfig struct {
	InviteTTLHours      int
	InvitesPerDay       int
	OwnerInviteTTLHours int
	ClientAppURL        string
	// MembersRead selects the gym membership read path: "legacy" (default) or "new" (gym_members).
	MembersRead string
	// AuthzEnforce (GYM_AUTHZ_ENFORCE, default off) routes gym owner and head coach checks through the Authorizer.
	// Off: legacy checks stay and the internal authz endpoint reports instead of denying.
	AuthzEnforce bool
	// FlatHierarchy (GYM_FLAT_HIERARCHY, default off) lets a coach join a gym without a head coach and enables
	// the member list and supervisor endpoints. Off: legacy behavior. On: gym_members is the membership source.
	FlatHierarchy bool
	// ClientGymRelations (CLIENT_GYM_RELATIONS, default off) enables the gym client relationship endpoints, the kind
	// filter of GET /coach/clients and the gym leg of the link check. Off: legacy behavior. The privacy rule that gym
	// and head coach views read gym_clients only applies regardless of this flag.
	ClientGymRelations bool
	// InviteLinksV2 (INVITE_LINKS_V2, default off) routes registration links, gym join links and email invites through
	// the unified InviteLink aggregate and enables /invite-links and /me/invites. Off: legacy behavior.
	InviteLinksV2 bool
	// DispatchEnabled (GYM_DISPATCH_ENABLED, default off) routes gym client assign, unassign, remove and leave and
	// the coach departure cleanup through one Mongo transaction (needs a replica set; startup fails fast without
	// one) and the tracker transfer. Requires CLIENT_GYM_RELATIONS. Off: legacy behavior.
	DispatchEnabled bool
	// SeatsEnabled (GYM_SEATS_ENABLED, default off) turns on gym seats: seat checks at join, the seat endpoints, the
	// gym entitlement source and the plan based max_gym_clients. Off: no payments gym calls, behavior unchanged.
	SeatsEnabled bool
	// SeatGrandfather (GYM_SEAT_GRANDFATHER, default on) keeps gyms with no subscription unlimited and seated.
	SeatGrandfather bool
	// SeatOverflowGraceDays (GYM_SEAT_OVERFLOW_GRACE_DAYS, default 7) is the grace of seats above the purchased total.
	SeatOverflowGraceDays int
	// MembershipPlansEnabled (GYM_MEMBERSHIP_PLANS_ENABLED, default off) cancels the gym membership plan
	// subscription at the period end when a client leaves or is removed from a gym (payments-api call; needs
	// GYM_DISPATCH_ENABLED and the payments key). Off: no extra calls.
	MembershipPlansEnabled bool
}

type BaseURLConfig struct {
	URL string
}

type MongoConfig struct {
	URI string
}

type NotificationConfig struct {
	MailgunDomain string
	MailgunAPIKey string
}

type IdentifierAPIConfig struct {
	URL    string
	APIKey string
}
type NotificationAPIConfig struct {
	URL    string
	APIKey string
}
type InvitationAPIConfig struct {
	URL    string
	APIKey string
}
type PaymentAPIConfig struct {
	URL    string
	APIKey string
}
type TrackerAPIConfig struct {
	URL    string
	APIKey string
}
type ChatAPIConfig struct {
	URL    string
	APIKey string
}

var (
	configInstance *AppConfig
	configOnce     sync.Once
)

func LoadConfig() *AppConfig {
	configOnce.Do(func() {
		viper.AutomaticEnv()

		internalKey := getStringWithDefault("INTERNAL_API_KEY", "")

		cfg := &AppConfig{
			Port:        getIntWithDefault("PORT", 8086),
			Environment: getStringWithDefault("ENVIRONMENT", "development"),
			BaseURL: BaseURLConfig{
				URL: getStringWithDefault("BASE_URL", "http://localhost:8086"),
			},
			MongoDB: MongoConfig{
				URI: getStringWithDefault("MONGODB_URI", ""),
			},
			Notification: NotificationConfig{
				MailgunAPIKey: getStringWithDefault("MAILGUN_API_KEY", ""),
				MailgunDomain: getStringWithDefault("MAILGUN_DOMAIN", "http://localhost:8080"),
			},
			IdentifierAPI: IdentifierAPIConfig{
				URL:    getStringWithDefault("IDENTIFIER_API_URL", "http://localhost:8084"),
				APIKey: internalKey,
			},
			InvitationAPI: InvitationAPIConfig{
				URL:    getStringWithDefault("INVITATION_API_URL", "https://api.coachify.tn/invitation"),
				APIKey: internalKey,
			},
			PaymentAPI: PaymentAPIConfig{
				URL:    getStringWithDefault("PAYMENT_API_URL", "https://api.coachify.tn/payments"),
				APIKey: getStringWithDefault("PAYMENT_API_KEY", internalKey),
			},
			ConfirmationCodeTemplatePending: getStringWithDefault("CONFIRMATION_CODE_TEMPLATE_PENDING", "confirmation_code_pending"),
			ResendConfirmationTemplate:      getStringWithDefault("RESEND_CONFIRMATION_TEMPLATE", "resend_confirmation"),
			ResetPasswordTemplate:           getStringWithDefault("RESET_PASSWORD_TEMPLATE", "reset_password"),
			GymOwnerWelcomeTemplate:         getStringWithDefault("GYM_OWNER_WELCOME_TEMPLATE", "gym_owner_welcome"),
			GymOwnerInviteTemplate:          getStringWithDefault("GYM_OWNER_INVITE_TEMPLATE", "gym_owner_invite"),
			GymJoinInviteTemplate:           getStringWithDefault("GYM_JOIN_INVITE_TEMPLATE", "gym_join_invite"),
			GymAssignmentTemplate:           getStringWithDefault("GYM_ASSIGNMENT_TEMPLATE", "gym_assignment"),
			ConfirmResetPasswordTemplate:    getStringWithDefault("CONFIRM_RESET_PASSWORD_TEMPLATE", "confirm_reset_password"),
			NotificationAPI: NotificationAPIConfig{
				URL:    getStringWithDefault("NOTIFICATION_API_URL", "https://coachify-notification-api-b1f95c31a09f.herokuapp.com"),
				APIKey: internalKey,
			},
			TrackerAPI: TrackerAPIConfig{
				URL:    getStringWithDefault("TRACKER_API_URL", "https://api.coachify.tn/tracker"),
				APIKey: internalKey,
			},
			ChatAPI: ChatAPIConfig{
				URL:    getStringWithDefault("CHAT_API_URL", "https://api.coachify.tn/chat"),
				APIKey: internalKey,
			},
			FacebookOAuth: &oauth2.Config{
				ClientID:     getStringWithDefault("FACEBOOK_APP_ID", "9450367418317323"),
				ClientSecret: getStringWithDefault("FACEBOOK_APP_SECRET", ""),
				RedirectURL:  getStringWithDefault("FACEBOOK_REDIRECT_URL", "http://localhost:8060/oauth/facebook/callback"),
				Scopes:       []string{"public_profile", "email"},
				Endpoint:     facebook.Endpoint,
			},
			FacebookEncryptionKey: getStringWithDefault("FACEBOOK_ENCRYPTION_KEY", ""),
			GoogleOAuth: &oauth2.Config{
				ClientID:     getStringWithDefault("GOOGLE_APP_ID", "597425356866-6vephqb542a7brgmk7sho06ojct1tqtt.apps.googleusercontent.com"),
				ClientSecret: getStringWithDefault("GOOGLE_APP_SECRET", ""),
				RedirectURL:  getStringWithDefault("GOOGLE_REDIRECT_URL", "http://localhost:8060/oauth/google/callback"),
				Scopes:       []string{"https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/userinfo.email"},
				Endpoint:     google.Endpoint,
			},
			GoogleEncryptionKey:    getStringWithDefault("GOOGLE_ENCRYPTION_KEY", ""),
			CoachifyEncryptionKey:  getStringWithDefault("COACHIFY_ENCRYPTION_KEY", ""),
			CoachifySecretKey:      getStringWithDefault("COACHIFY_SECRET_KEY", ""),
			WelcomeTemplate:        getStringWithDefault("WELCOME_TEMPLATE", "welcome_template"),
			InternalAPIKey:         internalKey,
			InternalAPIKeyRequired: viper.GetBool("INTERNAL_API_KEY_REQUIRED"),
		}

		cfg.Gym = GymConfig{
			InviteTTLHours:      getIntWithDefault("GYM_INVITE_TTL_HOURS", 168),
			InvitesPerDay:       getIntWithDefault("GYM_INVITES_PER_DAY", 20),
			OwnerInviteTTLHours: getIntWithDefault("GYM_OWNER_INVITE_TTL_HOURS", 168),
			ClientAppURL:        getStringWithDefault("FRONTEND_URL", "http://localhost:3000"),
			MembersRead:         getStringWithDefault("GYM_MEMBERS_READ", "legacy"),
			AuthzEnforce:        viper.GetBool("GYM_AUTHZ_ENFORCE"),
			FlatHierarchy:       viper.GetBool("GYM_FLAT_HIERARCHY"),
			ClientGymRelations:  viper.GetBool("CLIENT_GYM_RELATIONS"),
			InviteLinksV2:       viper.GetBool("INVITE_LINKS_V2"),
			DispatchEnabled:     viper.GetBool("GYM_DISPATCH_ENABLED"),

			SeatsEnabled:          viper.GetBool("GYM_SEATS_ENABLED"),
			SeatGrandfather:       !viper.IsSet("GYM_SEAT_GRANDFATHER") || viper.GetBool("GYM_SEAT_GRANDFATHER"),
			SeatOverflowGraceDays: getIntWithDefault("GYM_SEAT_OVERFLOW_GRACE_DAYS", 7),

			MembershipPlansEnabled: viper.GetBool("GYM_MEMBERSHIP_PLANS_ENABLED"),
		}

		cfg.Analytics = AnalyticsConfig{
			ScopeMaxCoaches: getIntWithDefault("ANALYTICS_SCOPE_MAX_COACHES", 5000),
		}

		configInstance = cfg
	})
	return configInstance
}

// Validate fails fast when a secret this service needs is not supplied by the environment.
// Only variable names are reported, never values.
func (c *AppConfig) Validate() error {
	required := []struct{ name, value string }{
		{"MONGODB_URI", c.MongoDB.URI},
		{"FACEBOOK_APP_SECRET", c.FacebookOAuth.ClientSecret},
		{"FACEBOOK_ENCRYPTION_KEY", c.FacebookEncryptionKey},
		{"GOOGLE_APP_SECRET", c.GoogleOAuth.ClientSecret},
		{"GOOGLE_ENCRYPTION_KEY", c.GoogleEncryptionKey},
		{"COACHIFY_ENCRYPTION_KEY", c.CoachifyEncryptionKey},
		{"COACHIFY_SECRET_KEY", c.CoachifySecretKey},
	}
	var missing []string
	for _, r := range required {
		if r.value == "" {
			missing = append(missing, r.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("required environment variables are missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

func getStringRequired(key string) string {
	value := viper.GetString(key)
	if value == "" {
		panic(fmt.Sprintf("required environment variable %s is missing", key))
	}
	return value
}

func getStringWithDefault(key, defaultValue string) string {
	if value := viper.GetString(key); value != "" {
		return value
	}
	return defaultValue
}

func getIntWithDefault(key string, defaultValue int) int {
	if viper.IsSet(key) {
		return viper.GetInt(key)
	}
	return defaultValue
}

// func validateConfig(cfg *AppConfig) {
// 	// Add any additional validation logic here
// 	if len(cfg.providerEncryptionKey) < 32 {
// 		panic("provider encryption key must be at least 32 characters long")
// 	}
// }
