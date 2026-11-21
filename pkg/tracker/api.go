package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"coachify-account-api/utils"
)

// TrackerClient is an HTTP client for the internal assignment endpoints of coachify-tracker-api,
// authenticated by the shared internal API key (x-api-key).
type TrackerClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	breaker    *utils.CircuitBreaker
	target     string
}

// BulkResult is the answer of the internal assignment endpoints.
type BulkResult struct {
	Matched  int64 `json:"matched"`
	Modified int64 `json:"modified"`
}

type transferRequest struct {
	GymID       string `json:"gym_id"`
	ClientID    string `json:"client_id"`
	FromCoachID string `json:"from_coach_id,omitempty"`
	ToCoachID   string `json:"to_coach_id"`
	Reason      string `json:"reason,omitempty"`
}

type cancelGymRequest struct {
	GymID    string     `json:"gym_id"`
	ClientID string     `json:"client_id"`
	From     *time.Time `json:"from,omitempty"`
}

// NewTrackerClient creates a TrackerClient using the TrackerAPIConfig from the app config.
func NewTrackerClient(cfg utils.TrackerAPIConfig) (*TrackerClient, error) {
	return &TrackerClient{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		baseURL:    cfg.URL,
		apiKey:     cfg.APIKey,
		breaker:    utils.NewCircuitBreaker("tracker-api"),
		target:     "tracker-api",
	}, nil
}

// Transfer moves the gym-context assignments of a client in a gym from one coach to another; to "" returns them
// to the pool and from "" matches every coach.
func (c *TrackerClient) Transfer(ctx context.Context, gymID, clientID, fromCoachID, toCoachID, reason string) (*BulkResult, error) {
	return c.post(ctx, "/internal/assignments/transfer", transferRequest{
		GymID: gymID, ClientID: clientID, FromCoachID: fromCoachID, ToCoachID: toCoachID, Reason: reason,
	})
}

// CancelGym cancels the pending gym-context assignments of a client in a gym from the given time on (nil: now).
func (c *TrackerClient) CancelGym(ctx context.Context, gymID, clientID string, from *time.Time) (*BulkResult, error) {
	return c.post(ctx, "/internal/assignments/cancel-gym", cancelGymRequest{GymID: gymID, ClientID: clientID, From: from})
}

func (c *TrackerClient) post(ctx context.Context, path string, payload interface{}) (*BulkResult, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)

	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		utils.LogDownstreamBody(c.target, resp.StatusCode, raw)
		return nil, fmt.Errorf("tracker-api returned %d", resp.StatusCode)
	}
	var out BulkResult
	if len(bytes.TrimSpace(raw)) == 0 {
		return &out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &out, nil
}

type notifyRequest struct {
	UserID   string                 `json:"user_id"`
	Type     string                 `json:"type,omitempty"`
	Title    string                 `json:"title"`
	Message  string                 `json:"message"`
	EntityID string                 `json:"entity_id,omitempty"`
	URL      string                 `json:"url,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// Notify creates an in-app notification for a user (tracker POST /notification/external). The tracker ignores the
// top-level type, so callers put the notification type in data["type"].
func (c *TrackerClient) Notify(ctx context.Context, userID, title, message, entityID, url string, data map[string]any) error {
	if userID == "" {
		return fmt.Errorf("user id is required")
	}
	_, err := c.post(ctx, "/notification/external", notifyRequest{
		UserID: userID, Title: title, Message: message, EntityID: entityID, URL: url, Data: data,
	})
	return err
}
