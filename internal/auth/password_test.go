package auth

import (
	"testing"
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
