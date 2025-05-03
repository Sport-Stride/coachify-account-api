package utils

import (
	"fmt"
	"sync"

	"github.com/spf13/viper"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/facebook"
	"golang.org/x/oauth2/google"
)

type AppConfig struct {
	Port                  int
	BaseURL               BaseURLConfig
	Environment           string
	MongoDB               MongoConfig
	Notification          NotificationConfig
	IdentifierAPI         IdentifierAPIConfig
	InvitationAPI         InvitationAPIConfig
	ChatAPI               ChatAPIConfig
	FacebookOAuth         *oauth2.Config
	FacebookEncryptionKey string
	GoogleOAuth           *oauth2.Config
	GoogleEncryptionKey   string
	CoachifyEncryptionKey string
	CoachifySecretKey     string
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
	URL string
}
type InvitationAPIConfig struct {
	URL string
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

		cfg := &AppConfig{
			Port:        getIntWithDefault("PORT", 8086),
			Environment: getStringWithDefault("ENVIRONMENT", "development"),
			BaseURL: BaseURLConfig{
				URL: getStringWithDefault("BASE_URL", "http://localhost:8086"),
			},
			MongoDB: MongoConfig{
				URI: getStringWithDefault("MONGODB_URI", "mongodb+srv://sportstride727:EHPbrGUJYUellUdg@sportstride.spdvs.mongodb.net"),
			},
			Notification: NotificationConfig{
				MailgunAPIKey: getStringWithDefault("MAILGUN_API_KEY", "43c4ac5e6a25ff81f3d1e53c39ca36b3"),
				MailgunDomain: getStringWithDefault("MAILGUN_DOMAIN", "http://localhost:8080"),
			},
			IdentifierAPI: IdentifierAPIConfig{
				URL: getStringWithDefault("IDENTIFIER_API_URL", "https://coachify-identifier-api-176ecee2c6dd.herokuapp.com"),
			},
			InvitationAPI: InvitationAPIConfig{
				URL: getStringWithDefault("INVITATION_API_URL", "http://localhost:8087"),
			},
			ChatAPI: ChatAPIConfig{
				URL: getStringWithDefault("CHAT_API_URL", "http://localhost:8088"),
			},
			FacebookOAuth: &oauth2.Config{
				ClientID:     getStringWithDefault("FACEBOOK_APP_ID", "9450367418317323"),
				ClientSecret: getStringWithDefault("FACEBOOK_APP_SECRET", "43c4ac5e6a25ff81f3d1e53c39ca36b3"),
				RedirectURL:  getStringWithDefault("FACEBOOK_REDIRECT_URL", "http://localhost:8060/oauth/facebook/callback"),
				Scopes:       []string{"public_profile", "email"},
				Endpoint:     facebook.Endpoint,
			},
			FacebookEncryptionKey: getStringWithDefault("FACEBOOK_ENCRYPTION_KEY", "mR8z3Q2tP9fW7kL1xY0nC5bV6sA4jE2Z"),
			GoogleOAuth: &oauth2.Config{
				ClientID:     getStringWithDefault("GOOGLE_APP_ID", "1091267835475-c2d6n42uusnfg8ih3hv96v83gmfiqi61.apps.googleusercontent.com"),
				ClientSecret: getStringWithDefault("GOOGLE_APP_SECRET", "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"),
				RedirectURL:  getStringWithDefault("GOOGLE_REDIRECT_URL", "http://localhost:8060/oauth/google/callback"),
				Scopes:       []string{"https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/userinfo.email"},
				Endpoint:     google.Endpoint,
			},
			GoogleEncryptionKey:   getStringWithDefault("GOOGLE_ENCRYPTION_KEY", "mR8z3Q2tP9fW7kL1xY0nC5bV6sA4jE2Z"),
			CoachifyEncryptionKey: getStringWithDefault("COACHIFY_ENCRYPTION_KEY", "mR8z3Q2tP9fW7kL1xY0nC5bV6sA4jE2Z"),
			CoachifySecretKey:     getStringWithDefault("COACHIFY_SECRET_KEY", "E3F9B6F9D7914B424E58DDF91AD86"),
		}

		configInstance = cfg
	})
	return configInstance
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
