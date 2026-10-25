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

const (
	// RegenCooldown is the minimum gap between two code issues for one account.
	RegenCooldown = 60 * time.Second
	// MaxRegensPerWindow caps code issues per RegenWindow for one account.
	MaxRegensPerWindow = 5
	RegenWindow        = time.Hour
)

// CodeBudget is the per-account bookkeeping that survives code regeneration.
type CodeBudget struct {
	LastIssuedAt     time.Time
	RegenWindowStart time.Time
	RegenCount       int
	LockedUntil      time.Time
}

// PlanIssue decides whether a new code may be issued now. It returns the updated bookkeeping
// and whether the attempt counter may be reset (only when a past lockout has elapsed). Wrong-guess
// attempts are otherwise kept across regenerations, so resending cannot refill the guess budget.
func PlanIssue(b CodeBudget, now time.Time) (next CodeBudget, resetAttempts bool, ok bool) {
	if b.LockedUntil.After(now) {
		return b, false, false
	}
	if !b.LastIssuedAt.IsZero() && now.Sub(b.LastIssuedAt) < RegenCooldown {
		return b, false, false
	}
	if b.RegenWindowStart.IsZero() || now.Sub(b.RegenWindowStart) >= RegenWindow {
		b.RegenWindowStart = now
		b.RegenCount = 0
	}
	if b.RegenCount >= MaxRegensPerWindow {
		return b, false, false
	}
	b.RegenCount++
	b.LastIssuedAt = now
	if !b.LockedUntil.IsZero() {
		b.LockedUntil = time.Time{}
		resetAttempts = true
	}
	return b, resetAttempts, true
}

// CodeOps abstracts the storage steps of one verification so the flow is testable without Mongo.
type CodeOps struct {
	// Consume counts one try and returns the code state after the increment (found=false if none).
	Consume func() (code string, expiry time.Time, attempts int, found bool, err error)
	Lock    func(until time.Time) error
	// Clear resets the attempt budget after a successful check.
	Clear func() error
}

// VerifyCode counts the try before comparing (so parallel guesses cannot skip the cap), locks
// the code on the fifth wrong try, and clears the budget only when the code is right.
// A right code never consumes budget, so verify followed by confirm of one code costs nothing.
func VerifyCode(ops CodeOps, given string, now time.Time) (CodeResult, error) {
	code, expiry, attempts, found, err := ops.Consume()
	if err != nil {
		return CodeInvalid, err
	}
	if !found {
		return CodeInvalid, nil
	}
	res := CheckCode(code, expiry, attempts, given, now)
	if res == CodeOK {
		return CodeOK, ops.Clear()
	}
	if code != "" && ShouldLock(attempts) {
		if lerr := ops.Lock(now.Add(CodeLockout)); lerr != nil {
			return res, lerr
		}
	}
	return res, nil
}
