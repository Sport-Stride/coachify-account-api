package oauth2

import (
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/utils"
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
)

type GoogleProvider struct {
	config *oauth2.Config
}

func NewGoogleProvider(clientID, clientSecret, redirectURL string) *GoogleProvider {
	return &GoogleProvider{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/userinfo.email"},
			Endpoint:     google.Endpoint,
		},
	}
}

func (p *GoogleProvider) GetLoginURL(state string) string {
	return p.config.AuthCodeURL(state)
}

func (p *GoogleProvider) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, *models.ApiError) {
	token, err := p.config.Exchange(ctx, code)
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrFailedToExchangeCode,
		}
	}
	return token, nil
}

func (p *GoogleProvider) GetUserInfo(ctx context.Context, token *oauth2.Token) (*db.OAuthUser, *models.ApiError) {
	// Validate ID token instead of using userinfo endpoint
	idToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidIDToken,
		}
	}

	// Validate the ID token with Google's certificates
	payload, err := idtoken.Validate(ctx, idToken, p.config.ClientID)
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: fmt.Errorf("invalid ID token: %v", err),
		}
	}

	// Extract claims from validated token
	claims := payload.Claims
	email, _ := claims["email"].(string)
	givenName, _ := claims["given_name"].(string)
	familyName, _ := claims["family_name"].(string)
	picture, _ := claims["picture"].(string)

	if email == "" {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrEmailNotProvided,
		}
	}

	// Encrypt sensitive tokens before storage
	encryptedAccess, err := utils.Encrypt(token.AccessToken, []byte(utils.LoadConfig().GetProviderEncryptionKey()))
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrTokenEncryptionFailed,
		}
	}

	encryptedRefresh, err := utils.Encrypt(token.RefreshToken, []byte(utils.LoadConfig().GetProviderEncryptionKey()))
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrTokenEncryptionFailed,
		}
	}

	return &db.OAuthUser{
		ProviderType:   "google",
		ProviderID:     claims["sub"].(string), // Google's unique user ID
		Email:          email,
		FirstName:      givenName,
		LastName:       familyName,
		ProfilePicture: picture,
		AccessToken:    encryptedAccess,
		RefreshToken:   encryptedRefresh,
		Expiry:         token.Expiry,
	}, nil
}
