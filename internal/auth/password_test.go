package auth

import (
	"testing"
	"time"
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

// TestRunDummyBcrypt_TimingParity asserts the dummy compare consumes wall
// time within the same order of magnitude as a real CheckPassword failure.
// This is the timing-oracle defence — if the dummy returned ~0ms while a
// real bcrypt failure took ~250ms, an attacker could enumerate accounts.
//
// We use a generous bound (real time / 4 ≤ dummy time ≤ real time * 4) to
// avoid CI flakiness from CPU contention; the goal is to catch a regression
// where someone accidentally swaps RunDummyBcrypt for a no-op or
// non-bcrypt operation.
func TestRunDummyBcrypt_TimingParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in -short mode")
	}

	hash, err := HashPassword("Str0ng!Pass")
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}

	// Warm caches so the first call doesn't skew the median.
	_ = CheckPassword(hash, "wrong-warmup")
	RunDummyBcrypt("wrong-warmup")

	const iters = 3
	realDur := medianDuration(t, iters, func() {
		_ = CheckPassword(hash, "wrong-password")
	})
	dummyDur := medianDuration(t, iters, func() {
		RunDummyBcrypt("wrong-password")
	})

	if dummyDur*4 < realDur || dummyDur > realDur*4 {
		t.Errorf("dummy bcrypt timing %v outside [real/4, real*4] = [%v, %v]",
			dummyDur, realDur/4, realDur*4)
	}
}

func medianDuration(t *testing.T, iters int, f func()) time.Duration {
	t.Helper()
	durs := make([]time.Duration, iters)
	for i := 0; i < iters; i++ {
		start := time.Now()
		f()
		durs[i] = time.Since(start)
	}
	// Tiny iters; sort by inserting in place.
	for i := 1; i < len(durs); i++ {
		for j := i; j > 0 && durs[j] < durs[j-1]; j-- {
			durs[j], durs[j-1] = durs[j-1], durs[j]
		}
	}
	return durs[len(durs)/2]
}
