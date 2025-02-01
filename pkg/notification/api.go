package notification

import (
	"context"
	"fmt"
	"log"

	"github.com/mailgun/mailgun-go/v4"
)

// NotificationClient represents the client for sending notifications via Mailgun.
type NotificationClient struct {
	mgClient mailgun.Mailgun
}

// NewNotificationClient creates a new NotificationClient with Mailgun.
func NewNotificationClient(domain, apiKey string) *NotificationClient {
	mgClient := mailgun.NewMailgun(domain, apiKey)
	return &NotificationClient{
		mgClient: mgClient,
	}
}

// Send sends an email via Mailgun.
func (c *NotificationClient) Send(ctx context.Context, req Request, template ...string) (*Response, error) {
	// Create the email message
	var tmpl string
	if len(template) > 0 {
		tmpl = template[0] // Use the provided template
	} else {
		tmpl = "" // Default to an empty string
	}
	message := c.mgClient.NewMessage(
		"Mailgun Sandbox <postmaster@sandbox7caec8d24a564f0e9d63bf11c29370cd.mailgun.org>",
		req.Subject,                // Email subject
		req.DynamicData["message"], // Email body
		req.To,                     // Recipient email
	)
	message.SetTemplate(tmpl)
	// Add dynamic data as variables (optional)
	for key, value := range req.DynamicData {
		message.AddVariable(key, value)
	}
	// Send the message

	_, id, err := c.mgClient.Send(ctx, message)
	if err != nil {
		log.Printf("Mailgun Error: %v", err)
		return nil, fmt.Errorf("failed to send email: %w", err)
	}

	// Return the response
	return &Response{
		Message: "Email sent successfully",
		ID:      id,
	}, nil
}
