package chat

import (
	"bytes"
	"coachify-account-api/models"
	"coachify-account-api/utils"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ChatClient is an HTTP client for the coachify-chat-api.
type ChatClient struct {
	httpClient                 *http.Client
	baseURL                    string
	createConversationEndpoint string
	breaker                    *utils.CircuitBreaker
	target                     string
}

type createConversationRequest struct {
	AdminID   string   `json:"admin_id"`
	ClientIDs []string `json:"client_ids"`
}

// NewChatClient creates a ChatClient using the ChatAPIConfig from the app config.
func NewChatClient(cfg utils.ChatAPIConfig) (*ChatClient, error) {
	return &ChatClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL:                    cfg.URL,
		createConversationEndpoint: "%s/create-conversation",
		breaker:                    utils.NewCircuitBreaker("chat-api"),
		target:                     "chat-api",
	}, nil
}

// CreateConversation opens a new coach-client conversation in the chat service.
// coachID is the admin (coach) external ID; clientID is the client external ID.
// coachToken is the coach's AES-GCM-encrypted access token for authentication.
func (c *ChatClient) CreateConversation(ctx context.Context, coachID, clientID, coachToken string) *models.ApiError {
	url := fmt.Sprintf(c.createConversationEndpoint, c.baseURL)

	body, err := json.Marshal(createConversationRequest{
		AdminID:   coachID,
		ClientIDs: []string{clientID},
	})
	if err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUnmarshalResponse)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToCreateRequest)
	}
	req.Header.Set("Content-Type", "application/json")
	if coachToken != "" {
		req.Header.Set("Authorization", "Bearer "+coachToken)
	}

	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 3*time.Second)
	if err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToSendRequest)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	default:
		return models.NewApiError(http.StatusInternalServerError,
			fmt.Errorf("%w: chat-api returned %d", models.ErrUnexpectedStatusCode, resp.StatusCode))
	}
}
