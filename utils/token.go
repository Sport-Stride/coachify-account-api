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

// LogTokenClaims decrypts a token, extracts and logs its claims
func LogTokenClaims(tokenString string, logger *log.Logger) error {
	// Decrypt the token string
	decryptedToken, err := Decrypt(tokenString, []byte(LoadConfig().CoachifyEncryptionKey))
	if err != nil {

		return err
	}

	// Parse the decrypted token
	secretKey := []byte(LoadConfig().CoachifySecretKey)
	token, err := jwt.Parse(decryptedToken, func(token *jwt.Token) (interface{}, error) {
		return secretKey, nil
	})
	if err != nil {

		return err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {

		return fmt.Errorf("unable to parse token claims")
	}

	// Extract common claims
	userId, _ := claims["id"]
	email, _ := claims["email"]
	role, _ := claims["role"]
	tokenType, _ := claims["token_type"]
	// Get expiration time
	exp, ok := claims["exp"].(float64)
	if !ok {

		return fmt.Errorf("exp claim is not present or invalid")
	}
	expirationTime := time.Unix(int64(exp), 0)

	// Get issued at time if present
	var issuedAt time.Time
	if iat, ok := claims["iat"].(float64); ok {
		issuedAt = time.Unix(int64(iat), 0)
	}

	// Convert claims values to appropriate zap field types
	userIdStr := fmt.Sprintf("%v", userId)
	emailStr := fmt.Sprintf("%v", email)
	roleStr := fmt.Sprintf("%v", role)
	tokenTypeStr := fmt.Sprintf("%v", tokenType)
	isExpired := time.Now().After(expirationTime)

	// Log the important claims using zap.Field objects
	Logger.Info("Token claims",
		zap.String("userId", userIdStr),
		zap.String("email", emailStr),
		zap.String("role", roleStr),
		zap.String("tokenType", tokenTypeStr),
		zap.Time("expiresAt", expirationTime),
		zap.Time("issuedAt", issuedAt),
		zap.Bool("isExpired", isExpired))

	// Create a map of all claims for more detailed logging
	allClaimsFields := []zap.Field{}
	for key, value := range claims {
		// Skip already logged fields
		if key != "user_id" && key != "email" && key != "role" && key != "token_type" && key != "exp" && key != "iat" {
			allClaimsFields = append(allClaimsFields, zap.Any(key, value))
		}
	}

	if len(allClaimsFields) > 0 {
		Logger.Info("Additional token claims", allClaimsFields...)
	}

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
	token, err := jwt.Parse(decryptedToken, func(token *jwt.Token) (interface{}, error) {
		return secretKey, nil
	})
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
