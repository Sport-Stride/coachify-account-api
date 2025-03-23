package oauth2

import (
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/facebook"
	"google.golang.org/api/idtoken"
)

type FacebookProvider struct {
	config        *oauth2.Config
	encryptionKey string
}

func NewFacebookProvider(clientID, clientSecret, redirectURL, encryptionKey string) *FacebookProvider {
	return &FacebookProvider{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"public_profile", "email"},
			Endpoint:     facebook.Endpoint,
		},
		encryptionKey: encryptionKey,
	}
}

func (p *FacebookProvider) GetLoginURL(state string) string {
	return p.config.AuthCodeURL(state)
}

func (p *FacebookProvider) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, *models.ApiError) {
	token, err := p.config.Exchange(ctx, code)
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrFailedToExchangeCode,
		}
	}
	return token, nil
}
func (p *FacebookProvider) ValidateToken(ctx context.Context, idToken string) (bool, *models.ApiError) {

	// Validate the ID token with Google's certificates
	payload, err := idtoken.Validate(ctx, idToken, p.config.ClientID)
	if err != nil {
		return false, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: fmt.Errorf("invalid ID token: %v", err),
		}
	}

	// Validate audience
	if payload.Claims["aud"].(string) != p.config.ClientID {
		return false, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidAudience,
		}

	}
	return true, nil
}
func (p *FacebookProvider) GetUserInfo(ctx context.Context, token *oauth2.Token) (*db.OAuthUser, *models.ApiError) {
	client := p.config.Client(ctx, token)
	resp, err := client.Get("https://graph.facebook.com/v13.0/me?fields=id,first_name,last_name,email,picture")
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrRetrievingUser,
		}
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrFailedToReadResponse,
		}
	}

	var fbUserInfo struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Picture   struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"picture"`
	}

	if err := json.Unmarshal(body, &fbUserInfo); err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrFailedToUnmarshalResponse,
		}
	}
	//log.Printf("IBL: OAuth response from Facebook: %s", fbUserInfo.Email)
	if fbUserInfo.Email == "" {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrEmailNotProvided,
		}
	}
	return &db.OAuthUser{
		ProviderType:   "facebook",
		ProviderID:     fbUserInfo.ID,
		Email:          fbUserInfo.Email,
		FirstName:      fbUserInfo.FirstName,
		LastName:       fbUserInfo.LastName,
		ProfilePicture: fbUserInfo.Picture.Data.URL,
	}, nil
}

func (p *FacebookProvider) RefreshToken(ctx context.Context, encryptedRefreshToken string) (*db.OAuthProviderDetails, *models.ApiError) {
	return nil, nil
}
