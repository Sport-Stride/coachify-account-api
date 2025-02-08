package oauth2

import "errors"

type ProviderType string

const (
	ProviderFacebook ProviderType = "facebook"
	ProviderGoogle   ProviderType = "google"
)

func NewProvider(providerType ProviderType, clientID, clientSecret, redirectURL string) (Provider, error) {
	switch providerType {
	case ProviderFacebook:
		return NewFacebookProvider(clientID, clientSecret, redirectURL), nil
	case ProviderGoogle:
		return NewGoogleProvider(clientID, clientSecret, redirectURL), nil
	default:
		return nil, errors.New("unsupported provider")
	}
}
