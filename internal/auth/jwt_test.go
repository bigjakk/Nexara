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

	token, expiresAt, err := svc.GenerateAccessToken(userID, email, role, uuid.New())
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

	token, _, err := svc1.GenerateAccessToken(uuid.New(), "test@example.com", "user", uuid.New())
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

	token, _, err := svc.GenerateAccessToken(uuid.New(), "test@example.com", "user", uuid.New())
	if err != nil {
		t.Fatalf("GenerateAccessToken() error: %v", err)
	}

	_, err = svc.ValidateAccessToken(token)
	if err == nil {
		t.Error("ValidateAccessToken() should fail for expired token")
	}
}

func TestGenerateAndValidateConsoleToken(t *testing.T) {
	svc := newTestJWTService()
	userID := uuid.New()
	scope := ConsoleScope{
		ClusterID: uuid.NewString(),
		Node:      "pve1",
		VMID:      100,
		Type:      "vm_vnc",
	}

	token, _, err := svc.GenerateConsoleToken(userID, "test@example.com", "admin", ConsoleCredential{SessionID: uuid.New()}, scope, 5*time.Minute)
	if err != nil {
		t.Fatalf("GenerateConsoleToken() error: %v", err)
	}

	claims, err := svc.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error: %v", err)
	}
	if claims.ConsoleScope == nil {
		t.Fatal("ConsoleScope should be set on console token")
	}
	if claims.ConsoleScope.ClusterID != scope.ClusterID {
		t.Errorf("ClusterID = %q, want %q", claims.ConsoleScope.ClusterID, scope.ClusterID)
	}
	if claims.ConsoleScope.Node != scope.Node {
		t.Errorf("Node = %q, want %q", claims.ConsoleScope.Node, scope.Node)
	}
	if claims.ConsoleScope.VMID != scope.VMID {
		t.Errorf("VMID = %d, want %d", claims.ConsoleScope.VMID, scope.VMID)
	}
	if claims.ConsoleScope.Type != scope.Type {
		t.Errorf("Type = %q, want %q", claims.ConsoleScope.Type, scope.Type)
	}
}

func TestAccessTokenHasNoConsoleScope(t *testing.T) {
	svc := newTestJWTService()
	token, _, err := svc.GenerateAccessToken(uuid.New(), "test@example.com", "admin", uuid.New())
	if err != nil {
		t.Fatalf("GenerateAccessToken() error: %v", err)
	}

	claims, err := svc.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error: %v", err)
	}
	if claims.ConsoleScope != nil {
		t.Errorf("regular access token should not have ConsoleScope, got %+v", claims.ConsoleScope)
	}
}

func TestConsoleTokenExpiry(t *testing.T) {
	svc := newTestJWTService()
	scope := ConsoleScope{ClusterID: uuid.NewString(), Node: "pve1", Type: "node_shell"}

	token, _, err := svc.GenerateConsoleToken(uuid.New(), "test@example.com", "admin", ConsoleCredential{SessionID: uuid.New()}, scope, -1*time.Second)
	if err != nil {
		t.Fatalf("GenerateConsoleToken() error: %v", err)
	}

	if _, err := svc.ValidateAccessToken(token); err == nil {
		t.Error("expired console token should be rejected")
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

// TestTokensNameTheirCredential: an access token is minted only for a session, and a
// console token only for exactly one of a session and an API key — one naming
// neither would hold its console to nothing (internal/ws/console_credential.go).
func TestTokensNameTheirCredential(t *testing.T) {
	t.Parallel()
	svc := NewJWTService("test-secret", time.Minute, time.Hour)
	scope := ConsoleScope{ClusterID: "cluster01", Node: "pve-01", Type: "node_shell"}
	id := uuid.New()
	console := func(cred ConsoleCredential) error {
		_, _, err := svc.GenerateConsoleToken(id, "test@example.com", "admin", cred, scope, time.Minute)
		return err
	}
	tests := []struct {
		name string
		err  error
		ok   bool
	}{
		{"an access token for a session", func() error {
			_, _, err := svc.GenerateAccessToken(id, "test@example.com", "admin", id)
			return err
		}(), true},
		{"an access token for no session", func() error {
			_, _, err := svc.GenerateAccessToken(id, "test@example.com", "admin", uuid.Nil)
			return err
		}(), false},
		{"a console token for a session", console(ConsoleCredential{SessionID: id}), true},
		{"a console token for an API key", console(ConsoleCredential{APIKeyID: id}), true},
		{"a console token for neither", console(ConsoleCredential{}), false},
		{"a console token for both", console(ConsoleCredential{SessionID: id, APIKeyID: id}), false},
	}
	for _, tt := range tests {
		if (tt.err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want minted %v", tt.name, tt.err, tt.ok)
		}
	}
}
