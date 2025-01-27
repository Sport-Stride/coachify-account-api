package utils

import (
	"fmt"

	"github.com/spf13/viper"
)

var errors []error

type AppConfig struct {
	Port          int
	Environment   string
	MongoDB       MongoConfig
	Notification  NotificationConfig
	IdentifierAPI IdentifierAPIConfig
}
type MongoConfig struct {
	MongoURI string
}

type NotificationConfig struct {
	URL string
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

func LoadConfig() AppConfig {
	viper.AutomaticEnv()
	viper.BindEnv("Tracker_API_URL")
	cfg := AppConfig{
		Port:        getIntWithDefault("PORT", 8060),
		Environment: getStringWithDefault("ENVIRONMENT", "development"),
		MongoDB: MongoConfig{
			MongoURI: getStringWithDefault("MONGODB_URI", "mongodb://localhost:27017"),
		},
		Notification: NotificationConfig{
			URL: getStringWithDefault("NOTIFICATION_URL", "http://localhost:8087"),
		},
		IdentifierAPI: IdentifierAPIConfig{
			URL: getStringWithDefault("IDENTIFIER_API_URL", "http://localhost:8084"),
		},
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

func getStringWithDefault(key, defaultValue string) string {
	viper.SetDefault(key, defaultValue)
	return viper.GetString(key)
}

func getIntWithDefault(key string, defaultValue int) int {
	viper.SetDefault(key, defaultValue)
	return viper.GetInt(key)
}
