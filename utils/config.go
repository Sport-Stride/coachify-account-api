package utils

import (
	"fmt"

	"github.com/spf13/viper"
)

var errors []error

type AppConfig struct {
	Port        int
	Environment string
}

func LoadConfig() AppConfig {
	viper.AutomaticEnv()
	cfg := AppConfig{
		Port:        getIntWithDefault("PORT", 8088),
		Environment: getStringWithDefault("ENVIRONMENT", "development"),
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
