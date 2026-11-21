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
		subscribeWithTrial: "/subscriptions/default",
		breaker:            utils.NewCircuitBreaker("payments-api"),
		target:             "payments-api",
		apiKey:             cfg.APIKey,
	}, nil
}

// SubscribeWithTrial subscribes the authenticated user to their default plan; payments-api
// resolves the plan from the caller's role (admin/coach-configured default).
func (c *PaymentClient) SubscribeWithTrial(ctx context.Context, role, UserRefreshToken string) (*PaymentResponse, error) {
	bodyBytes := []byte("{}")

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
		utils.LogDownstreamBody("payments-api", resp.StatusCode, b)
		return nil, fmt.Errorf("subscription API failed: status=%d", resp.StatusCode)
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

// GetBulkBillingInfo fetches billing info for a list of user IDs from payments-api.
// adminToken is the encrypted JWT of the admin caller, forwarded as-is.
func (c *PaymentClient) GetBulkBillingInfo(ctx context.Context, adminToken string, userIDs []string) (*BulkBillingResponse, error) {
	if len(userIDs) == 0 {
		return &BulkBillingResponse{}, nil
	}

	url := fmt.Sprintf("%s/admin/subscriptions/bulk-billing", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)

	q := req.URL.Query()
	for _, id := range userIDs {
		q.Add("user_ids", id)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		utils.LogDownstreamBody("payments-api", resp.StatusCode, b)
		return nil, fmt.Errorf("payments-api bulk billing failed: status=%d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result BulkBillingResponse
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, fmt.Errorf("error decoding bulk billing response: %w", err)
	}

	return &result, nil
}

// GetSubscriptionSummaries returns the active subscription summary of each user (service-to-service,
// x-api-key). A 404, timeout or any failure returns an error; callers treat that as "no plan".
func (c *PaymentClient) GetSubscriptionSummaries(ctx context.Context, userIDs []string) ([]SubscriptionSummaryEntry, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(subscriptionSummaryRequest{UserIDs: userIDs})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/internal/subscriptions/summary", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}

	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		utils.LogDownstreamBody("payments-api", resp.StatusCode, b)
		return nil, fmt.Errorf("payments-api subscription summary failed: status=%d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out subscriptionSummaryResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("error decoding subscription summary response: %w", err)
	}
	return out.Summaries, nil
}

// MaxGymSummaryIDs is the payments-api limit of gym ids per summary request.
const MaxGymSummaryIDs = 200

// GymSummaries returns the gym subscription summary of each gym (service-to-service, x-api-key). Every requested gym
// gets one entry; a gym without a subscription has status "none". Any failure returns an error.
func (c *PaymentClient) GymSummaries(ctx context.Context, gymIDs []string) ([]GymSummary, error) {
	if len(gymIDs) == 0 {
		return nil, nil
	}
	if len(gymIDs) > MaxGymSummaryIDs {
		return nil, fmt.Errorf("too many gym ids: %d", len(gymIDs))
	}
	body, err := json.Marshal(gymSummaryRequest{GymIDs: gymIDs})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/internal/gym-subscriptions/summary", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
	}
	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		utils.LogDownstreamBody("payments-api", resp.StatusCode, b)
		return nil, fmt.Errorf("payments-api gym summary failed: status=%d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out gymSummaryResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("error decoding gym summary response: %w", err)
	}
	return out.Summaries, nil
}

// GymMembershipCancelResult is the answer of the gym membership cancel: Status is "cancel_at_period_end" (the
// subscription ends at the period end) or "none" (the user had no gym membership subscription).
type GymMembershipCancelResult struct {
	Status         string `json:"status"`
	SubscriptionID string `json:"subscription_id,omitempty"`
}

// CancelGymMembership asks payments-api to end the user's gym membership plan subscription at the period end
// (service-to-service, strict x-api-key). Idempotent on the payments side. Any failure returns an error.
func (c *PaymentClient) CancelGymMembership(ctx context.Context, gymID, userID string) (*GymMembershipCancelResult, error) {
	if gymID == "" || userID == "" {
		return nil, fmt.Errorf("gym id and user id are required")
	}
	if c.apiKey == "" {
		return nil, fmt.Errorf("payments-api key is not configured")
	}
	body, err := json.Marshal(map[string]string{"gym_id": gymID, "user_id": userID})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/internal/gym-memberships/cancel", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		utils.LogDownstreamBody("payments-api", resp.StatusCode, b)
		return nil, fmt.Errorf("payments-api gym membership cancel failed: status=%d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out GymMembershipCancelResult
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("error decoding gym membership cancel response: %w", err)
	}
	return &out, nil
}

// GymDefaultSubscribeResult is the answer of the gym default plan subscribe: Status is one of "subscribed",
// "already_subscribed", "no_default", "gym_inactive" or "payment_required" (the default plan is paid, so the client
// chooses and pays it themselves).
type GymDefaultSubscribeResult struct {
	Status         string `json:"status"`
	SubscriptionID string `json:"subscription_id,omitempty"`
}

// SubscribeGymDefault asks payments-api to subscribe a client who joined a gym to the gym's default plan when it is
// free (service-to-service, strict x-api-key). joinRef makes a replay of the same join idempotent. Any failure
// returns an error.
func (c *PaymentClient) SubscribeGymDefault(ctx context.Context, gymID, userID, joinRef string) (*GymDefaultSubscribeResult, error) {
	if gymID == "" || userID == "" {
		return nil, fmt.Errorf("gym id and user id are required")
	}
	if c.apiKey == "" {
		return nil, fmt.Errorf("payments-api key is not configured")
	}
	body, err := json.Marshal(map[string]string{"gym_id": gymID, "user_id": userID, "join_ref": joinRef})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/internal/gym-memberships/default-subscribe", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		utils.LogDownstreamBody("payments-api", resp.StatusCode, b)
		return nil, fmt.Errorf("payments-api gym default subscribe failed: status=%d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out GymDefaultSubscribeResult
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("error decoding gym default subscribe response: %w", err)
	}
	return &out, nil
}
