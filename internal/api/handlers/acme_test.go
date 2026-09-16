package handlers

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// `delete` has to survive Fiber's binder as a []string before the client ever
// sees it. Nothing else proves that link: the client tests build the struct in
// Go, and if the binder dropped the list the handler would send a perfectly
// valid request that clears nothing — the same silent no-op the delete support
// was added to fix.
//
// The struct is shared with the GET response, so this also pins that `digest`
// round-trips: read it from the config, hand it back on the write.
//
// Scope: this exercises the struct and the binder, not SetNodeACMEConfig. A
// handler that stopped binding into proxmox.NodeACMEConfig would leave this
// green — the static guard in missing_object_guard_test.go is what watches the
// handler's own wiring.
func TestBindBody_PromotesNodeACMEDeleteList(t *testing.T) {
	app := fiber.New()
	var got proxmox.NodeACMEConfig
	app.Put("/t", func(c fiber.Ctx) error {
		if err := c.Bind().Body(&got); err != nil {
			return err
		}
		return c.SendStatus(fiber.StatusOK)
	})

	req := httptest.NewRequest(fiber.MethodPut, "/t", strings.NewReader(
		`{"acmedomain0":"node.example.com","delete":["acmedomain1","acmedomain2"],`+
			`"digest":"da39a3ee5e6b4b0d3255bfef95601890afd80709"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	// Checked first so a binder failure reads as one error rather than three
	// field mismatches.
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 — the body failed to bind", resp.StatusCode)
	}

	if got.ACMEDomain0 != "node.example.com" {
		t.Errorf("ACMEDomain0 = %q", got.ACMEDomain0)
	}
	if len(got.Delete) != 2 || got.Delete[0] != "acmedomain1" || got.Delete[1] != "acmedomain2" {
		t.Errorf("Delete = %v, want [acmedomain1 acmedomain2]", got.Delete)
	}
	if got.Digest != "da39a3ee5e6b4b0d3255bfef95601890afd80709" {
		t.Errorf("Digest = %q", got.Digest)
	}
}

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
