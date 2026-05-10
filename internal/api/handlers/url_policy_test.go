package handlers

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func httpRequest(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

func readBody(t *testing.T, body io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestValidateURLFormat(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		wantMsg string
	}{
		{name: "valid https", raw: "https://pve.example.com:8006", wantErr: false},
		{name: "missing scheme", raw: "pve.example.com:8006", wantErr: true, wantMsg: "scheme"},
		{name: "missing host", raw: "https://", wantErr: true, wantMsg: "scheme and host"},
		{name: "http rejected", raw: "http://pve.example.com", wantErr: true, wantMsg: "HTTPS"},
		{name: "ftp rejected", raw: "ftp://pve.example.com", wantErr: true, wantMsg: "HTTPS"},
		{name: "credentials in url", raw: "https://root:secret@pve.example.com", wantErr: true, wantMsg: "credentials"},
		{name: "empty", raw: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateURLFormat(tc.raw)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantMsg != "" && err != nil && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}

func TestClassifyIP(t *testing.T) {
	cases := []struct {
		name         string
		ip           string
		allowPrivate bool
		wantOK       bool
		wantHard     bool
	}{
		// Always-blocked classes regardless of allowPrivate.
		{name: "cloud metadata IPv4", ip: "169.254.169.254", allowPrivate: false, wantOK: false, wantHard: true},
		{name: "cloud metadata IPv4 allow-private has no effect", ip: "169.254.169.254", allowPrivate: true, wantOK: false, wantHard: true},
		{name: "unspecified IPv4", ip: "0.0.0.0", allowPrivate: true, wantOK: false, wantHard: true},
		{name: "unspecified IPv6", ip: "::", allowPrivate: true, wantOK: false, wantHard: true},
		{name: "multicast IPv4", ip: "224.0.0.1", allowPrivate: true, wantOK: false, wantHard: true},
		{name: "multicast IPv6", ip: "ff02::1", allowPrivate: true, wantOK: false, wantHard: true},
		{name: "limited broadcast", ip: "255.255.255.255", allowPrivate: true, wantOK: false, wantHard: true},
		{name: "class E", ip: "240.0.0.1", allowPrivate: true, wantOK: false, wantHard: true},
		{name: "ipv4-mapped metadata", ip: "::ffff:169.254.169.254", allowPrivate: true, wantOK: false, wantHard: true},

		// Private + lab classes — gated by allowPrivate.
		{name: "loopback IPv4 blocked", ip: "127.0.0.1", allowPrivate: false, wantOK: false, wantHard: false},
		{name: "loopback IPv4 allowed", ip: "127.0.0.1", allowPrivate: true, wantOK: true},
		{name: "loopback IPv6 blocked", ip: "::1", allowPrivate: false, wantOK: false, wantHard: false},
		{name: "rfc1918 10/8 blocked", ip: "10.0.0.1", allowPrivate: false, wantOK: false, wantHard: false},
		{name: "rfc1918 10/8 allowed", ip: "10.0.0.1", allowPrivate: true, wantOK: true},
		{name: "rfc1918 192.168/16 blocked", ip: "192.168.1.10", allowPrivate: false, wantOK: false, wantHard: false},
		{name: "rfc1918 172.16/12 blocked", ip: "172.20.5.5", allowPrivate: false, wantOK: false, wantHard: false},
		{name: "ipv6 ula blocked", ip: "fc00::1", allowPrivate: false, wantOK: false, wantHard: false},
		{name: "ipv6 ula allowed", ip: "fc00::1", allowPrivate: true, wantOK: true},
		{name: "ipv6 site-local blocked", ip: "fec0::1", allowPrivate: false, wantOK: false, wantHard: false},
		{name: "ipv6 site-local allowed", ip: "fec0::1", allowPrivate: true, wantOK: true},
		{name: "cgnat blocked", ip: "100.64.0.1", allowPrivate: false, wantOK: false, wantHard: false},
		{name: "cgnat allowed", ip: "100.64.0.1", allowPrivate: true, wantOK: true},
		{name: "link-local IPv4 (non-metadata)", ip: "169.254.1.1", allowPrivate: false, wantOK: false, wantHard: false},
		{name: "link-local IPv6", ip: "fe80::1", allowPrivate: false, wantOK: false, wantHard: false},

		// Public addresses always pass.
		{name: "public IPv4", ip: "8.8.8.8", allowPrivate: false, wantOK: true},
		{name: "public IPv6", ip: "2606:4700:4700::1111", allowPrivate: false, wantOK: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid test IP %q", tc.ip)
			}
			err := classifyIP(ip, tc.allowPrivate)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var pErr *addressPolicyError
			if !errors.As(err, &pErr) {
				t.Fatalf("expected *addressPolicyError, got %T", err)
			}
			if pErr.HardReject != tc.wantHard {
				t.Errorf("HardReject = %v, want %v", pErr.HardReject, tc.wantHard)
			}
			if pErr.IP == "" {
				t.Errorf("expected IP populated, got empty")
			}
		})
	}
}

func TestEnforceURLAddressPolicy_LiteralIPs(t *testing.T) {
	// We use IP literals so the test never touches the resolver.
	ctx := context.Background()
	cases := []struct {
		name         string
		raw          string
		allowPrivate bool
		wantOK       bool
		wantHard     bool
	}{
		{name: "metadata literal hard block", raw: "https://169.254.169.254/api2/json", wantOK: false, wantHard: true},
		{name: "metadata literal cannot be overridden", raw: "https://169.254.169.254/api2/json", allowPrivate: true, wantOK: false, wantHard: true},
		{name: "loopback warns", raw: "https://127.0.0.1:8006", wantOK: false, wantHard: false},
		{name: "loopback allowed", raw: "https://127.0.0.1:8006", allowPrivate: true, wantOK: true},
		{name: "rfc1918 warns", raw: "https://10.1.2.3:8006", wantOK: false, wantHard: false},
		{name: "rfc1918 allowed", raw: "https://10.1.2.3:8006", allowPrivate: true, wantOK: true},
		{name: "ipv6 ula bracketed warns", raw: "https://[fc00::1]:8006", wantOK: false, wantHard: false},
		{name: "ipv6 ula bracketed allowed", raw: "https://[fc00::1]:8006", allowPrivate: true, wantOK: true},
		{name: "public ipv4 allowed", raw: "https://8.8.8.8", wantOK: true},
		{name: "multicast hard block", raw: "https://224.1.2.3", allowPrivate: true, wantOK: false, wantHard: true},
		{name: "unspecified hard block", raw: "https://0.0.0.0", allowPrivate: true, wantOK: false, wantHard: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := enforceURLAddressPolicy(ctx, tc.raw, tc.allowPrivate)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var pErr *addressPolicyError
			if !errors.As(err, &pErr) {
				t.Fatalf("expected *addressPolicyError, got %T", err)
			}
			if pErr.HardReject != tc.wantHard {
				t.Errorf("HardReject = %v, want %v", pErr.HardReject, tc.wantHard)
			}
		})
	}
}

func TestEnforceURLAddressPolicy_DNSFailureIsAllowed(t *testing.T) {
	// Hosts that fail to resolve should not be rejected by the policy — the
	// real connection attempt will surface a more useful error.
	ctx := context.Background()
	err := enforceURLAddressPolicy(ctx, "https://this-host-must-not-resolve.invalid.", false)
	if err != nil {
		t.Fatalf("expected nil for unresolvable host, got %v", err)
	}
}

func TestRenderAddressPolicyError_HardReject(t *testing.T) {
	app := fiber.New()
	app.Get("/test", func(c *fiber.Ctx) error {
		err := &addressPolicyError{
			HardReject: true,
			IP:         "169.254.169.254",
			Reason:     "Address resolves to the cloud instance metadata service IP (169.254.169.254). Connections to this address are never allowed.",
		}
		return renderAddressPolicyError(c, err)
	})

	resp, err := app.Test(httpRequest("GET", "/test"))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}

func TestRenderAddressPolicyError_PrivateConfirm(t *testing.T) {
	app := fiber.New()
	app.Get("/test", func(c *fiber.Ctx) error {
		err := &addressPolicyError{
			HardReject: false,
			IP:         "10.0.0.1",
			Reason:     "Address resolves to a private/loopback IP (10.0.0.1).",
		}
		return renderAddressPolicyError(c, err)
	})

	resp, err := app.Test(httpRequest("GET", "/test"))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusUnprocessableEntity)
	}
	body := readBody(t, resp.Body)
	if !strings.Contains(body, "private_address_confirm_required") {
		t.Errorf("expected private_address_confirm_required code in body, got %q", body)
	}
	if !strings.Contains(body, "10.0.0.1") {
		t.Errorf("expected ip in details, got %q", body)
	}
}

func TestRenderAddressPolicyError_NonPolicyFallsBack(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Get("/test", func(c *fiber.Ctx) error {
		return renderAddressPolicyError(c, errors.New("plain error"))
	})

	resp, err := app.Test(httpRequest("GET", "/test"))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusBadRequest)
	}
}
