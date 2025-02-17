package utils

import (
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"fmt"
	"log"
	"net/http"

	"github.com/golang-jwt/jwt/v4"

	"time"
)

// CreateTokenParams struct
type CreateTokenParams struct {
	User api.RefreshToken
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
			Code:  http.StatusBadRequest, // Bad Request
			Error: models.ErrInvalidTokenType,
		}
	}

	claims := jwt.MapClaims{
		"id":    params.User.ExternalID,
		"email": params.User.UserEmail,
		"exp":   time.Now().Add(expirationTime).Unix(),
		"role":  params.User.UserRole,
	}

	secretKey := []byte(LoadConfig().CoachifySecretKey)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	signedToken, err := token.SignedString(secretKey)
	if err != nil {
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError, // Internal Server Error
			Error: models.ErrSigningToken,
		}
	}

	parsedToken, err := jwt.Parse(signedToken, func(token *jwt.Token) (interface{}, error) {
		return secretKey, nil
	})
	if err != nil {
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError, // Internal Server Error
			Error: models.ErrParsingSignedToken,
		}
	}

	fmt.Printf("Header après signature: %+v\n", parsedToken.Header)

	encryptedToken, err := Encrypt(signedToken, []byte(LoadConfig().CoachifyEncryptionKey))
	if err != nil {
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrTokenEncryptionFailed,
		}
	}

	log.Printf("IBL: encryptedToken : %+v, CoachifyEncryptionKey:  %+v", encryptedToken, LoadConfig().CoachifyEncryptionKey)
	return encryptedToken, nil
}
