package notification

// Request defines the payload structure for sending notifications
type Request struct {
	To          string            `json:"to"` // Recipient (email, phone, etc.)
	Subject     string            `json:"subject"`
	DynamicData map[string]string `json:"dynamicData"` // Dynamic content for the template
	test        string            `json:"subject"`
}

// Response represents the response from the notification service
type Response struct {
	ID      string
	Message string `json:"message"` // Success message
}
