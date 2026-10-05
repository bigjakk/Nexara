package auth

import (
	"slices"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword_ValidPassword(t *testing.T) {
	hash, err := HashPassword("Str0ng!Pass")
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if hash == "" {
		t.Fatal("hash should not be empty")
	}
	if hash == "Str0ng!Pass" {
		t.Fatal("hash should not equal plaintext")
	}
}

func TestCheckPassword_Correct(t *testing.T) {
	hash, err := HashPassword("Str0ng!Pass")
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if err := CheckPassword(hash, "Str0ng!Pass"); err != nil {
		t.Errorf("CheckPassword() should succeed for correct password: %v", err)
	}
}

func TestCheckPassword_Wrong(t *testing.T) {
	hash, err := HashPassword("Str0ng!Pass")
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if err := CheckPassword(hash, "wrongpassword"); err == nil {
		t.Error("CheckPassword() should fail for wrong password")
	}
}

func TestValidatePasswordStrength(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{"valid", "Str0ng!Pass", nil},
		{"too short", "S1!a", ErrPasswordTooShort},
		{"too long", "Aa1!" + string(make([]byte, 70)), ErrPasswordTooLong},
		{"no uppercase", "str0ng!pass", ErrPasswordWeak},
		{"no lowercase", "STR0NG!PASS", ErrPasswordWeak},
		{"no digit", "Strong!Pass", ErrPasswordWeak},
		{"no special", "Str0ngPassw", ErrPasswordWeak},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePasswordStrength(tt.password)
			if tt.wantErr == nil && err != nil {
				t.Errorf("ValidatePasswordStrength(%q) unexpected error: %v", tt.password, err)
			}
			if tt.wantErr != nil && err == nil {
				t.Errorf("ValidatePasswordStrength(%q) expected error %v, got nil", tt.password, tt.wantErr)
			}
			if tt.wantErr != nil && err != nil && err != tt.wantErr {
				t.Errorf("ValidatePasswordStrength(%q) = %v, want %v", tt.password, err, tt.wantErr)
			}
		})
	}
}

func TestHashPassword_WeakPasswordRejected(t *testing.T) {
	_, err := HashPassword("weak")
	if err == nil {
		t.Error("HashPassword() should reject weak passwords")
	}
}

// TestRunDummyBcrypt_DoesNotPanic asserts the helper is callable in normal
// failure paths and does not panic on arbitrary inputs.
func TestRunDummyBcrypt_DoesNotPanic(t *testing.T) {
	tests := []string{"", "short", "Str0ng!Pass", string(make([]byte, 200))}
	for _, p := range tests {
		RunDummyBcrypt(p)
	}
}

// TestRunDummyBcrypt_TimingParity is the timing-oracle defence: if the dummy
// returned in ~0ms while a real bcrypt failure took ~250ms, an attacker could
// enumerate accounts. A real hash and the dummy must both carry the production
// work factor (two cost-12 hashes, seconds under -race), and RunDummyBcrypt must
// actually spend it, which only timing shows. The timing
// runs at cost 8, alternates the two calls so a busy machine slows both, and
// allows a factor of 4 either way.
func TestRunDummyBcrypt_TimingParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in -short mode")
	}

	restoreProd := SetBcryptCostForTesting(0)
	prodHash, err := HashPassword("Str0ng!Pass")
	prodDummy := currentDummyHash()
	restoreProd()
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if got, _ := bcrypt.Cost([]byte(prodHash)); got != bcryptCost {
		t.Errorf("production password hash cost = %d, want %d", got, bcryptCost)
	}
	if got, err := bcrypt.Cost(prodDummy); err != nil || got != bcryptCost {
		t.Errorf("production dummy hash cost = %d (%v), want %d", got, err, bcryptCost)
	}

	defer SetBcryptCostForTesting(8)()
	hash, err := HashPassword("Str0ng!Pass")
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	realCost, _ := bcrypt.Cost([]byte(hash))
	if got, err := bcrypt.Cost(currentDummyHash()); err != nil || got != realCost {
		t.Fatalf("dummy hash cost = %d (%v), want the real hash's %d", got, err, realCost)
	}

	// Warm caches so the first call doesn't skew the median.
	_ = CheckPassword(hash, "wrong-warmup")
	RunDummyBcrypt("wrong-warmup")

	const iters = 5
	realDurs := make([]time.Duration, iters)
	dummyDurs := make([]time.Duration, iters)
	for i := 0; i < iters; i++ {
		realDurs[i] = timed(func() { _ = CheckPassword(hash, "wrong-password") })
		dummyDurs[i] = timed(func() { RunDummyBcrypt("wrong-password") })
	}
	realDur, dummyDur := median(realDurs), median(dummyDurs)
	if dummyDur*4 < realDur || dummyDur > realDur*4 {
		t.Errorf("dummy bcrypt timing %v outside [real/4, real*4] = [%v, %v]",
			dummyDur, realDur/4, realDur*4)
	}
}

func timed(f func()) time.Duration {
	start := time.Now()
	f()
	return time.Since(start)
}

func median(durs []time.Duration) time.Duration {
	sorted := slices.Clone(durs)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}
