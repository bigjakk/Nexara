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
	// ErrSSHNotConfigured means the cluster has no stored SSH credentials —
	// or that reading them failed. Actionable: add credentials in Settings.
	ErrSSHNotConfigured = errors.New("SSH credentials are not configured for this cluster")

	// ErrNodeAddressUnknown means the collector has not yet reported an IP
	// address for the node, so there is nothing to connect to.
	ErrNodeAddressUnknown = errors.New("no IP address is known for this node yet")

	// ErrHostKeyNotPinned means credentials exist but the node's host key was
	// never pinned. Actionable: run Test Connection and confirm the fingerprint.
	ErrHostKeyNotPinned = errors.New("the SSH host key for this node is not pinned")
)

// RunNodeCommand runs a shell command on a cluster node over SSH using the
// cluster's stored SSH credentials and pinned host key. It returns descriptive
// errors when SSH credentials or the pinned host key are missing so callers can
// surface actionable guidance (or fall back to a REST-based path).
//
// This mirrors the credential/host-key flow the rolling-update orchestrator uses
// for its remote apt upgrades, centralized so node-maintenance (and future
// callers) don't duplicate it.
func RunNodeCommand(ctx context.Context, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, nodeName, command string) (*sshpkg.ExecResult, error) {
	creds, err := queries.GetClusterSSHCredentials(ctx, clusterID)
	if err != nil {
		// Only a genuinely absent row means "not configured". A database
		// outage lands here too, and classifying that as a missing row would
		// send the operator to add credentials that already exist — so it
		// stays an unclassified error the caller reports generically and logs.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSSHNotConfigured
		}
		return nil, fmt.Errorf("look up SSH credentials: %w", err)
	}

	var password, privateKey string
	if creds.EncryptedPassword != "" {
		password, err = crypto.Decrypt(creds.EncryptedPassword, encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt SSH password: %w", err)
		}
	}
	if creds.EncryptedPrivateKey != "" {
		privateKey, err = crypto.Decrypt(creds.EncryptedPrivateKey, encryptionKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt SSH key: %w", err)
		}
	}

	// Node IP is populated by the collector from corosync. Fail loudly if absent
	// rather than falling back to DNS resolution of the node name.
	sshHost, addrErr := queries.GetNodeAddressByName(ctx, db.GetNodeAddressByNameParams{
		ClusterID: clusterID,
		Name:      nodeName,
	})
	if addrErr != nil && !errors.Is(addrErr, pgx.ErrNoRows) {
		return nil, fmt.Errorf("look up node address: %w", addrErr)
	}
	// Covers both a missing row and a row the collector has not filled in yet.
	if sshHost == "" {
		return nil, fmt.Errorf("%w (node %q; the collector hasn't reported it)", ErrNodeAddressUnknown, nodeName)
	}

	pinned, pinErr := queries.GetSSHKnownHost(ctx, db.GetSSHKnownHostParams{
		ClusterID: clusterID,
		Host:      sshHost,
		Port:      creds.Port,
	})
	if pinErr != nil {
		if !errors.Is(pinErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("look up pinned host key for %s: %w", sshHost, pinErr)
		}
		return nil, fmt.Errorf("%w (%s) — open Settings → SSH Credentials, run Test Connection, and confirm the fingerprint", ErrHostKeyNotPinned, sshHost)
	}
	knownKey, parseErr := sshpkg.ParseAuthorizedKey(pinned.PublicKey)
	if parseErr != nil {
		return nil, fmt.Errorf("stored SSH host key for %s is corrupt: %w — delete and re-pin", sshHost, parseErr)
	}

	cfg := sshpkg.Config{
		Host:         sshHost,
		Port:         int(creds.Port),
		Username:     creds.Username,
		Password:     password,
		PrivateKey:   privateKey,
		KnownHostKey: knownKey,
	}
	return sshpkg.Execute(ctx, cfg, command)
}
