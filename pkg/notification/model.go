package notification

// Request defines the payload structure for sending notifications
type Request struct {
	To          string            `json:"to"`          // Recipient (email, phone, etc.)
	DynamicData map[string]string `json:"dynamicData"` // Dynamic content for the template
}

// Response represents the response from the notification service
type Response struct {
	Message string `json:"message"` // Success message
}
