package handlers

import (
	"errors"
	"strings"
	"testing"
)

// redirect_uri was previously written to the database with no validation at
// all. These are the shapes that must not survive, plus the ones a real
// install legitimately uses.
func TestValidateOIDCRedirectURI(t *testing.T) {
	const good = "https://nexara.example.com/api/v1/auth/oidc/callback"

	tests := []struct {
		name    string
		uri     string
		wantErr string // substring; empty means the URI must be accepted
	}{
		{name: "the value the admin UI generates", uri: good},
		{
			name: "reverse proxy serving under a path prefix",
			uri:  "https://example.com/nexara/api/v1/auth/oidc/callback",
		},
		{
			name: "trailing slash is tolerated",
			uri:  good + "/",
		},
		{
			name: "loopback over http, for a localhost dev install",
			uri:  "http://localhost:5173/api/v1/auth/oidc/callback",
		},
		{
			name: "loopback by IP over http",
			uri:  "http://127.0.0.1:8080/api/v1/auth/oidc/callback",
		},
		{
			// Cleartext is no longer a FORMAT error — it is an overridable
			// refusal, so the validator accepts it and
			// requireOIDCRedirectSchemeAck is what stops it.
			name: "http on a LAN address is a format-valid callback",
			uri:  "http://192.168.1.10/api/v1/auth/oidc/callback",
		},
		{
			// Opaque, so it trips the host check before the scheme check —
			// rejected either way, which is what matters.
			name:    "javascript scheme",
			uri:     "javascript:alert(1)//api/v1/auth/oidc/callback",
			wantErr: "absolute URL",
		},
		{
			name:    "non-http scheme with a host",
			uri:     "ftp://nexara.example.com/api/v1/auth/oidc/callback",
			wantErr: "https",
		},
		{
			name:    "relative, no host",
			uri:     "/api/v1/auth/oidc/callback",
			wantErr: "absolute URL",
		},
		{
			name:    "embedded credentials",
			uri:     "https://user:pass@nexara.example.com/api/v1/auth/oidc/callback",
			wantErr: "credentials",
		},
		{
			// RFC 6749 §3.1.2 forbids it outright.
			name:    "fragment",
			uri:     good + "#x",
			wantErr: "fragment",
		},
		{
			// A host the caller controls is NOT caught — no format check can.
			// What is caught is it not being a callback this install serves.
			name:    "attacker host on a path we do not serve",
			uri:     "https://attacker.example/cb",
			wantErr: "callback this install serves",
		},
		{
			name:    "empty",
			uri:     "",
			wantErr: "absolute URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOIDCRedirectURI(tt.uri)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateOIDCRedirectURI(%q) = %v, want accepted", tt.uri, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateOIDCRedirectURI(%q) accepted it, want error containing %q", tt.uri, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

// The rule this documents is the one the comment in validateOIDCRedirectURI
// admits to: an attacker-controlled HOST serving the right path passes. Pin it
// so nobody later reads the validator as stronger than it is.
func TestValidateOIDCRedirectURI_DoesNotClaimOriginPinning(t *testing.T) {
	if err := validateOIDCRedirectURI("https://attacker.example/api/v1/auth/oidc/callback"); err != nil {
		t.Fatalf("expected format validation to accept a well-formed URI on any host, got %v", err)
	}
}

// Cleartext is refused by default and overridable, rather than judged from the
// request. An earlier version passed c.Scheme() in as the install's posture,
// but Fiber only reports https when TRUSTED_PROXIES is set AND the proxy sends
// X-Forwarded-Proto — so on the documented default it read "http" for every
// request and turned the rule off on exactly the deployments it protected.
func TestRequireOIDCRedirectSchemeAck(t *testing.T) {
	tests := []struct {
		name         string
		uri          string
		acknowledged bool
		wantRefusal  bool
	}{
		{
			name: "https needs no confirmation",
			uri:  "https://nexara.example.com/api/v1/auth/oidc/callback",
		},
		{
			name:        "cleartext on a LAN address is refused",
			uri:         "http://nexara.lan:8080/api/v1/auth/oidc/callback",
			wantRefusal: true,
		},
		{
			name:        "cleartext on a public host is refused",
			uri:         "http://nexara.example.com/api/v1/auth/oidc/callback",
			wantRefusal: true,
		},
		{
			name:         "acknowledged cleartext is allowed through",
			uri:          "http://nexara.lan:8080/api/v1/auth/oidc/callback",
			acknowledged: true,
		},
		{
			// The code never leaves the machine, so there is nothing to confirm.
			name: "loopback by name",
			uri:  "http://localhost:5173/api/v1/auth/oidc/callback",
		},
		{
			name: "loopback by IPv4",
			uri:  "http://127.0.0.1:5173/api/v1/auth/oidc/callback",
		},
		{
			name: "loopback by IPv6",
			uri:  "http://[::1]:5173/api/v1/auth/oidc/callback",
		},
		{
			// Not loopback, despite the prefix.
			name:        "localhost as a subdomain is not loopback",
			uri:         "http://localhost.attacker.example/api/v1/auth/oidc/callback",
			wantRefusal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireOIDCRedirectSchemeAck(tt.uri, tt.acknowledged)
			if !tt.wantRefusal {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("got nil, want a confirm-required refusal — the callback would have been saved")
			}
			var confirmErr *confirmRequiredError
			if !errors.As(err, &confirmErr) {
				t.Fatalf("error %v is not a *confirmRequiredError, so renderConfirmRequired "+
					"would return a plain 500 instead of the confirm prompt", err)
			}
			if confirmErr.Code != confirmInsecureOIDCRedirect {
				t.Errorf("Code = %q, want %q", confirmErr.Code, confirmInsecureOIDCRedirect)
			}
		})
	}
}

// Path checks run on the escaped form: a percent-encoded slash names one
// segment, not the callback route, and must not satisfy the suffix.
func TestValidateOIDCRedirectURI_EncodedSlashDoesNotSatisfyThePath(t *testing.T) {
	err := validateOIDCRedirectURI("https://nexara.example.com/api/v1/auth/oidc%2Fcallback")
	if err == nil {
		t.Fatal("percent-encoded slash satisfied the callback path check")
	}
	if !strings.Contains(err.Error(), "callback this install serves") {
		t.Errorf("error %q is not the path refusal", err)
	}
}

// A Host is not a hostname.
func TestValidateOIDCRedirectURI_PortOnlyHostIsRejected(t *testing.T) {
	if err := validateOIDCRedirectURI("https://:8080/api/v1/auth/oidc/callback"); err == nil {
		t.Error("a URL with a port but no host was accepted")
	}
}
