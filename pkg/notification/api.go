package notification

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

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
	url := "https://sandbox.api.mailtrap.io/api/send/2370532"
	method := "POST"

	messageText := req.DynamicData["message"]
	fmt.Println("Dynamic Message Data:", messageText) // 🔍 Debug log

	payload := strings.NewReader(fmt.Sprintf(`{
		"from": {"email": "hello@example.com", "name": "Mailtrap Test"},
		"to": [{"email": "khaledlajili11@gmail.com"}],
		"subject": "You are awesome!",
		"text": "Congrats for sending test email with Mailtrap! %s",
		"category": "Integration Test"
	}`, messageText))

	client := &http.Client{}
	req1, err := http.NewRequest(method, url, payload)

	req1.Header.Add("Authorization", "Bearer 7475bbc0b2f03ef8a90a71161170aff3")
	req1.Header.Add("Content-Type", "application/json")

	res, err := client.Do(req1)
	if err != nil {
		fmt.Println(err)

	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)

	}

	fmt.Println(string(body))

	// Return the response
	return &Response{
		Message: "Email sent successfully",
		ID:      "id",
	}, nil
}
