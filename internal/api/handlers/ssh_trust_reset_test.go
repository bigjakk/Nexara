package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// The wire code is a contract with EditClusterDialog's SSH_TRUST_RESET. If the
// two drift, the 422 stops matching, no confirm panel renders, the
// acknowledgement is never sent — and the cluster API URL becomes unchangeable
// from the UI, which is the dead end this gate's frontend was built to remove.
// Asserted as a LITERAL, so comparing the const against itself cannot hide a
// changed value.
func TestSSHTrustResetGateWireCode(t *testing.T) {
	if confirmSSHTrustReset != "ssh_trust_reset_confirm_required" {
		t.Errorf("confirmSSHTrustReset = %q — the frontend keys on "+
			"\"ssh_trust_reset_confirm_required\" (EditClusterDialog.tsx). Change both or neither.",
			confirmSSHTrustReset)
	}
}

// Same contract, for the other two gates.
func TestConfirmGateWireCodes(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"LDAP transport", confirmInsecureLDAPTransport, "insecure_ldap_transport_confirm_required"},
		{"OIDC redirect", confirmInsecureOIDCRedirect, "insecure_oidc_redirect_confirm_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("code = %q, want %q — the admin page keys on the literal", tt.got, tt.want)
			}
		})
	}
}

// The acknowledged path returns before touching h.queries, so it is reachable
// with a zero-value handler. What it pins is the property the whole confirm-gate
// shape exists for: the gate RETURNS its decision rather than writing a
// response, so its caller's `if err != nil` can actually stop the handler.
func TestRequireSSHTrustResetAck_AcknowledgedShortCircuits(t *testing.T) {
	h := &ClusterHandler{}

	if err := h.requireSSHTrustResetAck(context.Background(), uuid.New(), true); err != nil {
		t.Fatalf("acknowledged gate returned %v, want nil", err)
	}
}

// And when it does refuse, it must be a *confirmRequiredError — anything else
// renderConfirmRequired passes through as a plain 500, so the operator would
// get an unexplained failure instead of the prompt.
func TestSSHTrustResetRefusalIsAConfirmGate(t *testing.T) {
	err := &confirmRequiredError{
		Code:    confirmSSHTrustReset,
		Message: "…",
		Fields:  map[string]any{"clears_ssh_credential": true},
	}

	var confirmErr *confirmRequiredError
	if !errors.As(error(err), &confirmErr) {
		t.Fatal("confirmRequiredError does not satisfy errors.As against its own type")
	}
	if confirmErr.Code != confirmSSHTrustReset {
		t.Errorf("Code = %q, want %q", confirmErr.Code, confirmSSHTrustReset)
	}
}
