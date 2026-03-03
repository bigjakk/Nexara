package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestJWTService() *JWTService {
	return NewJWTService("test-secret-key-for-testing", 15*time.Minute, 7*24*time.Hour)
}

func TestGenerateAndValidateAccessToken(t *testing.T) {
	svc := newTestJWTService()
	userID := uuid.New()
	email := "test@example.com"
	role := "admin"

	token, expiresAt, err := svc.GenerateAccessToken(userID, email, role)
	if err != nil {
		t.Fatalf("GenerateAccessToken() error: %v", err)
	}
	if token == "" {
		t.Fatal("token should not be empty")
	}
	if expiresAt.IsZero() {
		t.Fatal("expiresAt should not be zero")
	}

	claims, err := svc.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error: %v", err)
	}

	if claims.UserID != userID {
		t.Errorf("UserID = %v, want %v", claims.UserID, userID)
	}
	if claims.Email != email {
		t.Errorf("Email = %q, want %q", claims.Email, email)
	}
	if claims.Role != role {
		t.Errorf("Role = %q, want %q", claims.Role, role)
	}
}

func TestValidateAccessToken_InvalidToken(t *testing.T) {
	svc := newTestJWTService()
	_, err := svc.ValidateAccessToken("invalid.token.string")
	if err == nil {
		t.Error("ValidateAccessToken() should fail for invalid token")
	}
}

func TestValidateAccessToken_WrongSecret(t *testing.T) {
	svc1 := NewJWTService("secret-one", 15*time.Minute, 7*24*time.Hour)
	svc2 := NewJWTService("secret-two", 15*time.Minute, 7*24*time.Hour)

	token, _, err := svc1.GenerateAccessToken(uuid.New(), "test@example.com", "user")
	if err != nil {
		t.Fatalf("GenerateAccessToken() error: %v", err)
	}

	_, err = svc2.ValidateAccessToken(token)
	if err == nil {
		t.Error("ValidateAccessToken() should fail with wrong secret")
	}
}

func TestValidateAccessToken_ExpiredToken(t *testing.T) {
	svc := NewJWTService("test-secret", -1*time.Hour, 7*24*time.Hour)

	token, _, err := svc.GenerateAccessToken(uuid.New(), "test@example.com", "user")
	if err != nil {
		t.Fatalf("GenerateAccessToken() error: %v", err)
	}

	_, err = svc.ValidateAccessToken(token)
	if err == nil {
		t.Error("ValidateAccessToken() should fail for expired token")
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	svc := newTestJWTService()

	token1, err := svc.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error: %v", err)
	}
	token2, err := svc.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error: %v", err)
	}

	if token1 == "" || token2 == "" {
		t.Fatal("refresh tokens should not be empty")
	}
	if token1 == token2 {
		t.Error("refresh tokens should be unique")
	}
	if len(token1) != 64 { // 32 bytes hex-encoded
		t.Errorf("refresh token length = %d, want 64", len(token1))
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	token := "test-refresh-token"
	hash1 := HashToken(token)
	hash2 := HashToken(token)
	if hash1 != hash2 {
		t.Error("HashToken should be deterministic")
	}
	if hash1 == token {
		t.Error("hash should not equal input")
	}
}
