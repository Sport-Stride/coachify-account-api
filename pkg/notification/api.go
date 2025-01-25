package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mbv-common-template-api/utils"
	"net/http"
	"time"
)

// NotificationClient represents the client for the notification service
type NotificationClient struct {
	httpClient  *http.Client
	baseURL     string
	notifyRoute string
}

// NewNotificationClient creates a new NotificationClient
func NewNotificationClient(config utils.NotificationConfig) *NotificationClient {
	return &NotificationClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL:     config.URL,
		notifyRoute: "%s/send-email",
	}
}

// SendNotification sends a notification via the notification service
func (c *NotificationClient) Send(ctx context.Context, req Request) (*Response, error) {
	// Build the request URL
	url := fmt.Sprintf(c.notifyRoute, c.baseURL)

	// Marshal the request payload
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal notification request: %w", err)
	}

	// Create an HTTP POST request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create notification request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send notification request: %w", err)
	}
	defer resp.Body.Close()

	// Read and handle the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("notification service returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var notifyResp Response
	if err := json.Unmarshal(body, &notifyResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &notifyResp, nil
}
