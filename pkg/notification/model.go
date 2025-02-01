package notification

// Request defines the payload structure for sending notifications
type Request struct {
	Method      string
	URL         string
	Body        []byte
	Header      map[string]string
	To          string
	DynamicData map[string]string
}

// Response represents the response from the notification service
type Response struct {
	Header  string
	ID      string
	Message string `json:"message"` // Success message
}
