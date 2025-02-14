package oauth2

import (
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
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
	client := p.config.Client(ctx, token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
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

	var googleUserInfo struct {
		ID         string `json:"id"`
		Email      string `json:"email"`
		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
		Picture    string `json:"picture"`
	}

	if err := json.Unmarshal(body, &googleUserInfo); err != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrFailedToUnmarshalResponse,
		}
	}
	if googleUserInfo.Email == "" {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrEmailNotProvided,
		}
	}
	return &db.OAuthUser{
		ProviderType:   "google",
		ProviderID:     googleUserInfo.ID,
		Email:          googleUserInfo.Email,
		FirstName:      googleUserInfo.GivenName,
		LastName:       googleUserInfo.FamilyName,
		ProfilePicture: googleUserInfo.Picture,
	}, nil
}
