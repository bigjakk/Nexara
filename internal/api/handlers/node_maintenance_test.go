package handlers

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/rolling"
	sshpkg "github.com/bigjakk/nexara/internal/ssh"
)

func TestNodeMaintenanceCommand(t *testing.T) {
	if got := nodeMaintenanceCommand(true, "pve1"); got != "ha-manager crm-command node-maintenance enable 'pve1'" {
		t.Errorf("enable cmd = %q", got)
	}
	if got := nodeMaintenanceCommand(false, "pve1"); got != "ha-manager crm-command node-maintenance disable 'pve1'" {
		t.Errorf("disable cmd = %q", got)
	}
}

func TestNodeMaintenanceNameRe(t *testing.T) {
	valid := []string{"pve1", "pve-01", "node.example", "PVE-01", "a_b"}
	for _, n := range valid {
		if !nodeMaintenanceNameRe.MatchString(n) {
			t.Errorf("expected %q to be accepted", n)
		}
	}
	// Injection / metacharacter attempts must be rejected.
	invalid := []string{
		"", "pve 1", "pve;rm -rf /", "pve$(whoami)", "pve`id`",
		"pve'1", "pve|x", "pve/..", "pve&", "pve\n", "pve>out",
	}
	for _, n := range invalid {
		if nodeMaintenanceNameRe.MatchString(n) {
			t.Errorf("expected %q to be rejected", n)
		}
	}
}

// A refused SSH login, and one held back after a refusal, say which instead of
// "Node command failed"; any other failure stays generic, its detail unsent.
func TestMaintenanceCommandError_SaysWhenTheLoginWasRefusedOrHeldBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		err        error
	}{
		{"held back", "not trying it again", fmt.Errorf("pve-01: %w", rolling.ErrSSHLoginHeldBack)},
		{"refused", "This node refused the SSH login. Check", fmt.Errorf("dial 192.0.2.10: %w", sshpkg.ErrAuthRejected)},
		{"anything else", "Node command failed", errors.New("dial 192.0.2.10: i/o timeout")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var fe *fiber.Error
			if err := maintenanceCommandError(tc.err, uuid.New(), "pve-01"); !errors.As(err, &fe) ||
				fe.Code != fiber.StatusBadGateway || !strings.Contains(fe.Message, tc.want) || strings.Contains(fe.Message, "192.0.2.10") {
				t.Errorf("got %v, want a 502 saying %q and no address", err, tc.want)
			}
		})
	}
}
