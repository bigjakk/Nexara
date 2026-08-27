package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// The predicate is the whole of the fix — every Update handler that pairs a
// stored secret with a mutable address consults it and nothing else. The cases
// that matter are the ones where an origin-only comparison would have passed:
// same scheme, same host, same port, different listener.
func TestCredentialRedirected(t *testing.T) {
	const stored = "ciphertext"

	tests := []struct {
		name            string
		newAddress      string
		existingAddress string
		storedSecret    string
		suppliedSecret  string
		want            bool
	}{
		{
			name:            "unchanged address keeps the stored secret",
			newAddress:      "https://pve.example.com:8006",
			existingAddress: "https://pve.example.com:8006",
			storedSecret:    stored,
			want:            false,
		},
		{
			name:            "new host without a new secret is a redirect",
			newAddress:      "https://attacker.example.net:8006",
			existingAddress: "https://pve.example.com:8006",
			storedSecret:    stored,
			want:            true,
		},
		{
			name:            "new host with a new secret is an ordinary re-home",
			newAddress:      "https://pve2.example.com:8006",
			existingAddress: "https://pve.example.com:8006",
			storedSecret:    stored,
			suppliedSecret:  "freshly typed",
			want:            false,
		},
		{
			// The bypass an earlier origin-only comparison allowed: the client
			// concatenates its API path onto the stored address, so a path
			// prefix routes the credential to a listener the attacker controls
			// behind a host that is still, by origin, the victim's.
			name:            "same origin, attacker-controlled path is a redirect",
			newAddress:      "https://pve.example.com:8006/attacker-path",
			existingAddress: "https://pve.example.com:8006",
			storedSecret:    stored,
			want:            true,
		},
		{
			name:            "same origin, query string is a redirect",
			newAddress:      "https://pve.example.com:8006?x=",
			existingAddress: "https://pve.example.com:8006",
			storedSecret:    stored,
			want:            true,
		},
		{
			// Fails safe rather than normalizing: the client would trim this
			// slash, so the re-prompt is unnecessary, but asking for a secret
			// one time too many is the harmless direction to be wrong in.
			name:            "trailing slash re-prompts rather than normalizing",
			newAddress:      "https://pve.example.com:8006/",
			existingAddress: "https://pve.example.com:8006",
			storedSecret:    stored,
			want:            true,
		},
		{
			// LDAP anonymous bind / OIDC public client: no credential is at
			// risk, so moving the address is not a redirect.
			name:            "no stored secret means nothing to redirect",
			newAddress:      "https://idp.example.net",
			existingAddress: "https://idp.example.com",
			storedSecret:    "",
			want:            false,
		},
		{
			name:            "scheme-only change is still a redirect",
			newAddress:      "https://pve.example.com:8006",
			existingAddress: "https://pve.example.com:8007",
			storedSecret:    stored,
			want:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := credentialRedirected(tt.newAddress, tt.existingAddress, tt.storedSecret, tt.suppliedSecret)
			if got != tt.want {
				t.Errorf("credentialRedirected(%q, %q, stored=%q, supplied=%q) = %v, want %v",
					tt.newAddress, tt.existingAddress, tt.storedSecret, tt.suppliedSecret, got, tt.want)
			}
		})
	}
}

// The refusal has to tell the operator which field to fill in and why, on a
// form where that field is optional every other time. A 400 that only said
// "bad request" would read as a bug in the dialog.
func TestErrCredentialRedirectNamesTheFieldAndReason(t *testing.T) {
	err := errCredentialRedirect("cluster API URL", "API token secret")

	fe, ok := err.(*fiber.Error)
	if !ok {
		t.Fatalf("expected *fiber.Error, got %T", err)
	}
	if fe.Code != fiber.StatusBadRequest {
		t.Errorf("code = %d, want %d", fe.Code, fiber.StatusBadRequest)
	}
	for _, want := range []string{
		"cluster API URL",
		"API token secret",
		"only ever sent to the address it was saved for",
	} {
		if !strings.Contains(fe.Message, want) {
			t.Errorf("message %q does not mention %q", fe.Message, want)
		}
	}
}

// audit_log.details is JSONB NOT NULL and readable by every Viewer. Building
// it by concatenation let a server name containing a quote either break the
// insert outright — leaving the action unaudited, since AuditLog only logs the
// failure — or inject attacker-chosen keys into the row.
func TestPBSAuditNameSurvivesHostileNames(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"plain", "PBS Primary"},
		{"embedded quote breaks concatenation", `x"`},
		{"key injection attempt", `x","api_key_id":"00000000-0000-0000-0000-000000000000`},
		{"backslash", `back\slash`},
		{"newline", "two\nlines"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got map[string]any
			if err := json.Unmarshal(pbsAuditName(tt.in), &got); err != nil {
				t.Fatalf("details for %q is not valid JSON: %v", tt.in, err)
			}
			if len(got) != 1 {
				t.Errorf("details carries %d keys, want exactly 1: %v", len(got), got)
			}
			if got["name"] != tt.in {
				t.Errorf("name = %q, want %q", got["name"], tt.in)
			}
		})
	}
}
