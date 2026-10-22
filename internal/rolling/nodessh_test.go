package rolling

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	sshpkg "github.com/bigjakk/nexara/internal/ssh"
)

// Each cause an operator can act on gets its own sentence naming what could
// not be read; anything else is generic and left for the caller to log.
func TestUnavailableReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		err            error
		wantClassified bool
		wantContains   []string
	}{
		{"missing credentials points at where they are set", fmt.Errorf("%w: no rows in result set", ErrSSHNotConfigured), true, []string{"Security → Rolling Updates → SSH Credentials", "the memory layout"}},
		{"an unpinned host key asks for Test Connection", fmt.Errorf("%w (192.0.2.10)", ErrHostKeyNotPinned), true, []string{"Test Connection"}},
		{"an unknown node address blames the collector", fmt.Errorf("%w (node %q)", ErrNodeAddressUnknown, "pve-01"), true, []string{"collector"}},
		{"a changed host key is named, and still logged", &sshpkg.HostKeyMismatchError{Host: "192.0.2.10"}, false, []string{"does not match the pinned one"}},
		{"anything else is generic", errors.New("dial 192.0.2.10:22: connect: connection refused"), false, []string{"Could not read the memory layout"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason, classified := UnavailableReason(tt.err, "the memory layout")
			if classified != tt.wantClassified {
				t.Errorf("classified = %v, want %v", classified, tt.wantClassified)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(reason, want) {
					t.Errorf("reason %q does not contain %q", reason, want)
				}
			}
		})
	}
}

// The errors carry node IPs, host-key fingerprints and decrypt detail; none of
// it may reach the sentence an API response shows.
func TestUnavailableReasonLeaksNothing(t *testing.T) {
	t.Parallel()
	secrets := []string{
		"192.0.2.10",
		"SHA256:AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJKKK",
		"cipher: message authentication failed",
	}
	errs := []error{
		fmt.Errorf("%w: %s", ErrSSHNotConfigured, secrets[2]),
		fmt.Errorf("%w (%s) fingerprint %s", ErrHostKeyNotPinned, secrets[0], secrets[1]),
		fmt.Errorf("%w (node %q at %s)", ErrNodeAddressUnknown, "pve-01", secrets[0]),
		fmt.Errorf("SSH handshake with %s: %s", secrets[0], secrets[1]),
		&sshpkg.HostKeyMismatchError{Host: secrets[0], PresentedFingerprint: secrets[1]},
	}
	for _, err := range errs {
		reason, _ := UnavailableReason(err, "hardware temperatures")
		for _, secret := range secrets {
			if strings.Contains(reason, secret) {
				t.Errorf("reason %q leaked %q from error %v", reason, secret, err)
			}
		}
	}
}
