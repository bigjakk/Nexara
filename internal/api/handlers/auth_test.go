package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/bigjakk/nexara/internal/auth"
)

func TestRegister_MissingFields(t *testing.T) {
	app := newTestApp(t)

	tests := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing password", `{"email":"test@example.com"}`},
		{"missing email", `{"password":"Str0ng!Pass"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func TestRegister_InvalidEmail(t *testing.T) {
	app := newTestApp(t)

	body := `{"email":"not-an-email","password":"Str0ng!Pass"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestRegister_WeakPassword(t *testing.T) {
	app := newTestApp(t)

	tests := []struct {
		name     string
		password string
	}{
		{"too short", "S1!a"},
		{"no uppercase", "str0ng!pass"},
		{"no digit", "Strong!Pass"},
		{"no special", "Str0ngPassw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"email":"test@example.com","password":"` + tt.password + `"}`
			req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusBadRequest, body)
			}
		})
	}
}

func TestLogin_MissingFields(t *testing.T) {
	app := newTestApp(t)

	tests := []struct {
		name string
		body string
	}{
		{"missing password", `{"email":"test@example.com"}`},
		{"missing email", `{"password":"Str0ng!Pass"}`},
		{"empty body", `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func TestLogin_InvalidJSON(t *testing.T) {
	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusBadRequest, body)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestRefresh_MissingToken(t *testing.T) {
	app := newTestApp(t)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestPasswordHashAndVerify(t *testing.T) {
	password := "Str0ng!Pass"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}

	if err := auth.CheckPassword(hash, password); err != nil {
		t.Errorf("CheckPassword() should succeed: %v", err)
	}

	if err := auth.CheckPassword(hash, "wrong"); err == nil {
		t.Error("CheckPassword() should fail for wrong password")
	}
}

func TestJWTRoundtrip(t *testing.T) {
	svc := auth.NewJWTService("test-secret", 15*time.Minute, 7*24*time.Hour)

	token, _, err := svc.GenerateAccessToken(
		[16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		"test@example.com",
		"admin",
	)
	if err != nil {
		t.Fatalf("GenerateAccessToken() error: %v", err)
	}

	claims, err := svc.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error: %v", err)
	}

	if claims.Email != "test@example.com" {
		t.Errorf("email = %q, want %q", claims.Email, "test@example.com")
	}
	if claims.Role != "admin" {
		t.Errorf("role = %q, want %q", claims.Role, "admin")
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"valid bearer", "Bearer eyJhbGciOi", "eyJhbGciOi"},
		{"lowercase bearer", "bearer eyJhbGciOi", "eyJhbGciOi"},
		{"no prefix", "eyJhbGciOi", ""},
		{"empty", "", ""},
		{"wrong prefix", "Basic abc123", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBearerTokenFromHeader(tt.header)
			if got != tt.want {
				t.Errorf("extractBearerTokenFromHeader(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

// newTestApp creates a Fiber app with auth handler for unit tests (no DB/Redis).
func newTestApp(t *testing.T) *fiber.App {
	t.Helper()

	jwtSvc := auth.NewJWTService("test-secret", 15*time.Minute, 7*24*time.Hour)
	handler := &AuthHandler{
		queries:    nil,
		jwtService: jwtSvc,
	}

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			message := "Internal Server Error"
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
				message = e.Message
			}
			return c.Status(code).JSON(fiber.Map{
				"error":   code,
				"message": message,
			})
		},
	})

	app.Post("/auth/register", handler.Register)
	app.Post("/auth/login", handler.Login)
	app.Post("/auth/refresh", handler.Refresh)

	return app
}

// extractBearerTokenFromHeader replicates the bearer token extraction logic for testing.
func extractBearerTokenFromHeader(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}
