package oauth2

import (
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"context"

	"golang.org/x/oauth2"
)

// TokenClaims is the identity taken from a provider-validated ID token.
// It is the only trusted source of who the caller is; client-sent profiles are not.
type TokenClaims struct {
	Email         string
	Subject       string
	EmailVerified bool
	GivenName     string
	FamilyName    string
	Picture       string
}

type Provider interface {
	GetLoginURL(state string) string
	ExchangeCode(ctx context.Context, code string) (*oauth2.Token, *models.ApiError)
	GetUserInfo(ctx context.Context, token *oauth2.Token) (*db.OAuthUser, *models.ApiError)
	RefreshToken(ctx context.Context, encryptedRefreshToken string) (*db.OAuthProviderDetails, *models.ApiError)
	// ValidateToken verifies the ID token with the provider and returns its claims.
	ValidateToken(ctx context.Context, idtoken string) (*TokenClaims, *models.ApiError)
}
