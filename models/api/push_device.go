package api

import "coachify-account-api/models/db"

// RegisterDeviceRequest is the body of POST /user/devices.
type RegisterDeviceRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
	Locale   string `json:"locale,omitempty"`
}

// DevicesResponse is the body of GET /internal/users/:id/devices.
type DevicesResponse struct {
	Devices []db.PushDevice `json:"devices"`
}
