package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"coachify-account-api/models"
	"coachify-account-api/utils"
)

type FacebookService interface {
	Authenticate(ctx context.Context, accessToken string) (*models.FacebookUser, error)
}

type FacebookServiceImpl struct {
	config utils.AppConfig
}

func NewFacebookService(config utils.AppConfig) FacebookService {
	return &FacebookServiceImpl{config: config}
}

func (s *FacebookServiceImpl) Authenticate(ctx context.Context, accessToken string) (*models.FacebookUser, error) {
	url := fmt.Sprintf("%s/me?access_token=%s&fields=id,name,email", s.config.FacebookEndpoint, accessToken)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var fbUser models.FacebookUser
	if err := json.Unmarshal(body, &fbUser); err != nil {
		return nil, err
	}

	return &fbUser, nil
}
