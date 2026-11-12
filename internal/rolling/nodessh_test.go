package rolling

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	gossh "golang.org/x/crypto/ssh"

	db "github.com/bigjakk/nexara/internal/db/generated"
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

// sshNode is the node the SSH tests resolve.
const sshNode = "pve-01"

// sshLookups are the queries NodeSSHConfig makes, in order.
var sshLookups = []string{"GetClusterSSHCredentials", "GetNodeAddressByName", "GetSSHKnownHost"}

// sshAnswers is what memberDB says to NodeSSHConfig. A nil creds, an empty
// address or an empty key is no row, and failing names the lookup that fails
// with a database error. Like the real tables it answers only for the
// cluster, sshNode, and the address and port the other two rows give, so a
// lookup keyed on anything else finds nothing.
type sshAnswers struct {
	creds   *db.ClusterSshCredential
	address string
	key     string // the pinned host key, in authorized_keys form
	failing string
}

func (a *sshAnswers) row(cluster uuid.UUID, name string, args []any) pgx.Row {
	if name == a.failing {
		return rowErr{errors.New("connection reset by peer")}
	}
	if id, _ := args[0].(uuid.UUID); id != cluster {
		return rowErr{pgx.ErrNoRows}
	}
	switch name {
	case "GetClusterSSHCredentials":
		if a.creds == nil {
			return rowErr{pgx.ErrNoRows}
		}
		return structRow{*a.creds}
	case "GetNodeAddressByName":
		if node, _ := args[1].(string); a.address == "" || node != sshNode {
			return rowErr{pgx.ErrNoRows}
		}
		return structRow{struct{ Address string }{a.address}}
	}
	host, _ := args[1].(string)
	port, _ := args[2].(int32)
	if a.key == "" || a.creds == nil || host != a.address || port != a.creds.Port {
		return rowErr{pgx.ErrNoRows}
	}
	return structRow{db.SshKnownHost{ClusterID: cluster, Host: host, Port: port, PublicKey: a.key}}
}

// testHostKey is a fresh ed25519 host key in authorized_keys form.
func testHostKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := gossh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(gossh.MarshalAuthorizedKey(key))
}

// NodeSSHConfig is the resolution every SSH call to a node makes. The checks
// before a create and before a drain rely on it telling a missing piece (a
// setup problem to report) from a failed read (ErrSSHLookup, to retry).
func TestNodeSSHConfig(t *testing.T) {
	t.Parallel()
	cluster := uuid.New()
	ready := sshAnswers{
		creds:   &db.ClusterSshCredential{ClusterID: cluster, Username: "root", Port: 2222},
		address: "192.0.2.11",
		key:     testHostKey(t),
	}
	for _, tt := range []struct {
		name string
		edit func(*sshAnswers)
		want error
	}{
		{"ready", func(*sshAnswers) {}, nil},
		{"no credentials", func(a *sshAnswers) { a.creds = nil }, ErrSSHNotConfigured},
		{"no address yet", func(a *sshAnswers) { a.address = "" }, ErrNodeAddressUnknown},
		{"no pinned key", func(a *sshAnswers) { a.key = "" }, ErrHostKeyNotPinned},
		{"credentials unreadable", func(a *sshAnswers) { a.failing = sshLookups[0] }, ErrSSHLookup},
		{"address unreadable", func(a *sshAnswers) { a.failing = sshLookups[1] }, ErrSSHLookup},
		{"pinned key unreadable", func(a *sshAnswers) { a.failing = sshLookups[2] }, ErrSSHLookup},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			answers := ready
			tt.edit(&answers)
			d := &memberDB{t: t, cluster: cluster, ssh: &answers}
			cfg, err := NodeSSHConfig(context.Background(), db.New(d), "", cluster, sshNode)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if lookup := errors.Is(err, ErrSSHLookup); lookup != errors.Is(tt.want, ErrSSHLookup) {
				t.Errorf("errors.Is(%v, ErrSSHLookup) = %v", err, lookup)
			}
			if err == nil && (cfg.Host != "192.0.2.11" || cfg.Port != 2222 || cfg.KnownHostKey == nil) {
				t.Errorf("config = %+v, want the node's address, the credentials' port and the pinned key", cfg)
			}
		})
	}
}

// startNode checks an automated job's node over SSH before draining it: a node
// the upgrade cannot reach fails with nothing moved, and one whose settings
// cannot be read waits, undrained, for the next tick. A manual job needs no SSH.
func TestStartNode_ChecksSSHBeforeTheDrain(t *testing.T) {
	t.Parallel()
	cluster := uuid.New()
	creds := &db.ClusterSshCredential{ClusterID: cluster, Username: "root", Port: 22}
	// Every Proxmox call fails, so a node past the check fails at the first
	// call of its drain, the guest listing.
	const drainFailed = "snapshot guests: "
	for _, tt := range []struct {
		name    string
		auto    bool
		answers sshAnswers
		failure string // the start of the node's failure reason; "" for none
	}{
		{"unreachable", true, sshAnswers{creds: creds, address: "192.0.2.11"},
			"not drained, since the automated upgrade could not reach it: " + ErrHostKeyNotPinned.Error()},
		{"settings unreadable", true, sshAnswers{failing: sshLookups[0]}, ""},
		{"reachable", true, sshAnswers{creds: creds, address: "192.0.2.11", key: testHostKey(t)}, drainFailed},
		{"manual, no SSH at all", false, sshAnswers{}, drainFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := &memberDB{t: t, cluster: cluster, members: []string{sshNode}, ssh: &tt.answers}
			o, client, sent := engineOn(t, d, nil)
			job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, AutoUpgrade: tt.auto, DrainGuests: true}
			o.startNode(context.Background(), client, job,
				db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: sshNode, Step: "pending"})

			if got := strings.Join(d.failures, " | "); !strings.HasPrefix(got, tt.failure) || (got == "") != (tt.failure == "") {
				t.Errorf("failed the node with %q, want a reason starting %q", got, tt.failure)
			}
			listed := slices.ContainsFunc(sent(), func(r string) bool { return strings.HasSuffix(r, "/qemu") })
			if want := tt.failure == drainFailed; listed != want {
				t.Errorf("listed the guests to drain: %v, want %v (sent %v)", listed, want, sent())
			}
		})
	}
}
