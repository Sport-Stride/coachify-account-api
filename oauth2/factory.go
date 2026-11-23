package oauth2

import "errors"

type ProviderType string

const (
	ProviderFacebook ProviderType = "facebook"
	ProviderGoogle   ProviderType = "google"
)

// NewProvider builds a provider. extraAudiences only apply to Google (mobile client IDs).
func NewProvider(providerType ProviderType, clientID, clientSecret, redirectURL, encryptionKey string, extraAudiences ...string) (Provider, error) {
	switch providerType {
	case ProviderFacebook:
		return NewFacebookProvider(clientID, clientSecret, redirectURL, encryptionKey), nil
	case ProviderGoogle:
		return NewGoogleProvider(clientID, clientSecret, redirectURL, encryptionKey, extraAudiences...), nil
	default:
		return nil, errors.New("unsupported provider")
	}
}
