package api

// api/response.go
type CheckClientResponse struct {
	CoachName     string `json:"coachName"`
	UserExists    bool   `json:"userExists"`
	AlreadyLinked bool   `json:"alreadyLinked"`
	InvitationURL string `json:"invitationUrl,omitempty"`
}

type InvitationResponse struct {
	InvitationURL string `json:"invitationUrl"`
	CoachName     string `json:"coachName"`
}
