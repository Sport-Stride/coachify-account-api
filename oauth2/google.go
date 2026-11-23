package oauth2

import (
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/utils"
	"context"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
)

type GoogleProvider struct {
	config        *oauth2.Config
	encryptionKey string
	// audiences are the client IDs accepted in an ID token sent by a client (web first, then mobile).
	audiences []string
}

// NewGoogleProvider builds the Google provider. extraAudiences are the mobile (Android, iOS) client IDs
// accepted by ValidateToken on top of clientID; the web code-exchange flow keeps clientID only.
func NewGoogleProvider(clientID, clientSecret, redirectURL, encryptionKey string, extraAudiences ...string) *GoogleProvider {
	audiences := []string{clientID}
	for _, a := range extraAudiences {
		if a != "" && a != clientID {
			audiences = append(audiences, a)
		}
	}
	return &GoogleProvider{
		audiences: audiences,
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/userinfo.email"},
			Endpoint:     google.Endpoint,
		},
		encryptionKey: encryptionKey,
	}
}

func (p *GoogleProvider) GetLoginURL(state string) string {
	return p.config.AuthCodeURL(state)
}

func (p *GoogleProvider) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, *models.ApiError) {
	token, err := p.config.Exchange(ctx, code)
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrFailedToExchangeCode,
		}
	}
	return token, nil
}

func (p *GoogleProvider) ValidateToken(ctx context.Context, idToken string) (*TokenClaims, *models.ApiError) {

	// Validate signature and expiry with Google's certificates. No audience is passed here: it is
	// checked below against every accepted client ID (web, Android, iOS), then the issuer.
	payload, err := idtoken.Validate(ctx, idToken, "")
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: fmt.Errorf("invalid ID token: %v", err),
		}
	}

	if !p.acceptsAudience(payload.Audience) {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidAudience,
		}
	}
	// idtoken.Validate does not check the issuer: only Google's own may sign in users.
	if !googleIssuer(payload.Issuer) {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidIDToken,
		}
	}
	return claimsFromPayload(payload.Claims), nil
}

// googleIssuer reports whether iss is one of the two issuers Google uses for ID tokens.
func googleIssuer(iss string) bool {
	return iss == "accounts.google.com" || iss == "https://accounts.google.com"
}

// acceptsAudience reports whether aud is one of the configured client IDs.
func (p *GoogleProvider) acceptsAudience(aud string) bool {
	if aud == "" {
		return false
	}
	for _, a := range p.audiences {
		if a == aud {
			return true
		}
	}
	return false
}

// claimsFromPayload extracts the identity claims; email_verified may arrive as a bool or a string.
func claimsFromPayload(c map[string]interface{}) *TokenClaims {
	str := func(k string) string { s, _ := c[k].(string); return s }
	verified := false
	switch v := c["email_verified"].(type) {
	case bool:
		verified = v
	case string:
		verified = v == "true"
	}
	return &TokenClaims{
		Email:         str("email"),
		Subject:       str("sub"),
		EmailVerified: verified,
		GivenName:     str("given_name"),
		FamilyName:    str("family_name"),
		Picture:       str("picture"),
	}
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

	// Validate audience
	if payload.Claims["aud"].(string) != p.config.ClientID {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidAudience,
		}

	}

	// Extract claims from validated token
	claims := payload.Claims
	email, _ := claims["email"].(string)
	givenName, _ := claims["given_name"].(string)
	familyName, _ := claims["family_name"].(string)
	picture, _ := claims["picture"].(string)
	//providerID := claims["sub"].(string)

	if email == "" {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrEmailNotProvided,
		}
	}

	// Encrypt sensitive tokens before storage
	encryptedAccess, err := utils.Encrypt(token.AccessToken, []byte(p.encryptionKey))
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrTokenEncryptionFailed,
		}
	}

	encryptedRefresh, err := utils.Encrypt(token.RefreshToken, []byte(p.encryptionKey))
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

// Add to Provider interface
func (p *GoogleProvider) RefreshToken(ctx context.Context, encryptedRefreshToken string) (*db.OAuthProviderDetails, *models.ApiError) {
	// Decrypt refresh token
	decryptedRefresh, err := utils.Decrypt(encryptedRefreshToken, []byte(p.encryptionKey))
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: fmt.Errorf("refresh token decryption failed: %v", err),
		}
	}

	// Get new token
	token := &oauth2.Token{
		RefreshToken: decryptedRefresh,
		Expiry:       time.Now().Add(-1 * time.Hour),
	}
	newToken, err := p.config.TokenSource(ctx, token).Token()
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: fmt.Errorf("token refresh failed: %v", err),
		}
	}

	// Encrypt new tokens
	encryptedAccess, err := utils.Encrypt(newToken.AccessToken, []byte(p.encryptionKey))
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrTokenEncryptionFailed,
		}
	}

	encryptedRefresh, err := utils.Encrypt(newToken.RefreshToken, []byte(p.encryptionKey))
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrTokenEncryptionFailed,
		}
	}

	return &db.OAuthProviderDetails{
		AccessToken:  encryptedAccess,
		RefreshToken: encryptedRefresh,
		Expiry:       newToken.Expiry,
	}, nil
}
