package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestSessionResponseOmitsTokenHash is the assertion this DTO exists for.
//
// db.Session carries token_hash — the verifier for a live refresh token. If a
// future change serialises the model directly, or someone adds the field to
// sessionResponse "for debugging", every caller is handed the means to
// impersonate their own sessions, and anything that logs the response body
// spreads it further. Marshalling and searching the JSON catches that whatever
// route it arrives by, including an embedded struct.
func TestSessionResponseOmitsTokenHash(t *testing.T) {
	const secret = "a1b2c3d4e5f6-the-hash-value"

	session := db.Session{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		TokenHash:  secret,
		UserAgent:  "Mozilla/5.0",
		IpAddress:  "192.0.2.10",
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
		LastUsedAt: time.Now(),
	}

	encoded, err := json.Marshal(toSessionResponse(session, uuid.Nil))
	if err != nil {
		t.Fatalf("marshal session response: %v", err)
	}

	if strings.Contains(string(encoded), secret) {
		t.Errorf("session response leaks token_hash: %s", encoded)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "token") {
		t.Errorf("session response mentions a token field: %s", encoded)
	}
}

// TestToSessionResponseIsCurrent covers the flag the UI uses to warn before
// you sign out the device you are holding.
//
// The uuid.Nil case is the one worth pinning: currentSessionID returns Nil when
// there is no usable refresh cookie, and a naive equality would then mark a
// session current whenever its own id happened to be the zero UUID. More
// importantly it must never mark an *arbitrary* session current — no cookie
// means no session is flagged, not all of them.
func TestToSessionResponseIsCurrent(t *testing.T) {
	sessionID := uuid.New()
	otherID := uuid.New()

	tests := []struct {
		name      string
		current   uuid.UUID
		wantIsCur bool
	}{
		{name: "same session is current", current: sessionID, wantIsCur: true},
		{name: "different session is not", current: otherID, wantIsCur: false},
		{name: "no cookie flags nothing", current: uuid.Nil, wantIsCur: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toSessionResponse(db.Session{ID: sessionID}, tt.current)
			if got.IsCurrent != tt.wantIsCur {
				t.Errorf("IsCurrent = %v, want %v", got.IsCurrent, tt.wantIsCur)
			}
		})
	}
}

// TestToSessionResponseNullDeviceFields covers sessions predating migration
// 000045, which added the device columns. Those rows have them NULL, and the
// frontend renders the field directly — a null reaching it would print
// "null" as the device name rather than falling back to a placeholder.
func TestToSessionResponseNullDeviceFields(t *testing.T) {
	got := toSessionResponse(db.Session{ID: uuid.New()}, uuid.Nil)

	if got.DeviceName != "" {
		t.Errorf("DeviceName = %q, want empty for a NULL column", got.DeviceName)
	}
	if got.DeviceType != "" {
		t.Errorf("DeviceType = %q, want empty for a NULL column", got.DeviceType)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), ":null") {
		t.Errorf("null reached the JSON: %s", encoded)
	}
}

func TestTextOrEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   pgtype.Text
		want string
	}{
		{name: "valid text", in: pgtype.Text{String: "laptop", Valid: true}, want: "laptop"},
		{name: "null", in: pgtype.Text{Valid: false}, want: ""},
		{name: "null with a stale string", in: pgtype.Text{String: "stale", Valid: false}, want: ""},
		{name: "valid empty", in: pgtype.Text{String: "", Valid: true}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := textOrEmpty(tt.in); got != tt.want {
				t.Errorf("textOrEmpty(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSessionResponseTimestampsAreRFC3339 pins the wire format. The frontend
// parses these with `new Date(...)`, which silently yields Invalid Date for a
// Go default-formatted time ("2006-01-02 15:04:05.999999999 -0700 MST") — a
// failure that shows up as "Invalid Date" in the session list rather than as
// an error anyone traces back to here.
func TestSessionResponseTimestampsAreRFC3339(t *testing.T) {
	now := time.Date(2026, 9, 8, 14, 30, 5, 123456789, time.UTC)
	got := toSessionResponse(db.Session{
		ID:         uuid.New(),
		CreatedAt:  now,
		ExpiresAt:  now.Add(time.Hour),
		LastUsedAt: now,
	}, uuid.Nil)

	for _, field := range []struct {
		name  string
		value string
	}{
		{"created_at", got.CreatedAt},
		{"last_used_at", got.LastUsedAt},
		{"expires_at", got.ExpiresAt},
	} {
		if _, err := time.Parse(time.RFC3339Nano, field.value); err != nil {
			t.Errorf("%s = %q is not RFC3339Nano: %v", field.name, field.value, err)
		}
	}
}
