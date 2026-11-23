package api

// CoachPublicProfile is a coach as any signed-in user sees them (GET /coaches/:coach_id/profile).
// It never carries contact details (email, phone) or private data.
type CoachPublicProfile struct {
	ExternalID     string   `json:"external_id"`
	Firstname      string   `json:"firstname"`
	Lastname       string   `json:"lastname"`
	Role           string   `json:"role"`
	ProfilePicture string   `json:"profile_picture,omitempty"`
	Bio            string   `json:"bio,omitempty"`
	City           string   `json:"city,omitempty"`
	Specialties    []string `json:"specialties"`
	// Clients is the number of clients the coach follows; left out when the count is unavailable.
	Clients *int `json:"clients,omitempty"`
}
