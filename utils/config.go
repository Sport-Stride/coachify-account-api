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
	ConfirmResetPasswordTemplate    string
	ChatAPI                         ChatAPIConfig
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
}

// AnalyticsConfig holds the analytics scope limits.
type AnalyticsConfig struct {
	ScopeMaxCoaches int
}

// GymConfig holds the optional gym invitation limits.
type GymConfig struct {
	InviteTTLHours int
	InvitesPerDay  int
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
	URL           string
	PlanHexCoach  string
	PlanHexClient string
}
type ChatAPIConfig struct {
	URL string
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
				URL:           getStringWithDefault("PAYMENT_API_URL", "https://api.coachify.tn/payments"),
				PlanHexCoach:  getStringWithDefault("PLAN_HEX_COACH", "68a312e60fed38a0afe2cd57"),
				PlanHexClient: getStringWithDefault("PLAN_HEX_CLIENT", "68b1c82ff8a8e4136caa57bd"),
			},
			ConfirmationCodeTemplatePending: getStringWithDefault("CONFIRMATION_CODE_TEMPLATE_PENDING", "confirmation_code_pending"),
			ResendConfirmationTemplate:      getStringWithDefault("RESEND_CONFIRMATION_TEMPLATE", "resend_confirmation"),
			ResetPasswordTemplate:           getStringWithDefault("RESET_PASSWORD_TEMPLATE", "reset_password"),
			ConfirmResetPasswordTemplate:    getStringWithDefault("CONFIRM_RESET_PASSWORD_TEMPLATE", "confirm_reset_password"),
			NotificationAPI: NotificationAPIConfig{
				URL:    getStringWithDefault("NOTIFICATION_API_URL", "https://coachify-notification-api-b1f95c31a09f.herokuapp.com"),
				APIKey: internalKey,
			},
			ChatAPI: ChatAPIConfig{
				URL: getStringWithDefault("CHAT_API_URL", "https://api.coachify.tn/chat"),
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
			GoogleEncryptionKey:   getStringWithDefault("GOOGLE_ENCRYPTION_KEY", ""),
			CoachifyEncryptionKey: getStringWithDefault("COACHIFY_ENCRYPTION_KEY", ""),
			CoachifySecretKey:     getStringWithDefault("COACHIFY_SECRET_KEY", ""),
			WelcomeTemplate:       getStringWithDefault("WELCOME_TEMPLATE", "welcome_template"),
			InternalAPIKey:        internalKey,
		}

		cfg.Gym = GymConfig{
			InviteTTLHours: getIntWithDefault("GYM_INVITE_TTL_HOURS", 168),
			InvitesPerDay:  getIntWithDefault("GYM_INVITES_PER_DAY", 20),
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
