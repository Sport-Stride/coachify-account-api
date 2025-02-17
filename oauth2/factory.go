package oauth2

import "errors"

type ProviderType string

const (
	ProviderFacebook ProviderType = "facebook"
	ProviderGoogle   ProviderType = "google"
)

func NewProvider(providerType ProviderType, clientID, clientSecret, redirectURL, encryptionKey string) (Provider, error) {
	switch providerType {
	case ProviderFacebook:
		return NewFacebookProvider(clientID, clientSecret, redirectURL, encryptionKey), nil
	case ProviderGoogle:
		return NewGoogleProvider(clientID, clientSecret, redirectURL, encryptionKey), nil
	default:
		return nil, errors.New("unsupported provider")
	}
}
