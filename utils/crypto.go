package utils

import (
	"coachify-account-api/models"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"math/rand"
	"net/http"
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
