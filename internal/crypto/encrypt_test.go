package crypto

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// validKey is a 32-byte hex-encoded key for testing.
var validKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		plaintext string
	}{
		{"simple string", "hello world"},
		{"empty string", ""},
		{"unicode", "こんにちは世界"},
		{"special chars", `{"token":"abc-123!@#$%^&*"}`},
		{"long string", string(make([]byte, 4096))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := Encrypt(tt.plaintext, validKey)
			if err != nil {
				t.Fatalf("Encrypt() error = %v", err)
			}

			decrypted, err := Decrypt(encrypted, validKey)
			if err != nil {
				t.Fatalf("Decrypt() error = %v", err)
			}

			if decrypted != tt.plaintext {
				t.Errorf("round-trip mismatch: got %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestDecryptWrongKey(t *testing.T) {
	otherKey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	encrypted, err := Encrypt("secret", validKey)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	_, err = Decrypt(encrypted, otherKey)
	if err != ErrDecryptionFailed {
		t.Errorf("Decrypt() with wrong key: got %v, want %v", err, ErrDecryptionFailed)
	}
}

func TestTamperedCiphertext(t *testing.T) {
	encrypted, err := Encrypt("secret", validKey)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	data, _ := base64.StdEncoding.DecodeString(encrypted)
	// Flip last byte.
	data[len(data)-1] ^= 0xff
	tampered := base64.StdEncoding.EncodeToString(data)

	_, err = Decrypt(tampered, validKey)
	if err != ErrDecryptionFailed {
		t.Errorf("Decrypt() with tampered ciphertext: got %v, want %v", err, ErrDecryptionFailed)
	}
}

func TestInvalidKeyFormat(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"too short", "0123456789abcdef"},
		{"too long", validKey + "ff"},
		{"not hex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Encrypt("test", tt.key)
			if err != ErrInvalidKey {
				t.Errorf("Encrypt() with key %q: got %v, want %v", tt.key, err, ErrInvalidKey)
			}

			_, err = Decrypt("dGVzdA==", tt.key)
			if err != ErrInvalidKey {
				t.Errorf("Decrypt() with key %q: got %v, want %v", tt.key, err, ErrInvalidKey)
			}
		})
	}
}

func TestNonceUniqueness(t *testing.T) {
	const iterations = 100
	seen := make(map[string]bool, iterations)

	for range iterations {
		encrypted, err := Encrypt("same plaintext", validKey)
		if err != nil {
			t.Fatalf("Encrypt() error = %v", err)
		}

		data, _ := base64.StdEncoding.DecodeString(encrypted)
		nonce := hex.EncodeToString(data[:12]) // GCM nonce is 12 bytes
		if seen[nonce] {
			t.Fatal("duplicate nonce detected")
		}
		seen[nonce] = true
	}
}

func TestDecryptInvalidBase64(t *testing.T) {
	_, err := Decrypt("not-valid-base64!!!", validKey)
	if err != ErrDecryptionFailed {
		t.Errorf("Decrypt() with invalid base64: got %v, want %v", err, ErrDecryptionFailed)
	}
}

func TestDecryptTooShort(t *testing.T) {
	// Base64 of fewer than 12 bytes (nonce size).
	short := base64.StdEncoding.EncodeToString([]byte("short"))
	_, err := Decrypt(short, validKey)
	if err != ErrDecryptionFailed {
		t.Errorf("Decrypt() with short data: got %v, want %v", err, ErrDecryptionFailed)
	}
}
