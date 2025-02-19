package utils

import (
	"coachify-account-api/models"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"math/big"

	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// Encrypt encrypts a plaintext string using AES-GCM and returns a base64-encoded ciphertext.
func Encrypt(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded ciphertext using AES-GCM and returns the plaintext string.
func Decrypt(ciphertext string, key []byte) (string, error) {
	// Decode the base64-encoded ciphertext
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", err
	}

	// Split the nonce and ciphertext
	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]

	// Decrypt the ciphertext
	plaintextBytes, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}

	return string(plaintextBytes), nil
}

// GenerateRandomString generates a secure random string of the specified length.
// It uses crypto/rand for cryptographic security.
func GenerateRandomString(length int) (string, *models.ApiError) {
	if length <= 0 {
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInvalidIdStringGeneration,
		}
	}

	// Calculate the number of bytes needed for the desired string length
	// Base64 encoding uses 4 characters for every 3 bytes, so we adjust accordingly
	byteLength := (length * 3) / 4
	if (length*3)%4 != 0 {
		byteLength++
	}

	// Generate random bytes
	randomBytes := make([]byte, byteLength)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	// Encode bytes to a URL-safe base64 string
	randomString := base64.URLEncoding.EncodeToString(randomBytes)

	// Trim the string to the desired length
	return randomString[:length], nil
}

// Character sets used to generate a strong password.
var (
	lowercase = "abcdefghijklmnopqrstuvwxyz"
	uppercase = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits    = "0123456789"
	special   = "!@#$%^&*()-_=+[]{}|;:,.<>/?"
)

// GenerateStrongPassword creates a strong 8-character password ensuring
// at least one character from each category (lowercase, uppercase, digit, special).
func GenerateStrongPassword() (string, *models.ApiError) {
	passwordLength := 8
	password := make([]byte, passwordLength)
	categories := []string{lowercase, uppercase, digits, special}

	// Ensure at least one character from each category.
	for i := 0; i < len(categories); i++ {
		char, err := randomChar(categories[i])
		if err != nil {
			return "", &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrInternalError,
			}
		}
		password[i] = char
	}

	// Fill the rest of the password with random characters from all categories.
	allChars := lowercase + uppercase + digits + special
	for i := len(categories); i < passwordLength; i++ {
		char, err := randomChar(allChars)
		if err != nil {
			return "", &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrInternalError,
			}
		}
		password[i] = char
	}

	// Shuffle the generated password to avoid predictable placements.
	shuffle(password)
	return string(password), nil
}

// randomChar returns a random character from the given charset.
func randomChar(charset string) (byte, error) {
	max := big.NewInt(int64(len(charset)))
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return 0, err
	}
	return charset[n.Int64()], nil
}

// shuffle shuffles a slice of bytes in place using the Fisher-Yates algorithm.
func shuffle(a []byte) {
	for i := len(a) - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			continue
		}
		j := int(jBig.Int64())
		a[i], a[j] = a[j], a[i]
	}
}

// HashPassword hashes a plain-text password using bcrypt.
func HashPassword(password string) (string, *models.ApiError) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	return string(bytes), nil
}

// GenerateAndHashPassword generates a strong password and returns both the plain text
// and the hashed version.
func GenerateAndHashPassword() (plain string, hashed string, err *models.ApiError) {
	plain, err = GenerateStrongPassword()
	if err != nil {
		return "", "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	hashed, err = HashPassword(plain)
	if err != nil {
		return "", "", err
	}
	return plain, hashed, nil
}
