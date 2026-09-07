package handlers

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestMapProxmoxError_InvalidInputIsClientError(t *testing.T) {
	// A rejected identifier never reaches Proxmox, so it must not be reported as
	// a Proxmox failure — the operator needs to know their input was the problem.
	err := fmt.Errorf("%w: volume id %q has a bad path segment %q",
		proxmox.ErrInvalidInput, "local:iso/../x", "..")

	var fe *fiber.Error
	if !errors.As(mapProxmoxError(err), &fe) {
		t.Fatalf("mapProxmoxError returned %T, want *fiber.Error", mapProxmoxError(err))
	}
	if fe.Code != fiber.StatusBadRequest {
		t.Errorf("status = %d, want %d", fe.Code, fiber.StatusBadRequest)
	}
	if !strings.Contains(fe.Message, "bad path segment") {
		t.Errorf("message = %q, want it to explain the rejection", fe.Message)
	}
}

func TestMapProxmoxError_SentinelsKeepTheirStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"not found", proxmox.ErrNotFound, fiber.StatusNotFound},
		{"forbidden", proxmox.ErrForbidden, fiber.StatusForbidden},
		{"connection failed", proxmox.ErrConnectionFailed, fiber.StatusBadGateway},
		{"unknown", errors.New("boom"), fiber.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(mapProxmoxError(tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.want {
				t.Errorf("status = %d, want %d", fe.Code, tt.want)
			}
		})
	}
}

// Proxmox answers with a JSON envelope, and mapProxmoxError's 502 message goes
// straight onto the operator's screen (describeError in ClusterCephTab.tsx), so
// what it picks out of that envelope is user-visible text.
func TestMapProxmoxError_APIErrorEnvelope(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		wantCode int
		wantMsg  string
	}{
		{
			// The case that motivated this: a cluster with no Ceph. The
			// operator was shown the whole document, braces and all.
			name:     "envelope message is unwrapped and trimmed",
			message:  `{"message":"binary not installed: /usr/bin/ceph-mon\n","data":null}`,
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "binary not installed: /usr/bin/ceph-mon",
		},
		{
			// Same flattened text, but with no rejection map alongside it, so
			// there is nothing to say the operator's input was at fault. The
			// map is what promotes this to a 400 — see the Fields case below.
			name:     "flattened text alone is not a validation failure",
			message:  "vmid: property is not defined in schema",
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "vmid: property is not defined in schema",
		},
		{
			name:     "plain text is passed through untouched",
			message:  "500 Internal Server Error",
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "500 Internal Server Error",
		},
		{
			// Nothing to unwrap, so the envelope remains the most informative
			// thing available — better than an empty error.
			name:     "envelope with no message falls back to the raw body",
			message:  `{"data":null}`,
			wantCode: fiber.StatusBadGateway,
			wantMsg:  `{"data":null}`,
		},
		{
			name:     "whitespace-only message falls back to the raw body",
			message:  `{"message":"  \n ","data":null}`,
			wantCode: fiber.StatusBadGateway,
			wantMsg:  `{"message":"  \n ","data":null}`,
		},
		{
			name:     "empty message falls back to the raw body",
			message:  "",
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "",
		},
		{
			// A bare JSON scalar does not unmarshal into the envelope struct.
			name:     "non-object JSON is passed through untouched",
			message:  "[1,2]",
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "[1,2]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &proxmox.APIError{StatusCode: 500, Message: tt.message}

			var fe *fiber.Error
			if !errors.As(mapProxmoxError(err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", fe.Code, tt.wantCode)
			}
			if fe.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMsg)
			}
		})
	}
}

// A rejected parameter is the operator's own typo, so it must not be dressed up
// as an upstream gateway failure. The APIError below is exactly what
// proxmox.checkStatus builds from a real PVE parameter-verification body — see
// TestCheckStatus_ParameterVerificationKeepsTheFieldMap, which pins that half.
func TestMapProxmoxError_ParameterRejectionIsAClientError(t *testing.T) {
	err := &proxmox.APIError{
		StatusCode: 400,
		Message:    "vmid: property is not defined in schema",
		Fields:     map[string]string{"vmid": "property is not defined in schema"},
	}

	var fe *fiber.Error
	if !errors.As(mapProxmoxError(err), &fe) {
		t.Fatal("want *fiber.Error")
	}
	if fe.Code != fiber.StatusBadRequest {
		t.Errorf("status = %d, want %d", fe.Code, fiber.StatusBadRequest)
	}
	// Message, not a fresh join of Fields: the stable order lives in Message,
	// and re-deriving it here would reintroduce the per-request shuffle.
	if fe.Message != "vmid: property is not defined in schema" {
		t.Errorf("message = %q, want the flattened field list", fe.Message)
	}
}

// Only a rejection map on a 400 is the operator's mistake. Everything else
// stays a gateway error — including a 5xx that happens to carry a map, which a
// reverse proxy in front of Proxmox can produce and which must keep being
// retried rather than being blamed on the caller.
func TestMapProxmoxError_OnlyA400RejectionIsAClientError(t *testing.T) {
	tests := []struct {
		name     string
		err      *proxmox.APIError
		wantCode int
		wantMsg  string
	}{
		{
			name:     "400 with no rejection map",
			err:      &proxmox.APIError{StatusCode: 400, Message: `{"message":"nope\n","data":null}`},
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "nope",
		},
		{
			name: "503 carrying a rejection map is still the gateway's failure",
			err: &proxmox.APIError{
				StatusCode: 503,
				Message:    "detail: no healthy upstream",
				Fields:     map[string]string{"detail": "no healthy upstream"},
			},
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "detail: no healthy upstream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(mapProxmoxError(tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", fe.Code, tt.wantCode)
			}
			if fe.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMsg)
			}
		})
	}
}

// Branch-ordering guard: a sentinel that wraps an APIError must keep the
// sentinel's status. The shape is synthetic — api_client.go wraps with
// "%w: %s", so an APIError never actually lands inside ErrConnectionFailed —
// but the ordering it pins is what stops a future edit reversing the two.
func TestMapProxmoxError_WrappedAPIErrorKeepsSentinelPrecedence(t *testing.T) {
	inner := &proxmox.APIError{StatusCode: 500, Message: `{"message":"nope\n"}`}
	wrapped := fmt.Errorf("%w: %w", proxmox.ErrConnectionFailed, inner)

	var fe *fiber.Error
	if !errors.As(mapProxmoxError(wrapped), &fe) {
		t.Fatal("want *fiber.Error")
	}
	if fe.Code != fiber.StatusBadGateway {
		t.Errorf("status = %d, want %d", fe.Code, fiber.StatusBadGateway)
	}
	if fe.Message != "Failed to connect to Proxmox" {
		t.Errorf("message = %q, want the connection-failed message", fe.Message)
	}
}
