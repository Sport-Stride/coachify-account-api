package core

import (
	"coachify-account-api/models"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"golang.org/x/crypto/bcrypt"
)

type PasswordChecker interface {
	HashPassword(password string) (string, *models.ApiError)
	VerifyPassword(providedPassword, password string) (bool, *models.ApiError)
}

type PasswordCheckerImpl struct{}

func NewPasswordChecker() *PasswordCheckerImpl {
	return &PasswordCheckerImpl{}
}

func (p *PasswordCheckerImpl) HashPassword(password string) (string, *models.ApiError) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", models.NewApiError(http.StatusInternalServerError, models.ErrFailedToHashPassword)
	}

	return string(hashedPassword), nil
}

func (p *PasswordCheckerImpl) VerifyPassword(providedPassword string, userPassword string) (bool, *models.ApiError) {
	err := bcrypt.CompareHashAndPassword([]byte(userPassword), []byte(providedPassword))
	if err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, models.NewApiError(http.StatusUnauthorized, fmt.Errorf("%w: %v", models.ErrPasswordMismatch, err))
		}
		return false, models.NewApiError(http.StatusInternalServerError, fmt.Errorf("%w: %v", models.ErrFailedToVerifyPassword, err))
	}
	return true, nil
}

// ValidatePassword checks if the password contains an uppercase letter, a lowercase letter, a symbol, and at least 8 characters
func ValidatePassword(password string) bool {
	var (
		hasMinLen  = len(password) >= 8
		hasUpper   = regexp.MustCompile(`[A-Z]`).MatchString(password)
		hasLower   = regexp.MustCompile(`[a-z]`).MatchString(password)
		hasSpecial = regexp.MustCompile(`[^a-zA-Z0-9]`).MatchString(password)
	)

	return hasMinLen && hasUpper && hasLower && hasSpecial
}
