package handlers

import (
	"errors"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// The `delete` list used to be pinned here as a Fiber-binder test: nothing else
// proved that the list survived the bind, and a dropped one would have sent a
// perfectly valid request that cleared nothing.
//
// SetNodeACMEConfig no longer binds a struct — the route is declared
// (internal/api/registry_acme.go) and the list arrives through the parameter
// schema — so that link has moved with it.
// TestNodeACMEDeleteListReachesTheHandlerAsAList in
// internal/api/registry_acme_test.go drives the REAL declaration end to end and
// asserts the same thing about the same three fields, including that `digest`
// round-trips from the GET into the PUT.

// mapNodeConfigError turns PVE's digest-mismatch die into a 409. Everything
// else has to keep the status mapProxmoxError gave it — the phrase list is the
// only thing separating "someone else edited this node" from every other way
// the write can fail, and answering 409 to an unrelated failure would tell the
// operator to reload when reloading will not help.
func TestMapNodeConfigError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "digest mismatch is a conflict",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    "detected modified configuration - file changed by other user? Try again.",
			},
			want: fiber.StatusConflict,
		},
		{
			// The client refused it; it never reached the node.
			name: "an undeletable key stays a 400",
			err:  proxmox.ErrInvalidInput,
			want: fiber.StatusBadRequest,
		},
		{
			name: "an unreachable node stays a 502",
			err:  proxmox.ErrConnectionFailed,
			want: fiber.StatusBadGateway,
		},
		{
			name: "a permission failure stays a 403",
			err:  proxmox.ErrForbidden,
			want: fiber.StatusForbidden,
		},
		{
			name: "an unrelated PVE die stays a 502",
			err: &proxmox.APIError{
				StatusCode: 500,
				Message:    "unable to parse acme domain config",
			},
			want: fiber.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			err := mapNodeConfigError(tt.err)
			if !errors.As(err, &fe) {
				t.Fatalf("mapNodeConfigError(%v) = %v, want a *fiber.Error", tt.err, err)
			}
			if fe.Code != tt.want {
				t.Errorf("status = %d (%q), want %d", fe.Code, fe.Message, tt.want)
			}
		})
	}

	if err := mapNodeConfigError(nil); err != nil {
		t.Errorf("mapNodeConfigError(nil) = %v, want nil", err)
	}
}
