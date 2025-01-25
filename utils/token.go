package utils

import (
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"fmt"

	"github.com/golang-jwt/jwt/v4"

	"time"
)

// CreateTokenParams struct
type CreateTokenParams struct {
	User db.User
	Type string
}

func CreateToken(params CreateTokenParams) (string, *models.ApiError) {
	var expirationTime time.Duration

	switch params.Type {
	case "access":
		expirationTime = 5 * time.Minute
	case "refresh":
		//30 * 24 * time.Hour
		expirationTime = 15 * time.Minute
	default:
		return "", &models.ApiError{
			Code:  400, // Bad Request
			Error: fmt.Errorf("invalid token type: %s", params.Type),
		}
	}

	claims := jwt.MapClaims{
		"id":    params.User.ExternalID,
		"email": params.User.UserEmail,
		"exp":   time.Now().Add(expirationTime).Unix(),
		"role":  params.User.UserRole,
	}

	secretKey := []byte("E3F9B6F9D7914B424E58DDF91AD86")

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	signedToken, err := token.SignedString(secretKey)
	if err != nil {
		return "", &models.ApiError{
			Code:  500, // Internal Server Error
			Error: fmt.Errorf("error signing token: %w", err),
		}
	}

	parsedToken, err := jwt.Parse(signedToken, func(token *jwt.Token) (interface{}, error) {
		return secretKey, nil
	})
	if err != nil {
		return "", &models.ApiError{
			Code:  500, // Internal Server Error
			Error: fmt.Errorf("error parsing signed token: %w", err),
		}
	}

	fmt.Printf("Header après signature: %+v\n", parsedToken.Header)

	return signedToken, nil
}
