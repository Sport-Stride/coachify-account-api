package utils

import (
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"fmt"
	"log"
	"net/http"

	"github.com/golang-jwt/jwt/v4"
	"go.uber.org/zap"

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
			Code:  http.StatusInternalServerError,
			Error: models.ErrSigningToken,
		}
	}

	encryptedToken, err := Encrypt(signedToken, []byte(LoadConfig().CoachifyEncryptionKey))
	if err != nil {
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrTokenEncryptionFailed,
		}
	}

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
	token, err := ParseSignedToken(decryptedToken, secretKey)
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

// LogTokenClaims decrypts a token, extracts and logs its claims
func LogTokenClaims(tokenString string, logger *log.Logger) error {
	// Decrypt the token string
	decryptedToken, err := Decrypt(tokenString, []byte(LoadConfig().CoachifyEncryptionKey))
	if err != nil {

		return err
	}

	// Parse the decrypted token
	secretKey := []byte(LoadConfig().CoachifySecretKey)
	token, err := ParseSignedToken(decryptedToken, secretKey)
	if err != nil {

		return err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {

		return fmt.Errorf("unable to parse token claims")
	}

	// Log only the user ID: email, role and other claims are PII and stay out of logs.
	userId, _ := claims["id"]
	if _, ok := claims["exp"].(float64); !ok {

		return fmt.Errorf("exp claim is not present or invalid")
	}

	Logger.Info("Token claims", zap.String("userId", fmt.Sprintf("%v", userId)))

	return nil
}

// GetTokenClaims decrypts a token and returns its claims
func GetTokenClaims(tokenString string) (map[string]interface{}, error) {
	// Decrypt the token string
	decryptedToken, err := Decrypt(tokenString, []byte(LoadConfig().CoachifyEncryptionKey))
	if err != nil {
		return nil, err
	}

	// Parse the decrypted token
	secretKey := []byte(LoadConfig().CoachifySecretKey)
	token, err := ParseSignedToken(decryptedToken, secretKey)
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unable to parse token claims")
	}

	// Convert jwt.MapClaims to map[string]interface{}
	result := make(map[string]interface{})
	for key, value := range claims {
		result[key] = value
	}

	return result, nil
}

// ParseSignedToken parses and verifies a JWT issued by CreateToken. It accepts only
// HS256 and requires an exp claim, so other HMAC variants and non-expiring tokens fail.
func ParseSignedToken(tokenString string, key []byte) (*jwt.Token, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 || t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unable to parse token claims")
	}
	if _, ok := claims["exp"].(float64); !ok {
		return nil, fmt.Errorf("exp claim is not present or invalid")
	}
	return token, nil
}

// LogDownstreamBody logs a truncated downstream response body at debug level only.
func LogDownstreamBody(target string, status int, body []byte) {
	const maxLen = 200
	s := string(body)
	if len(s) > maxLen {
		s = s[:maxLen] + "...(truncated)"
	}
	zap.L().Debug("downstream error response",
		zap.String("target", target),
		zap.Int("status", status),
		zap.String("body", s),
	)
}
