// Package crypto provides authenticated symmetric encryption helpers.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrInvalidKey indicates the encryption key is not a valid 32-byte hex string.
	ErrInvalidKey = errors.New("encryption key must be 64 hex characters (32 bytes)")
	// ErrDecryptionFailed indicates the ciphertext could not be decrypted.
	ErrDecryptionFailed = errors.New("decryption failed")
)

// aeadCache memoises the AEAD per encryption key. The AES key schedule and
// GCM derivation cost shows up in CPU profiles for token-heavy workloads
// (every per-cluster sync, alert evaluation, rolling-update tick, etc.
// decrypts the cluster's API token before each Proxmox call). The hex key
// is constant per-process today, so this is essentially a single-entry
// cache; the sync.Map keeps the door open for future key-rotation flows.
var aeadCache sync.Map // map[string]cipher.AEAD

func aeadFor(hexKey string) (cipher.AEAD, error) {
	if v, ok := aeadCache.Load(hexKey); ok {
		return v.(cipher.AEAD), nil
	}

	key, err := parseKey(hexKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	actual, _ := aeadCache.LoadOrStore(hexKey, gcm)
	return actual.(cipher.AEAD), nil
}

// Encrypt encrypts plaintext using AES-256-GCM with the given hex-encoded key.
// Returns base64-encoded nonce+ciphertext.
func Encrypt(plaintext, hexKey string) (string, error) {
	gcm, err := aeadFor(hexKey)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded nonce+ciphertext using AES-256-GCM.
func Decrypt(encoded, hexKey string) (string, error) {
	gcm, err := aeadFor(hexKey)
	if err != nil {
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrDecryptionFailed
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", ErrDecryptionFailed
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrDecryptionFailed
	}

	return string(plaintext), nil
}

func parseKey(hexKey string) ([]byte, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, ErrInvalidKey
	}
	return key, nil
}
