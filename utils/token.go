package utils

import (
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"fmt"
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
		expirationTime = 24 * time.Hour
	case "refresh":
		//30 * 24 * time.Hour
		expirationTime = 30 * 24 * time.Hour
	default:
		return "", &models.ApiError{
			Code:  http.StatusBadRequest, // Bad Request
			Error: models.ErrInvalidTokenType,
		}
	}

	claims := jwt.MapClaims{
		"id":    params.User.ExternalID,
		"name":  params.User.UserFirstname + " " + params.User.UserLastname,
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

	//log.Printf("IBL: encryptedToken : %+v, CoachifyEncryptionKey:  %+v", encryptedToken, LoadConfig().CoachifyEncryptionKey)
	return encryptedToken, nil
}

// IsTokenExpired checks if a given JWT token is expired.
// The tokenString must be a valid (decrypted) JWT token.
func IsTokenExpired(tokenString string) (bool, error) {
	// Decrypt the token string
	decryptedToken, err := Decrypt(tokenString, []byte(LoadConfig().CoachifyEncryptionKey))
	if err != nil {
		// If decryption fails, treat the token as expired.
		return true, err
	}

	// Parse the decrypted token
	secretKey := []byte(LoadConfig().CoachifySecretKey)
	token, err := jwt.Parse(decryptedToken, func(token *jwt.Token) (interface{}, error) {
		return secretKey, nil
	})
	if err != nil {
		// If token parsing fails, treat the token as expired.
		return true, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return true, fmt.Errorf("unable to parse token claims")
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return true, fmt.Errorf("exp claim is not present or invalid")
	}
	expirationTime := time.Unix(int64(exp), 0)
	return time.Now().After(expirationTime), nil
}
