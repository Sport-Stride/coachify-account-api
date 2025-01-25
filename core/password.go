package core

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"mbv-common-template-api/models"
	"regexp"

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
		return "", &models.ApiError{
			Code:  500, // Internal Server Error
			Error: fmt.Errorf("failed to hash password: %w", err),
		}
	}

	return string(hashedPassword), nil
}

func (p *PasswordCheckerImpl) VerifyPassword(providedPassword string, userPassword string) (bool, *models.ApiError) {
	err := bcrypt.CompareHashAndPassword([]byte(userPassword), []byte(providedPassword))
	if err != nil {
		if err == bcrypt.ErrMismatchedHashAndPassword {
			return false, &models.ApiError{
				Code:  401, // Unauthorized (mot de passe incorrect)
				Error: fmt.Errorf("password mismatch: %w", err),
			}
		}
		return false, &models.ApiError{
			Code:  500, // Internal Server Error
			Error: fmt.Errorf("failed to verify password: %w", err),
		}
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
