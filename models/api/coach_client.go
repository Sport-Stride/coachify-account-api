package api

import (
	"time"
)

type SearchClient struct {
	Page    int           `form:"page" default:"1"`
	Size    int           `form:"size" default:"10"`
	Query   string        `form:"query"`
	Filters ClientFilters `form:"filters"`
}

type ClientFilters struct {
	Status      string    `form:"status"`
	JoinedAfter time.Time `form:"joined_after"`
	// Add other filter fields
}

type ClientResponse struct {
	ID          string    `json:"id"`
	Firstname   string    `json:"firstname"`
	Lastname    string    `json:"lastname"`
	Email       string    `json:"email"`
	Phone       string    `json:"phone"`
	JoinedAt    time.Time `json:"joined_at"`
	LastSession time.Time `json:"last_session"`
	Progress    float64   `json:"progress"`
}

type PaginatedClientResponse struct {
	Clients []*ClientResponse `json:"clients"`
	Total   int               `json:"total"`
}

type InviteClientRequest struct {
	Email     string `json:"email" binding:"required,email"`
	FirstName string `json:"firstname"`
	LastName  string `json:"lastname"`
}

type InviteClientResponse struct {
	Message    string `json:"message"`
	Successful int32  `json:"successful"`
	Failed     int32  `json:"failed"`
}

type MultipleInvitationsRequest struct {
	Emails []string `json:"emails" binding:"required,dive,email"`
}
