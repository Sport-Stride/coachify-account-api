package models

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewApiError(t *testing.T) {
	e := NewApiError(http.StatusConflict, ErrEmailAlreadyExists)
	assert.Equal(t, http.StatusConflict, e.Code)
	assert.ErrorIs(t, e.Error, ErrEmailAlreadyExists)
}

func TestApiError_String(t *testing.T) {
	assert.Equal(t, "code=400, err=boom", (&ApiError{Code: 400, Error: errors.New("boom")}).Error_())
	assert.Equal(t, "code=500", (&ApiError{Code: 500}).Error_())
}
