package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/proxdash/proxdash/internal/crypto"
)

// testEncryptionKey is a valid 64 hex-character (32 byte) AES key for testing.
const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestGenerateSecret(t *testing.T) {
	svc := NewTOTPService(testEncryptionKey)

	encrypted, otpauthURL, plainSecret, err := svc.GenerateSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateSecret() error = %v", err)
	}

	if encrypted == "" {
		t.Error("encrypted secret is empty")
	}
	if !strings.HasPrefix(otpauthURL, "otpauth://totp/") {
		t.Errorf("otpauth URL has wrong prefix: %s", otpauthURL)
	}
	if plainSecret == "" {
		t.Error("plain secret is empty")
	}

	// Verify the encrypted secret can be decrypted back to the plain secret.
	decrypted, err := crypto.Decrypt(encrypted, testEncryptionKey)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if decrypted != plainSecret {
		t.Errorf("decrypted secret %q != plain secret %q", decrypted, plainSecret)
	}
}

func TestValidateCode(t *testing.T) {
	svc := NewTOTPService(testEncryptionKey)

	encrypted, _, plainSecret, err := svc.GenerateSecret("test@example.com")
	if err != nil {
		t.Fatalf("GenerateSecret() error = %v", err)
	}

	// Generate a valid code from the plain secret.
	validCode, err := totp.GenerateCode(plainSecret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}

	tests := []struct {
		name string
		code string
		want bool
	}{
		{"valid code", validCode, true},
		{"invalid code", "000000", false},
		{"empty code", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.ValidateCode(encrypted, tt.code)
			if err != nil {
				t.Fatalf("ValidateCode() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ValidateCode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateRecoveryCodes(t *testing.T) {
	svc := NewTOTPService(testEncryptionKey)

	plainCodes, hashedCodes, err := svc.GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes() error = %v", err)
	}

	if len(plainCodes) != 10 {
		t.Errorf("expected 10 plain codes, got %d", len(plainCodes))
	}
	if len(hashedCodes) != 10 {
		t.Errorf("expected 10 hashed codes, got %d", len(hashedCodes))
	}

	// Verify format: XXXX-XXXX
	for _, code := range plainCodes {
		if len(code) != 9 || code[4] != '-' {
			t.Errorf("invalid code format: %s", code)
		}
	}

	// Verify uniqueness
	seen := make(map[string]bool)
	for _, code := range plainCodes {
		if seen[code] {
			t.Errorf("duplicate code: %s", code)
		}
		seen[code] = true
	}
}

func TestValidateRecoveryCode(t *testing.T) {
	svc := NewTOTPService(testEncryptionKey)

	plainCodes, hashedCodes, err := svc.GenerateRecoveryCodes(3)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes() error = %v", err)
	}

	tests := []struct {
		name  string
		hash  string
		input string
		want  bool
	}{
		{"valid code with dash", hashedCodes[0], plainCodes[0], true},
		{"valid code without dash", hashedCodes[1], strings.ReplaceAll(plainCodes[1], "-", ""), true},
		{"valid code lowercase", hashedCodes[2], strings.ToLower(plainCodes[2]), true},
		{"wrong code", hashedCodes[0], "ZZZZ-ZZZZ", false},
		{"empty code", hashedCodes[0], "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.ValidateRecoveryCode(tt.hash, tt.input)
			if got != tt.want {
				t.Errorf("ValidateRecoveryCode() = %v, want %v", got, tt.want)
			}
		})
	}
}
