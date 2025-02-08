package oauth2

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/facebook"
)

type FacebookProvider struct {
	config *oauth2.Config
}

func NewFacebookProvider(clientID, clientSecret, redirectURL string) *FacebookProvider {
	return &FacebookProvider{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"public_profile", "email"},
			Endpoint:     facebook.Endpoint,
		},
	}
}

func (p *FacebookProvider) GetLoginURL(state string) string {
	return p.config.AuthCodeURL(state)
}

func (p *FacebookProvider) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	return p.config.Exchange(ctx, code)
}

func (p *FacebookProvider) GetUserInfo(ctx context.Context, token *oauth2.Token) (map[string]interface{}, error) {
	client := p.config.Client(ctx, token)
	resp, err := client.Get("https://graph.facebook.com/v13.0/me?fields=name,email")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %v", err)
	}
	defer resp.Body.Close()

	var userInfo map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, fmt.Errorf("failed to decode user info: %v", err)
	}

	return userInfo, nil
}
