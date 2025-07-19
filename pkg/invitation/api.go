package invitation

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

// InvitationClient represents a client to connect to the invitation API
type InvitationClient struct {
	httpClient           *http.Client
	baseURL              string
	validateEndpoint     string
	checkEmailEndpoint   string
	acceptInviteEndpoint string
}

// InvitationResponse represents the response structure from the invitation API
type InvitationResponse struct {
	Success    bool       `json:"success"`
	Invitation Invitation `json:"invitation,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// Invitation represents the invitation entity returned from the invitation API
type Invitation struct {
	ExternalID   string    `json:"external_id"`
	SenderID     string    `json:"sender_id"`
	SenderName   string    `json:"sender_name"`
	Status       string    `json:"status"`
	ReceiverMail string    `json:"receiver_mail"`
	Content      string    `json:"content,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ValidatedAt  time.Time `json:"validated_at,omitempty"`
	AcceptedAt   time.Time `json:"accepted_at,omitempty"`
}

// AcceptInvitationRequest represents the request body for accepting an invitation
type AcceptInvitationRequest struct {
	ExternalID string `json:"invitation_id"`
	Email      string `json:"email"`
}

// NewInvitationClient creates a new API client for the invitation service
func NewInvitationClient(cfg utils.InvitationAPIConfig) (*InvitationClient, error) {
	return &InvitationClient{
		httpClient: &http.Client{
			Timeout: 50 * time.Second,
		},
		baseURL:              cfg.URL,
		validateEndpoint:     "%s/api/invitations/validate",
		checkEmailEndpoint:   "%s/api/invitations/check",
		acceptInviteEndpoint: "%s/api/invitations/accept",
	}, nil
}

// CheckInvitationByEmail checks if an email exists in the invitation list and returns the coach external id if exists
func (c *InvitationClient) CheckInvitationByEmail(ctx context.Context, email string) (exists bool, coachExternalID string, apiErr *models.ApiError) {
	url := fmt.Sprintf(c.checkEmailEndpoint, c.baseURL)
	payload := map[string]string{"email": email}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return false, "", models.NewApiError(http.StatusInternalServerError, err)
	}
	req, err := c.do(ctx, http.MethodPost, url, jsonBody)
	if err != nil {
		return false, "", models.NewApiError(http.StatusInternalServerError, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", models.NewApiError(http.StatusInternalServerError, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", models.NewApiError(http.StatusInternalServerError, err)
	}
	if resp.StatusCode == http.StatusOK {
		var result struct {
			Exists          bool   `json:"exists"`
			CoachExternalID string `json:"coach_external_id"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return false, "", models.NewApiError(http.StatusInternalServerError, err)
		}
		return result.Exists, result.CoachExternalID, nil
	}
	return false, "", nil
}

// ValidateInvitation validates an invitation by ID and email
func (c *InvitationClient) ValidateInvitation(ctx context.Context, invitationID, email string) (*Invitation, *models.ApiError) {
	// Create the request URL
	url := fmt.Sprintf(c.validateEndpoint, c.baseURL)

	// Create request body
	requestBody := AcceptInvitationRequest{
		ExternalID: invitationID,
		Email:      email,
	}

	// Marshal request body to JSON
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUnmarshalResponse)
	}

	// Create the request
	req, err := c.do(ctx, http.MethodPost, url, jsonBody)
	if err != nil {
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToCreateRequest)
	}

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToSendRequest)
	}
	defer resp.Body.Close()

	// Read the response body
	bodyResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToReadResponse)
	}

	// Handle different HTTP response codes
	switch resp.StatusCode {
	case http.StatusOK:
		var response InvitationResponse
		if err := json.Unmarshal(bodyResp, &response); err != nil {
			return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUnmarshalResponse)
		}

		if !response.Success {
			return nil, models.NewApiError(http.StatusBadRequest, fmt.Errorf("invitation validation failed: %s", response.Error))
		}

		return &response.Invitation, nil
	case http.StatusNotFound:
		return nil, models.NewApiError(http.StatusNotFound, fmt.Errorf("invitation not found or invalid"))
	case http.StatusBadRequest:
		return nil, models.NewApiError(http.StatusBadRequest, fmt.Errorf("invitation bad request"))
	default:
		return nil, models.NewApiError(http.StatusInternalServerError, fmt.Errorf("%w: %d, body: %s", models.ErrUnexpectedStatusCode, resp.StatusCode, string(bodyResp)))
	}
}

// AcceptInvitation accepts an invitation after user registration
func (c *InvitationClient) AcceptInvitation(ctx context.Context, invitationID, email, UserRefreshToken string) (*Invitation, *models.ApiError) {
	// Create the request URL
	url := fmt.Sprintf(c.acceptInviteEndpoint, c.baseURL)

	// Create request body
	requestBody := AcceptInvitationRequest{
		ExternalID: invitationID,
		Email:      email,
	}
	
	// Marshal request body to JSON
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUnmarshalResponse)
	}

	// Create the request
	req, err := c.do(ctx, http.MethodPost, url, jsonBody)
	if err != nil {
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToCreateRequest)
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+UserRefreshToken)
	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToSendRequest)
	}
	defer resp.Body.Close()

	// Read the response body
	bodyResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToReadResponse)
	}

	// Handle different HTTP response codes
	switch resp.StatusCode {
	case http.StatusOK:
		var response InvitationResponse
		if err := json.Unmarshal(bodyResp, &response); err != nil {
			return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUnmarshalResponse)
		}

		if !response.Success {
			return nil, models.NewApiError(http.StatusBadRequest, fmt.Errorf("invitation acceptance failed: %s", response.Error))
		}

		return &response.Invitation, nil
	case http.StatusNotFound:
		return nil, models.NewApiError(http.StatusNotFound, fmt.Errorf("invitation not found"))
	case http.StatusBadRequest:
		return nil, models.NewApiError(http.StatusBadRequest, fmt.Errorf("invitation bad request"))
	default:
		return nil, models.NewApiError(http.StatusInternalServerError, fmt.Errorf("%w: %d, body: %s", models.ErrUnexpectedStatusCode, resp.StatusCode, string(bodyResp)))
	}
}

// Utility method 'do' to create the request
func (c *InvitationClient) do(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	// Add necessary headers
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
