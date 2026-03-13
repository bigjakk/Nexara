package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

const secretsFileName = ".secrets.json"

// secrets is the on-disk format for auto-generated secrets.
type secrets struct {
	JWTSecret     string `json:"jwt_secret"`
	EncryptionKey string `json:"encryption_key"`
}

// ResolveSecrets fills in JWT_SECRET and ENCRYPTION_KEY if they weren't
// provided via environment variables. It checks for a persisted secrets
// file in DataDir first; if none exists, it generates new secrets and
// writes them to disk. Env vars always take precedence.
func (c *Config) ResolveSecrets(logger *slog.Logger) error {
	if c.JWTSecret != "" && c.EncryptionKey != "" {
		// Both provided via env — nothing to do.
		return nil
	}

	secretsPath := filepath.Join(c.DataDir, secretsFileName)

	// Try to load existing secrets file.
	stored, err := loadSecrets(secretsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read secrets file: %w", err)
	}

	changed := false

	if c.JWTSecret == "" || c.JWTSecret == "change-this-to-a-secure-random-string" {
		if stored != nil && stored.JWTSecret != "" {
			c.JWTSecret = stored.JWTSecret
			logger.Info("loaded JWT_SECRET from secrets file")
		} else {
			secret, genErr := generateJWTSecret()
			if genErr != nil {
				return fmt.Errorf("generate JWT_SECRET: %w", genErr)
			}
			c.JWTSecret = secret
			changed = true
			logger.Info("generated new JWT_SECRET")
		}
	}

	if c.EncryptionKey == "" || c.EncryptionKey == "change-this-to-a-32-byte-hex-key" {
		if stored != nil && stored.EncryptionKey != "" {
			c.EncryptionKey = stored.EncryptionKey
			logger.Info("loaded ENCRYPTION_KEY from secrets file")
		} else {
			key, genErr := generateEncryptionKey()
			if genErr != nil {
				return fmt.Errorf("generate ENCRYPTION_KEY: %w", genErr)
			}
			c.EncryptionKey = key
			changed = true
			logger.Info("generated new ENCRYPTION_KEY")
		}
	}

	// Persist if we generated anything new.
	if changed {
		if err := saveSecrets(secretsPath, &secrets{
			JWTSecret:     c.JWTSecret,
			EncryptionKey: c.EncryptionKey,
		}, logger); err != nil {
			return fmt.Errorf("save secrets file: %w", err)
		}
	}

	return nil
}

func loadSecrets(path string) (*secrets, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from config DataDir, not user input
	if err != nil {
		return nil, err
	}
	var s secrets
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse secrets file: %w", err)
	}
	return &s, nil
}

func saveSecrets(path string, s *secrets, logger *slog.Logger) error {
	// Ensure the parent directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create secrets directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ") //nolint:gosec // intentionally writing secrets to a restricted file
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}

	logger.Info("secrets persisted", "path", path)
	return nil
}

// generateJWTSecret returns a 32-byte base64-encoded random string.
func generateJWTSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateEncryptionKey returns a 32-byte hex-encoded random string (64 hex chars).
func generateEncryptionKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
