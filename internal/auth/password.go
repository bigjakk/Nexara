package auth

import (
	"errors"
	"fmt"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

var (
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	ErrPasswordTooLong  = errors.New("password must be at most 72 characters")
	ErrPasswordWeak     = errors.New("password must contain uppercase, lowercase, digit, and special character")
)

// dummyBcryptHash is a precomputed bcrypt hash used to pad authentication
// failure paths so that login response time does not reveal whether a given
// email maps to a real local account. Computed once at package init.
var dummyBcryptHash []byte

func init() {
	// The seed string is arbitrary — it is never compared against real
	// plaintext, only used to produce a hash with the same cost factor as
	// production hashes. RunDummyBcrypt verifies a different password, so
	// the comparison is always guaranteed to fail.
	h, err := bcrypt.GenerateFromPassword([]byte("nexara-dummy-bcrypt-seed"), bcryptCost)
	if err != nil {
		// bcrypt.GenerateFromPassword can only error if cost is out of
		// range, which is impossible at our compile-time constant.
		// Refuse to start rather than serve logins without timing parity.
		panic("auth: failed to compute dummy bcrypt hash: " + err.Error())
	}
	dummyBcryptHash = h
}

// HashPassword hashes a plaintext password using bcrypt.
func HashPassword(password string) (string, error) {
	if err := ValidatePasswordStrength(password); err != nil {
		return "", fmt.Errorf("password validation: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
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
	_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
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
