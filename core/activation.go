package core

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
	"time"
)

const (
	// MaxCodeAttempts is the number of wrong guesses allowed against one code.
	MaxCodeAttempts = 5
	// CodeLockout blocks issuing a new code after the attempt cap is hit.
	CodeLockout = 15 * time.Minute

	ResetCodeTTL   = 15 * time.Minute
	ConfirmCodeTTL = 24 * time.Hour
)

type ActivationManager interface {
	GenerateCode() string
}
type SimpleActivationManager struct{}

func NewSimpleActivationManager() *SimpleActivationManager {
	return &SimpleActivationManager{}
}

// GenerateCode returns a uniformly random 6-digit code (100000-999999) from the OS CSPRNG.
func (a *SimpleActivationManager) GenerateCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		// crypto/rand failing means the platform entropy source is broken; never fall back to a weak code.
		panic(fmt.Errorf("activation code: %w", err))
	}
	return fmt.Sprintf("%06d", n.Int64()+100000)
}

type CodeResult int

const (
	CodeOK CodeResult = iota
	CodeInvalid
	CodeLocked
)

// CheckCode evaluates a submitted code. attempts is the counter after counting this try,
// so the first try is 1 and a try over MaxCodeAttempts is rejected even if the code is right.
func CheckCode(stored string, expiry time.Time, attempts int, given string, now time.Time) CodeResult {
	if attempts > MaxCodeAttempts {
		return CodeLocked
	}
	if stored == "" || !expiry.After(now) {
		return CodeInvalid
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(given)) != 1 {
		return CodeInvalid
	}
	return CodeOK
}

// ShouldLock reports whether a failed try at this attempt count exhausts the code.
func ShouldLock(attempts int) bool {
	return attempts >= MaxCodeAttempts
}

// RegenBlocked reports whether issuing a fresh code is still blocked by a lockout.
func RegenBlocked(lockedUntil, now time.Time) bool {
	return lockedUntil.After(now)
}
