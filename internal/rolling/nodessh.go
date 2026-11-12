package rolling

import (
	"context"
	"errors"
	"fmt"

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
	// never pinned. Actionable: run Test Connection and confirm the fingerprint.
	ErrHostKeyNotPinned = errors.New("the SSH host key for this node is not pinned")
)

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
		return "SSH is not configured for this cluster. Add credentials under Security → Rolling Updates → SSH Credentials to read " + what + ".", true
	case errors.Is(err, ErrHostKeyNotPinned):
		return "This node's SSH host key is not pinned. Open Security → Rolling Updates → SSH Credentials, run Test Connection, and confirm the fingerprint.", true
	case errors.Is(err, ErrNodeAddressUnknown):
		return "No IP address is known for this node yet — the collector has not reported one.", true
	case errors.As(err, &mismatch):
		return "This node presented an SSH host key that does not match the pinned one. If it was not reinstalled, treat this as a possible interception; otherwise re-pin it under Security → Rolling Updates → SSH Credentials.", false
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
func RunNodeCommandLimited(ctx context.Context, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, nodeName, command string, maxOutput int) (*sshpkg.ExecResult, error) {
	cfg, err := NodeSSHConfig(ctx, queries, encryptionKey, clusterID, nodeName)
	if err != nil {
		return nil, err
	}
	if maxOutput > 0 {
		return sshpkg.ExecuteLimited(ctx, cfg, command, maxOutput)
	}
	return sshpkg.Execute(ctx, cfg, command)
}

// NodeSSHConfig resolves how to reach a node over SSH: the cluster's stored
// credentials, the node's address and its pinned host key. It dials nothing.
//
// Every SSH call to a node resolves through it, and so do the checks made
// before an automated job is created and before a node is drained, so those
// checks find exactly the missing pieces the upgrade would. They do not dial:
// a wrong password or a changed host key still shows only when it connects.
// A database error is ErrSSHLookup; a missing piece is its own sentinel.
func NodeSSHConfig(ctx context.Context, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, nodeName string) (sshpkg.Config, error) {
	creds, err := queries.GetClusterSSHCredentials(ctx, clusterID)
	if err != nil {
		// Only a genuinely absent row means "not configured". A database
		// outage lands here too, and classifying that as a missing row would
		// send the operator to add credentials that already exist.
		if errors.Is(err, pgx.ErrNoRows) {
			return sshpkg.Config{}, ErrSSHNotConfigured
		}
		return sshpkg.Config{}, fmt.Errorf("%w: look up SSH credentials: %w", ErrSSHLookup, err)
	}

	var password, privateKey string
	if creds.EncryptedPassword != "" {
		password, err = crypto.Decrypt(creds.EncryptedPassword, encryptionKey)
		if err != nil {
			return sshpkg.Config{}, fmt.Errorf("decrypt SSH password: %w", err)
		}
	}
	if creds.EncryptedPrivateKey != "" {
		privateKey, err = crypto.Decrypt(creds.EncryptedPrivateKey, encryptionKey)
		if err != nil {
			return sshpkg.Config{}, fmt.Errorf("decrypt SSH key: %w", err)
		}
	}

	// Node IP is populated by the collector from corosync. Fail loudly if absent
	// rather than falling back to DNS resolution of the node name.
	sshHost, addrErr := queries.GetNodeAddressByName(ctx, db.GetNodeAddressByNameParams{
		ClusterID: clusterID,
		Name:      nodeName,
	})
	if addrErr != nil && !errors.Is(addrErr, pgx.ErrNoRows) {
		return sshpkg.Config{}, fmt.Errorf("%w: look up node address: %w", ErrSSHLookup, addrErr)
	}
	// Covers both a missing row and a row the collector has not filled in yet.
	if sshHost == "" {
		return sshpkg.Config{}, fmt.Errorf("%w (node %q; the collector hasn't reported it)", ErrNodeAddressUnknown, nodeName)
	}

	pinned, pinErr := queries.GetSSHKnownHost(ctx, db.GetSSHKnownHostParams{
		ClusterID: clusterID,
		Host:      sshHost,
		Port:      creds.Port,
	})
	if pinErr != nil {
		if !errors.Is(pinErr, pgx.ErrNoRows) {
			return sshpkg.Config{}, fmt.Errorf("%w: look up pinned host key for %s: %w", ErrSSHLookup, sshHost, pinErr)
		}
		return sshpkg.Config{}, fmt.Errorf("%w (%s) — open Security → Rolling Updates → SSH Credentials, run Test Connection, and confirm the fingerprint", ErrHostKeyNotPinned, sshHost)
	}
	knownKey, parseErr := sshpkg.ParseAuthorizedKey(pinned.PublicKey)
	if parseErr != nil {
		return sshpkg.Config{}, fmt.Errorf("stored SSH host key for %s is corrupt: %w — delete and re-pin", sshHost, parseErr)
	}

	return sshpkg.Config{
		Host:         sshHost,
		Port:         int(creds.Port),
		Username:     creds.Username,
		Password:     password,
		PrivateKey:   privateKey,
		KnownHostKey: knownKey,
	}, nil
}
