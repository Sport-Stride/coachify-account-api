package payments

import (
	"bytes"
	"coachify-account-api/utils"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// AcceptInvitationRequest represents the request body for accepting an invitation
// NewInvitationClient creates a new API client for the invitation service
func NewPaymentClient(cfg utils.PaymentAPIConfig) (*PaymentClient, error) {
	return &PaymentClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL:            cfg.URL,
		subscribeWithTrial: "/subscriptions",
		planHexCoach:       cfg.PlanHexCoach,
		planHexClient:      cfg.PlanHexClient,
		breaker:            utils.NewCircuitBreaker("payments-api"),
		target:             "payments-api",
	}, nil
}

// SubscribeWithTrial subscribes the authenticated user to a plan with a trial period.
func (c *PaymentClient) SubscribeWithTrial(ctx context.Context, role, UserRefreshToken string) (*PaymentResponse, error) {
	// hard-coded plan id and trial days per your request
	var planHex string
	if role == "coach" {
		planHex = c.planHexCoach
	} else {
		planHex = c.planHexClient
	}
	const trialDays = 30

	payload := map[string]interface{}{
		"plan_id":    planHex,
		"trial_days": trialDays,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s%s", c.baseURL, c.subscribeWithTrial)
	zap.L().Info("calling payments-api",
		zap.String("component", "external"),
		zap.String("target", "payments-api"),
		zap.String("role", role),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+UserRefreshToken)

	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Accept 200 OK or 201 Created as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("subscription API failed: status=%d body=%s", resp.StatusCode, string(b))
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var pr PaymentResponse
	if err := json.Unmarshal(b, &pr); err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	return &pr, nil
}
