package notification

import (
	"bytes"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/utils"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// NotificationClient handles communication with the notification API
type NotificationClient struct {
	httpClient           *http.Client
	baseURL              string
	notificationEndpoint string
	breaker              *utils.CircuitBreaker
	target               string
}

// NewNotificationClient initializes a new client for the notification service
func NewNotificationClient(cfg utils.NotificationAPIConfig) (*NotificationClient, error) {
	return &NotificationClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL:              cfg.URL,
		notificationEndpoint: "/emails",
		breaker:              utils.NewCircuitBreaker("notification-api"),
		target:               "notification-api",
	}, nil
}

// SendInvitationMail sends an invitation email via the notification API
func (c *NotificationClient) SendMail(ctx context.Context, dbUser *db.User) error {
	if dbUser.UserEmail == "" {
		return fmt.Errorf("no email recipient provided")
	}

	// Construct the endpoint URL correctly
	url := c.baseURL + c.notificationEndpoint
	zap.L().Info("calling notification-api",
		zap.String("component", "external"),
		zap.String("target", "notification-api"),
		zap.String("email", dbUser.UserEmail),
	)
	// Build the request body
	requestBody := map[string]interface{}{
		"to":           dbUser.UserEmail,
		"templateName": utils.LoadConfig().ConfirmationCodeTemplatePending,
		"dynamicData": map[string]string{
			"message":  "Thank you for registering! Here is your confirmation code: " + dbUser.UserConfirmCode.Code,
			"subject":  "Confirmation of Your Registration",
			"username": dbUser.UserFirstname + " " + dbUser.UserLastname,
		},
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := c.do(ctx, http.MethodPost, url, jsonData)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 3*time.Second)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	bodyResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("invalid request: %s", string(bodyResp))
	case http.StatusInternalServerError:
		return fmt.Errorf("server error: %s", string(bodyResp))
	default:
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(bodyResp))
	}

}

// SendConfirmationEmail sends a confirmation email
func (c *NotificationClient) SendConfirmationEmail(ctx context.Context, dbUser *db.User) error {
	if dbUser.UserEmail == "" {
		return fmt.Errorf("no email recipient provided")
	}
	if dbUser.UserConfirmCode == nil {
		zap.L().Warn("user confirmation code is nil",
			zap.String("component", "external"),
			zap.String("target", "notification-api"),
			zap.String("email", dbUser.UserEmail),
		)
		return fmt.Errorf("user confirmation code is missing")
	}
	template := utils.LoadConfig().ConfirmationCodeTemplatePending
	dynamicData := map[string]string{
		"message":  "Thank you for registering! Here is your confirmation code: " + dbUser.UserConfirmCode.Code,
		"subject":  "Confirmation of Your Registration",
		"username": dbUser.UserFirstname + " " + dbUser.UserLastname,
	}
	return c.sendEmail(ctx, dbUser.UserEmail, template, dynamicData)
}

// SendResendConfirmationEmail sends a resend confirmation email
func (c *NotificationClient) SendResendConfirmationEmail(ctx context.Context, dbUser *db.User) error {
	if dbUser.UserEmail == "" {
		return fmt.Errorf("no email recipient provided")
	}
	template := utils.LoadConfig().ResendConfirmationTemplate
	dynamicData := map[string]string{
		"message":  "Here is your new confirmation code: " + dbUser.UserConfirmCode.Code,
		"subject":  "Resend Confirmation Code",
		"username": dbUser.UserFirstname + " " + dbUser.UserLastname,
	}
	return c.sendEmail(ctx, dbUser.UserEmail, template, dynamicData)
}

// SendResetPasswordEmail sends a reset password email
func (c *NotificationClient) SendResetPasswordEmail(ctx context.Context, dbUser *api.ResetPasswordResponse) error {
	if dbUser.UserEmail == "" {
		return fmt.Errorf("no email recipient provided")
	}
	template := utils.LoadConfig().ResetPasswordTemplate
	dynamicData := map[string]string{
		"message":  "You requested a password reset. Here is your reset code: " + dbUser.UserResetPasswordCode.Code,
		"subject":  "Reset Your Password",
		"username": dbUser.UserFirstname + " " + dbUser.UserLastname,
	}
	return c.sendEmail(ctx, dbUser.UserEmail, template, dynamicData)
}

// SendConfirmResetPasswordEmail sends a confirmation of password reset
func (c *NotificationClient) SendConfirmResetPasswordEmail(ctx context.Context, dbUser *api.ResetPasswordResponse) error {
	if dbUser.UserEmail == "" {
		return fmt.Errorf("no email recipient provided")
	}
	template := utils.LoadConfig().ConfirmResetPasswordTemplate
	dynamicData := map[string]string{
		"message":  "Your password has been successfully reset.",
		"subject":  "Password Reset Confirmation",
		"username": dbUser.UserFirstname + " " + dbUser.UserLastname,
	}
	return c.sendEmail(ctx, dbUser.UserEmail, template, dynamicData)
}

// Send a welcome email after confirmation
func (c *NotificationClient) SendWelcomeEmail(ctx context.Context, dbUser *db.User) error {
	if dbUser.UserEmail == "" {
		return fmt.Errorf("no email recipient provided")
	}
	template := utils.LoadConfig().WelcomeTemplate
	dynamicData := map[string]string{
		"message":         "Welcome to our platform !",
		"subject":         "Welcome to Our Community!",
		"username":        dbUser.UserFirstname + " " + dbUser.UserLastname,
		"validation_link": "https://app.coachify.tn/auth/signin",
	}
	return c.sendEmail(ctx, dbUser.UserEmail, template, dynamicData)
}

// sendEmail is a helper to send an email with a template and dynamic data
func (c *NotificationClient) sendEmail(ctx context.Context, to, template string, dynamicData map[string]string) error {
	url := c.baseURL + c.notificationEndpoint
	requestBody := map[string]interface{}{
		"to":           to,
		"templateName": template,
		"dynamicData":  dynamicData,
	}
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}
	req, err := c.do(ctx, http.MethodPost, url, jsonData)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := utils.InstrumentedDo(ctx, c.httpClient, req, c.target, c.breaker, 3*time.Second)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()
	bodyResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted:
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("invalid request: %s", string(bodyResp))
	case http.StatusInternalServerError:
		return fmt.Errorf("server error: %s", string(bodyResp))
	default:
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(bodyResp))
	}
}

// do creates an HTTP request with context and headers
func (c *NotificationClient) do(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
