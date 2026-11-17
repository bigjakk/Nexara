package rolling

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	sshpkg "github.com/bigjakk/nexara/internal/ssh"
)

// Sentinel errors for the RunNodeCommand preconditions a caller may want to
// tell apart — to render actionable guidance instead of a generic failure, or
// to degrade to "not available" rather than surfacing an error.
//
// They exist so callers use errors.Is rather than substring-matching the
// message, which silently stops working the first time the wording is
// improved. Each is wrapped with the detail (host, node, cause) a server-side
// log wants; that detail is deliberately NOT safe to hand to an API caller
// verbatim, so classify on the sentinel and log the wrapped error.
var (
	// ErrSSHNotConfigured means the cluster has no stored SSH credentials.
	// Actionable: add credentials in Settings.
	ErrSSHNotConfigured = errors.New("SSH credentials are not configured for this cluster")

	// ErrSSHLookup means reading what the call needs failed (a database
	// error, not a missing row), so nothing is known to be wrong with the
	// setup. A caller that can wait tries again instead of reporting one.
	ErrSSHLookup = errors.New("could not read the SSH settings")

	// ErrNodeAddressUnknown means the collector has not yet reported an IP
	// address for the node, so there is nothing to connect to.
	ErrNodeAddressUnknown = errors.New("no IP address is known for this node yet")

	// ErrHostKeyNotPinned means credentials exist but the node's host key was
	// never pinned. Actionable: run Test connection and confirm the fingerprint.
	ErrHostKeyNotPinned = errors.New("the SSH host key for this node is not pinned")

	// ErrSSHLoginHeldBack means the node refused a recent login and this call
	// was answered from memory without dialling (RunNodeCommandLimited). It
	// wraps sshpkg.ErrAuthRejected. Classified: no login happened, so nothing
	// is audited as one.
	ErrSSHLoginHeldBack = fmt.Errorf("%w recently, so it was not tried again", sshpkg.ErrAuthRejected)
)

// heldBackError is ErrSSHLoginHeldBack with how long the pause has left.
type heldBackError struct{ remaining time.Duration }

func (e *heldBackError) Error() string {
	return fmt.Sprintf("%s; not tried again for %s, or until the SSH credentials are changed or Test connection passes",
		ErrSSHLoginHeldBack, e.remaining)
}

func (e *heldBackError) Unwrap() error { return ErrSSHLoginHeldBack }

// UnavailableReason maps a RunNodeCommand failure to a sentence for the
// operator that names what could not be read ("hardware temperatures", "the
// memory layout"), and reports whether it recognised the cause.
//
// The recognised causes are the ones the operator can act on, and each stops
// before anything is dialed. Their text is written here rather than taken from
// the error, which carries node IPs, host-key fingerprints and decrypt detail
// that must not reach an API response. Anything else gets the generic
// sentence, and the caller logs the error.
//
// A changed host key gets its own sentence but stays unclassified, so the
// caller still logs it: it is the one failure that may be an interception.
func UnavailableReason(err error, what string) (reason string, classified bool) {
	var mismatch *sshpkg.HostKeyMismatchError
	switch {
	case errors.Is(err, ErrSSHNotConfigured):
		return "SSH is not configured for this cluster. Add credentials under Security → Rolling updates → SSH access to read " + what + ".", true
	case errors.Is(err, ErrHostKeyNotPinned):
		return "This node's SSH host key is not pinned. Open Security → Rolling updates → SSH access, run Test connection, and confirm the fingerprint.", true
	case errors.Is(err, ErrNodeAddressUnknown):
		return "No IP address is known for this node yet — the collector has not reported one.", true
	case errors.Is(err, ErrSSHLoginHeldBack):
		wait := "a few minutes"
		var held *heldBackError
		if errors.As(err, &held) {
			wait = fmt.Sprintf("%d min", max(1, int(held.remaining.Round(time.Minute)/time.Minute)))
		}
		return "This node refused the SSH login, so Nexara is not trying it again for " + wait +
			". Saving new credentials under Security → Rolling updates → SSH access, or a Test connection that logs in, ends the pause.", true
	case errors.Is(err, sshpkg.ErrAuthRejected):
		// Unclassified so the caller still logs it: the first one is a real
		// failed login, and a node's own lockout may follow.
		return "This node refused the SSH login. Check the credentials under Security → Rolling updates → SSH access and run Test connection.", false
	case errors.As(err, &mismatch):
		return "This node presented an SSH host key that does not match the pinned one. If it was not reinstalled, treat this as a possible interception; otherwise re-pin it under Security → Rolling updates → SSH access.", false
	default:
		return "Could not read " + what + " over SSH from this node.", false
	}
}

// RunNodeCommand runs a shell command on a cluster node over SSH using the
// cluster's stored SSH credentials and pinned host key. It returns descriptive
// errors when SSH credentials or the pinned host key are missing so callers can
// surface actionable guidance (or fall back to a REST-based path).
//
// This mirrors the credential/host-key flow the rolling-update orchestrator uses
// for its remote apt upgrades, centralized so node-maintenance (and future
// callers) don't duplicate it.
func RunNodeCommand(ctx context.Context, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, nodeName, command string) (*sshpkg.ExecResult, error) {
	return RunNodeCommandLimited(ctx, queries, encryptionKey, clusterID, nodeName, command, 0)
}

// RunNodeCommandLimited is RunNodeCommand keeping at most maxOutput bytes of
// the command's stdout and of its stderr (sshpkg.ExecuteLimited); 0 keeps
// Execute's default cap.
//
// After the node refuses the login (sshpkg.ErrAuthRejected), every caller gets
// that refusal back without a dial for loginBackoffWindow, so a page polling a
// node's sensors cannot add a failed login to the node's auth log every minute
// and trip its lockout. Changed credentials, a changed node address, a success
// or a passing Test connection (ForgetSSHRejection) end it at once.
func RunNodeCommandLimited(ctx context.Context, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, nodeName, command string, maxOutput int) (*sshpkg.ExecResult, error) {
	return runNodeCommand(ctx, queries, encryptionKey, clusterID, nodeName, command, maxOutput, loginBackoff, executeSSH)
}

func executeSSH(ctx context.Context, cfg sshpkg.Config, command string, maxOutput int) (*sshpkg.ExecResult, error) {
	if maxOutput > 0 {
		return sshpkg.ExecuteLimited(ctx, cfg, command, maxOutput)
	}
	return sshpkg.Execute(ctx, cfg, command)
}

type sshExecFunc func(ctx context.Context, cfg sshpkg.Config, command string, maxOutput int) (*sshpkg.ExecResult, error)

func runNodeCommand(ctx context.Context, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, nodeName, command string, maxOutput int, backoff *rejectionBackoff, exec sshExecFunc) (*sshpkg.ExecResult, error) {
	cfg, stamp, err := nodeSSHConfig(ctx, queries, encryptionKey, clusterID, nodeName)
	if err != nil {
		return nil, err
	}
	key := nodeKey{clusterID, nodeName}
	if err := backoff.held(key, stamp); err != nil {
		return nil, err
	}
	res, err := exec(ctx, cfg, command, maxOutput)
	switch {
	case errors.Is(err, sshpkg.ErrAuthRejected):
		backoff.remember(key, stamp)
	case err == nil:
		backoff.forget(key)
	}
	return res, err
}

// loginBackoffWindow is how long a refused login is answered from memory. Ten
// minutes keeps a node to two failures in any fifteen, under pam_faillock's
// default of three, and to one in fail2ban's default ten-minute window.
const loginBackoffWindow = 10 * time.Minute

// maxRememberedRejections bounds the map: one entry per node whose login was
// refused, so a cap far above any estate's node count only matters if
// something goes wrong.
const maxRememberedRejections = 4096

var loginBackoff = newRejectionBackoff(time.Now)

// ForgetSSHRejection ends the back-off for a node, for a Test connection that
// logged in to it.
func ForgetSSHRejection(clusterID uuid.UUID, nodeName string) {
	loginBackoff.forget(nodeKey{clusterID, nodeName})
}

type nodeKey struct {
	cluster uuid.UUID
	node    string
}

// credStamp identifies the credentials and address a login used, without
// holding either secret: a digest of the stored ciphertexts (re-encrypted with
// a fresh nonce on every save), the account, the port, the row's updated_at
// and the node's address.
type credStamp [sha256.Size]byte

type rejection struct {
	stamp credStamp
	until time.Time
}

// rejectionBackoff remembers which nodes refused a login, and until when. It
// holds the error class only, never a password, key or passphrase.
type rejectionBackoff struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[nodeKey]rejection
}

func newRejectionBackoff(now func() time.Time) *rejectionBackoff {
	return &rejectionBackoff{now: now, entries: map[nodeKey]rejection{}}
}

// held answers a remembered refusal for key, unless it has expired or the
// credentials have changed since, in which case it is dropped.
func (b *rejectionBackoff) held(key nodeKey, stamp credStamp) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.entries[key]
	if !ok {
		return nil
	}
	now := b.now()
	if r.stamp != stamp || !now.Before(r.until) {
		delete(b.entries, key)
		return nil
	}
	return &heldBackError{remaining: r.until.Sub(now).Round(time.Second)}
}

func (b *rejectionBackoff) remember(key nodeKey, stamp credStamp) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if _, ok := b.entries[key]; !ok && len(b.entries) >= maxRememberedRejections {
		for k, r := range b.entries {
			if !now.Before(r.until) {
				delete(b.entries, k)
			}
		}
		if len(b.entries) >= maxRememberedRejections {
			return
		}
	}
	b.entries[key] = rejection{stamp: stamp, until: now.Add(loginBackoffWindow)}
}

func (b *rejectionBackoff) forget(key nodeKey) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, key)
}

// NodeSSHConfig resolves how to reach a node over SSH: the cluster's stored
// credentials, the node's address and its pinned host key. It dials nothing.
//
// Every SSH call to a node resolves through it, and so do the checks made
// before an automated job is created and before a node is drained, so those
// checks find exactly the missing pieces the upgrade would. It dials nothing;
// the check before a drain then logs in with what it returns (sshReady).
// A database error is ErrSSHLookup; a missing piece is its own sentinel.
func NodeSSHConfig(ctx context.Context, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, nodeName string) (sshpkg.Config, error) {
	cfg, _, err := nodeSSHConfig(ctx, queries, encryptionKey, clusterID, nodeName)
	return cfg, err
}

func nodeSSHConfig(ctx context.Context, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, nodeName string) (sshpkg.Config, credStamp, error) {
	cfg, creds, err := resolveNodeSSH(ctx, queries, encryptionKey, clusterID, nodeName)
	if err != nil {
		return sshpkg.Config{}, credStamp{}, err
	}
	h := sha256.New()
	for _, part := range []string{creds.EncryptedPassword, creds.EncryptedPrivateKey, creds.Username,
		strconv.Itoa(int(creds.Port)), creds.UpdatedAt.UTC().Format(time.RFC3339Nano), cfg.Host} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	var stamp credStamp
	h.Sum(stamp[:0])
	return cfg, stamp, nil
}

func resolveNodeSSH(ctx context.Context, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, nodeName string) (sshpkg.Config, db.ClusterSshCredential, error) {
	creds, err := queries.GetClusterSSHCredentials(ctx, clusterID)
	if err != nil {
		// Only a genuinely absent row means "not configured". A database
		// outage lands here too, and classifying that as a missing row would
		// send the operator to add credentials that already exist.
		if errors.Is(err, pgx.ErrNoRows) {
			return sshpkg.Config{}, db.ClusterSshCredential{}, ErrSSHNotConfigured
		}
		return sshpkg.Config{}, db.ClusterSshCredential{}, fmt.Errorf("%w: look up SSH credentials: %w", ErrSSHLookup, err)
	}

	var password, privateKey string
	if creds.EncryptedPassword != "" {
		password, err = crypto.Decrypt(creds.EncryptedPassword, encryptionKey)
		if err != nil {
			return sshpkg.Config{}, db.ClusterSshCredential{}, fmt.Errorf("decrypt SSH password: %w", err)
		}
	}
	if creds.EncryptedPrivateKey != "" {
		privateKey, err = crypto.Decrypt(creds.EncryptedPrivateKey, encryptionKey)
		if err != nil {
			return sshpkg.Config{}, db.ClusterSshCredential{}, fmt.Errorf("decrypt SSH key: %w", err)
		}
	}

	// Node IP is populated by the collector from corosync. Fail loudly if absent
	// rather than falling back to DNS resolution of the node name.
	sshHost, addrErr := queries.GetNodeAddressByName(ctx, db.GetNodeAddressByNameParams{
		ClusterID: clusterID,
		Name:      nodeName,
	})
	if addrErr != nil && !errors.Is(addrErr, pgx.ErrNoRows) {
		return sshpkg.Config{}, db.ClusterSshCredential{}, fmt.Errorf("%w: look up node address: %w", ErrSSHLookup, addrErr)
	}
	// Covers both a missing row and a row the collector has not filled in yet.
	if sshHost == "" {
		return sshpkg.Config{}, db.ClusterSshCredential{}, fmt.Errorf("%w (node %q; the collector hasn't reported it)", ErrNodeAddressUnknown, nodeName)
	}

	pinned, pinErr := queries.GetSSHKnownHost(ctx, db.GetSSHKnownHostParams{
		ClusterID: clusterID,
		Host:      sshHost,
		Port:      creds.Port,
	})
	if pinErr != nil {
		if !errors.Is(pinErr, pgx.ErrNoRows) {
			return sshpkg.Config{}, db.ClusterSshCredential{}, fmt.Errorf("%w: look up pinned host key for %s: %w", ErrSSHLookup, sshHost, pinErr)
		}
		return sshpkg.Config{}, db.ClusterSshCredential{}, fmt.Errorf("%w (%s) — open Security → Rolling updates → SSH access, run Test connection, and confirm the fingerprint", ErrHostKeyNotPinned, sshHost)
	}
	knownKey, parseErr := sshpkg.ParseAuthorizedKey(pinned.PublicKey)
	if parseErr != nil {
		return sshpkg.Config{}, db.ClusterSshCredential{}, fmt.Errorf("stored SSH host key for %s is corrupt: %w — delete and re-pin", sshHost, parseErr)
	}

	return sshpkg.Config{
		Host:         sshHost,
		Port:         int(creds.Port),
		Username:     creds.Username,
		Password:     password,
		PrivateKey:   privateKey,
		KnownHostKey: knownKey,
	}, creds, nil
}
