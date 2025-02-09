package oauth2

import (
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"context"

	"golang.org/x/oauth2"
)

type Provider interface {
	GetLoginURL(state string) string
	ExchangeCode(ctx context.Context, code string) (*oauth2.Token, *models.ApiError)
	GetUserInfo(ctx context.Context, token *oauth2.Token) (*db.OAuthUser, *models.ApiError)
}
