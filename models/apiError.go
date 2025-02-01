package models

import (
	"fmt"
)

// ApiError represents an API error response.
type ApiError struct {
	Code  int   // HTTP status code
	Error error // Underlying error
}

// Error implements the error interface for ApiError.
func (e *ApiError) Error_() string {
	if e.Error != nil {
		return fmt.Sprintf("code=%d, err=%v", e.Code, e.Error)
	}
	return fmt.Sprintf("code=%d", e.Code)
}

// NewApiError creates a new ApiError.
func NewApiError(code int, err error) *ApiError {
	return &ApiError{
		Code:  code,
		Error: err,
	}
}
