package utils

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestLoadConfig_SingletonWithEnvAndDefaults(t *testing.T) {
	cfg := LoadConfig()
	require.NotNil(t, cfg)
	assert.Same(t, cfg, LoadConfig())
	assert.Equal(t, testSecret, cfg.CoachifySecretKey)
	assert.Equal(t, testEncKey, cfg.CoachifyEncryptionKey)
	assert.Equal(t, 168, cfg.Gym.InviteTTLHours)
	assert.Equal(t, 5000, cfg.Analytics.ScopeMaxCoaches)
	assert.NotNil(t, cfg.GoogleOAuth)
	assert.NotNil(t, cfg.FacebookOAuth)
}

func TestConfigHelpers(t *testing.T) {
	assert.Equal(t, "dflt", getStringWithDefault("UNSET_KEY_FOR_TEST", "dflt"))
	assert.Equal(t, 7, getIntWithDefault("UNSET_INT_FOR_TEST", 7))
	t.Setenv("SET_INT_FOR_TEST", "42")
	t.Setenv("SET_STR_FOR_TEST", "val")
	viper.AutomaticEnv()
	assert.Equal(t, 42, getIntWithDefault("SET_INT_FOR_TEST", 7))
	assert.Equal(t, "val", getStringWithDefault("SET_STR_FOR_TEST", "d"))
	assert.Equal(t, "val", getStringRequired("SET_STR_FOR_TEST"))
	assert.Panics(t, func() { getStringRequired("UNSET_REQUIRED_FOR_TEST") })
}

func TestValidateReportsMissingNamesOnly(t *testing.T) {
	cfg := &AppConfig{FacebookOAuth: &oauth2.Config{}, GoogleOAuth: &oauth2.Config{}, CoachifySecretKey: "x"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing secrets")
	}
	for _, name := range []string{"MONGODB_URI", "FACEBOOK_APP_SECRET", "GOOGLE_ENCRYPTION_KEY", "COACHIFY_ENCRYPTION_KEY"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error should name %s: %v", name, err)
		}
	}
	if strings.Contains(err.Error(), "COACHIFY_SECRET_KEY") {
		t.Errorf("COACHIFY_SECRET_KEY is set and must not be reported: %v", err)
	}
}

func TestValidateOK(t *testing.T) {
	cfg := &AppConfig{
		MongoDB:               MongoConfig{URI: "x"},
		FacebookOAuth:         &oauth2.Config{ClientSecret: "x"},
		FacebookEncryptionKey: "x",
		GoogleOAuth:           &oauth2.Config{ClientSecret: "x"},
		GoogleEncryptionKey:   "x",
		CoachifyEncryptionKey: "x",
		CoachifySecretKey:     "x",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
