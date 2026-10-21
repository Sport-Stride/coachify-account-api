package utils

import (
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

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
