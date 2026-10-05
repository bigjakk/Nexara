package auth

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// recoveryCodeCost is the work factor for recovery-code hashes.
const recoveryCodeCost = bcrypt.DefaultCost

var (
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	ErrPasswordTooLong  = errors.New("password must be at most 72 characters")
	ErrPasswordWeak     = errors.New("password must contain uppercase, lowercase, digit, and special character")
)

// testBcryptCost, while non-zero, replaces both work factors above. Only
// SetBcryptCostForTesting sets it. It is atomic because a goroutine an earlier
// test left running may still be hashing when a later test changes it.
var testBcryptCost atomic.Int64

// SetBcryptCostForTesting makes every hash this package computes use cost
// instead of the production work factors, and returns a func that puts the
// previous setting back. Cost 0 means the production work factors. Tests only —
// it panics outside a test binary: under the race detector a single cost-12 hash
// takes seconds.
func SetBcryptCostForTesting(cost int) (restore func()) {
	if !testing.Testing() {
		panic("auth: SetBcryptCostForTesting called outside a test binary")
	}
	if cost != 0 && (cost < bcrypt.MinCost || cost > bcrypt.MaxCost) {
		panic(fmt.Sprintf("auth: bcrypt cost %d is outside [%d, %d]", cost, bcrypt.MinCost, bcrypt.MaxCost))
	}
	prev := testBcryptCost.Swap(int64(cost))
	return func() { testBcryptCost.Store(prev) }
}

func costOr(production int) int {
	if c := testBcryptCost.Load(); c != 0 {
		return int(c)
	}
	return production
}

// dummyHashes holds, per work factor, a precomputed bcrypt hash used to pad
// authentication failure paths so that login response time does not reveal
// whether a given email maps to a real local account. Production only ever
// uses bcryptCost, computed at package init.
var dummyHashes sync.Map // int → []byte

func init() {
	// Refuse to start rather than serve logins without timing parity. A test
	// binary computes it on first use instead, at the cost the test set.
	if !testing.Testing() {
		dummyHash(bcryptCost)
	}
}

// dummyHash returns the dummy hash at cost. The seed string is arbitrary — it
// is never compared against real plaintext, only used to produce a hash with
// the same cost factor as a real one. RunDummyBcrypt verifies a different
// password, so the comparison is always guaranteed to fail.
func dummyHash(cost int) []byte {
	if h, ok := dummyHashes.Load(cost); ok {
		return h.([]byte)
	}
	h, err := bcrypt.GenerateFromPassword([]byte("nexara-dummy-bcrypt-seed"), cost)
	if err != nil {
		// bcrypt.GenerateFromPassword can only error if cost is out of range,
		// which bcryptCost and SetBcryptCostForTesting's check rule out.
		panic("auth: failed to compute dummy bcrypt hash: " + err.Error())
	}
	actual, _ := dummyHashes.LoadOrStore(cost, h)
	return actual.([]byte)
}

// HashPassword hashes a plaintext password using bcrypt.
func HashPassword(password string) (string, error) {
	if err := ValidatePasswordStrength(password); err != nil {
		return "", fmt.Errorf("password validation: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), costOr(bcryptCost))
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword compares a plaintext password against a bcrypt hash.
// Uses constant-time comparison internally via bcrypt.
func CheckPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// RunDummyBcrypt runs a bcrypt comparison against a precomputed hash so the
// caller burns CPU time equivalent to a real CheckPassword call. Used on
// authentication failure paths (nonexistent user, OIDC-source user trying
// password login, inactive account) so login response time does not leak
// whether the email maps to a real local account.
//
// The result is intentionally discarded — the comparison is guaranteed to
// fail for any caller-provided password.
func RunDummyBcrypt(password string) {
	_ = bcrypt.CompareHashAndPassword(currentDummyHash(), []byte(password))
}

// currentDummyHash is the dummy hash at the work factor HashPassword uses now.
func currentDummyHash() []byte {
	return dummyHash(costOr(bcryptCost))
}

// ValidatePasswordStrength checks that a password meets minimum complexity requirements.
func ValidatePasswordStrength(password string) error {
	if len(password) < 8 {
		return ErrPasswordTooShort
	}
	if len(password) > 72 {
		return ErrPasswordTooLong
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit || !hasSpecial {
		return ErrPasswordWeak
	}
	return nil
}
