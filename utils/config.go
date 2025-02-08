package utils

import (
	"fmt"

	"github.com/spf13/viper"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/facebook"
)

var errors []error

type AppConfig struct {
	Port                int
	Environment         string
	MongoDB             MongoConfig
	Notification        NotificationConfig
	IdentifierAPI       IdentifierAPIConfig
	FacebookAppID       string
	FacebookAppSecret   string
	FacebookRedirectURL string
}
type MongoConfig struct {
	MongoURI string
}

type FacebookService struct {
	oauthConfig *oauth2.Config
}
type NotificationConfig struct {
	MailgunDomain string // Mailgun domain
	MailgunAPIKey string // Mailgun API key
}

type TrackerAPIConfig struct {
	URL string
}
type IdentifierAPIConfig struct {
	URL string
}
type AccountingAPIConfig struct {
	URL string
}

var (
	OAuthConf        *oauth2.Config
	OAuthStateString = "random" // This should be a cryptographically random string in production
)

func LoadConfig() AppConfig {
	viper.AutomaticEnv()
	viper.BindEnv("Tracker_API_URL")
	cfg := AppConfig{
		Port:        getIntWithDefault("PORT", 8060),
		Environment: getStringWithDefault("ENVIRONMENT", "development"),
		MongoDB: MongoConfig{
			MongoURI: getStringWithDefault("MONGODB_URI", "mongodb+srv://sportstride727:EHPbrGUJYUellUdg@sportstride.spdvs.mongodb.net"),
		},
		Notification: NotificationConfig{
			MailgunAPIKey: getStringWithDefault("MAILGUN_API_KEY", "5a88901c9830e9c821c4de4e9e2b0a70-d8df908e-65aaaae3"),
			MailgunDomain: getStringWithDefault("MAILGUN_DOMAIN", "sandbox7caec8d24a564f0e9d63bf11c29370cd.mailgun.org"),
		},
		IdentifierAPI: IdentifierAPIConfig{
			URL: getStringWithDefault("IDENTIFIER_API_URL", "http://localhost:8084"),
		},
		FacebookAppID:       getStringWithDefault("FACEBOOK_APP_ID", "9450367418317323"),
		FacebookAppSecret:   getStringWithDefault("FACEBOOK_APP_SECRET", "43c4ac5e6a25ff81f3d1e53c39ca36b3"),
		FacebookRedirectURL: getStringWithDefault("FACEBOOK_APP_SECRET", "43c4ac5e6a25ff81f3d1e53c39ca36b3"),
	}
	if len(errors) != 0 {
		errorReport := "errors in config :\n"
		for _, err := range errors {
			errorReport += fmt.Sprintf("- %s\n", err)
		}
		panic(fmt.Errorf(errorReport))
	}

	return cfg
}
func InitOAuthConfig(config AppConfig) {
	OAuthConf = &oauth2.Config{
		ClientID:     config.FacebookAppID,
		ClientSecret: config.FacebookAppSecret,
		RedirectURL:  "http://localhost:8080/auth/facebook/callback",
		Scopes:       []string{"public_profile", "email"},
		Endpoint:     facebook.Endpoint,
	}
}
func getStringWithDefault(key, defaultValue string) string {
	viper.SetDefault(key, defaultValue)
	return viper.GetString(key)
}

func getIntWithDefault(key string, defaultValue int) int {
	viper.SetDefault(key, defaultValue)
	return viper.GetInt(key)
}
