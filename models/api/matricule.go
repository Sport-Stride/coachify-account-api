package api

import "time"

// MatriculeFiscaleApplication represents a matricule fiscale submission for admin review.
type MatriculeFiscaleApplication struct {
	UserID           string     `json:"user_id"`
	FullName         string     `json:"full_name"`
	Role             string     `json:"role"`
	Email            string     `json:"email"`
	MatriculeFiscale string     `json:"matricule_fiscale"`
	Status           string     `json:"status"`
	SubmittedAt      *time.Time `json:"submitted_at,omitempty"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
	ReviewedBy       string     `json:"reviewed_by,omitempty"`
}
