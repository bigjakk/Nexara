package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/proxdash/proxdash/internal/crypto"
)

// TOTPService provides TOTP enrollment and verification.
type TOTPService struct {
	encryptionKey string
	issuer        string
}

// NewTOTPService creates a new TOTP service.
func NewTOTPService(encryptionKey string) *TOTPService {
	return &TOTPService{
		encryptionKey: encryptionKey,
		issuer:        "ProxDash",
	}
}

// GenerateSecret creates a new TOTP secret for the given account email.
// Returns the encrypted secret (for DB storage) and the otpauth URL (for QR code).
func (s *TOTPService) GenerateSecret(email string) (encryptedSecret string, otpauthURL string, plainSecret string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.issuer,
		AccountName: email,
		Algorithm:   otp.AlgorithmSHA1,
		Digits:      otp.DigitsSix,
		Period:      30,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("generate TOTP key: %w", err)
	}

	encrypted, err := crypto.Encrypt(key.Secret(), s.encryptionKey)
	if err != nil {
		return "", "", "", fmt.Errorf("encrypt TOTP secret: %w", err)
	}

	return encrypted, key.URL(), key.Secret(), nil
}

// ValidateCode validates a TOTP code against an encrypted secret.
func (s *TOTPService) ValidateCode(encryptedSecret, code string) (bool, error) {
	secret, err := crypto.Decrypt(encryptedSecret, s.encryptionKey)
	if err != nil {
		return false, fmt.Errorf("decrypt TOTP secret: %w", err)
	}

	valid := totp.Validate(code, secret)
	return valid, nil
}

// recoveryCodeAlphabet contains characters used in recovery codes (no ambiguous chars).
const recoveryCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GenerateRecoveryCodes creates count random 8-character recovery codes.
// Returns both plaintext codes (to show user once) and bcrypt hashes (for DB storage).
func (s *TOTPService) GenerateRecoveryCodes(count int) (plainCodes []string, hashedCodes []string, err error) {
	plainCodes = make([]string, count)
	hashedCodes = make([]string, count)

	alphabetLen := big.NewInt(int64(len(recoveryCodeAlphabet)))

	for i := 0; i < count; i++ {
		var sb strings.Builder
		for j := 0; j < 8; j++ {
			idx, err := rand.Int(rand.Reader, alphabetLen)
			if err != nil {
				return nil, nil, fmt.Errorf("generate recovery code: %w", err)
			}
			sb.WriteByte(recoveryCodeAlphabet[idx.Int64()])
		}
		code := sb.String()
		plainCodes[i] = code[:4] + "-" + code[4:]

		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return nil, nil, fmt.Errorf("hash recovery code: %w", err)
		}
		hashedCodes[i] = string(hash)
	}

	return plainCodes, hashedCodes, nil
}

// ValidateRecoveryCode checks if the input code matches the bcrypt hash.
// The input should be the raw code without dashes.
func (s *TOTPService) ValidateRecoveryCode(hashedCode, inputCode string) bool {
	// Strip dashes from input
	clean := strings.ReplaceAll(strings.ToUpper(inputCode), "-", "")
	return bcrypt.CompareHashAndPassword([]byte(hashedCode), []byte(clean)) == nil
}
