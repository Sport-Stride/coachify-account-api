package identifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mbv-common-template-api/models"
	"mbv-common-template-api/utils"
	"net/http"
	"time"
)

// IdentifierClient represents a client to connect to the identifier API
type IdentifierClient struct {
	httpClient         *http.Client
	baseURL            string
	identifierEndpoint string
}

// NewIdentifierClient creates a new API client for the identifier service
func NewIdentifierClient(cfg utils.IdentifierAPIConfig) (*IdentifierClient, error) {
	return &IdentifierClient{
		httpClient: &http.Client{
			Timeout: 50 * time.Second,
		},
		baseURL:            cfg.URL,
		identifierEndpoint: "%s/identifier/%s", // This expects the label as part of the URL path
	}, nil
}

// GenerateId sends a request to generate a new ID based on the label
func (c *IdentifierClient) GenerateId(ctx context.Context, label string) (Entity, *models.ApiError) {
	// Create the request URL with the label
	url := fmt.Sprintf(c.identifierEndpoint, c.baseURL, label)

	// Create the request
	req, err := c.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Entity{}, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("Failed to create request to identifier service: %w", err),
		}
	}

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Entity{}, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("Failed to send request to identifier service: %w", err),
		}
	}
	defer resp.Body.Close()

	// Read the response body
	bodyResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return Entity{}, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("Error reading response body: %w", err),
		}
	}

	// Handle different HTTP response codes
	switch resp.StatusCode {
	case http.StatusOK:
		// Assuming the response contains the generated ID as a JSON object
		var result Entity
		if err := json.Unmarshal(bodyResp, &result); err != nil {
			return Entity{}, &models.ApiError{
				Code:  500,
				Error: fmt.Errorf("Error unmarshaling response body: %w", err),
			}
		}
		return result, nil
	case http.StatusBadRequest:
		return Entity{}, &models.ApiError{
			Code:  400,
			Error: fmt.Errorf("Bad request to identifier service: %s", string(bodyResp)),
		}
	default:
		return Entity{}, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("Unexpected status code from identifier service: %d, body: %s", resp.StatusCode, string(bodyResp)),
		}
	}
}

// Utility method 'do' to create the request
func (c *IdentifierClient) do(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	// Add necessary headers here
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
