package services

import (
	"crypto/rand"
	"encoding/hex"
)

// Generate a random code for invitation
func generateInvitationCode() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
