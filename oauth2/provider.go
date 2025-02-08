package oauth2

import (
	"context"

	"golang.org/x/oauth2"
)

type Provider interface {
	GetLoginURL(state string) string
	ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error)
	GetUserInfo(ctx context.Context, token *oauth2.Token) (map[string]interface{}, error)
}
