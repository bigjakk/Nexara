package rolling

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	sshpkg "github.com/bigjakk/nexara/internal/ssh"
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
		return nil, fmt.Errorf("SSH credentials are not configured for this cluster")
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
	if addrErr != nil || sshHost == "" {
		return nil, fmt.Errorf("no IP address known for node %q yet (the collector hasn't reported it)", nodeName)
	}

	pinned, pinErr := queries.GetSSHKnownHost(ctx, db.GetSSHKnownHostParams{
		ClusterID: clusterID,
		Host:      sshHost,
		Port:      creds.Port,
	})
	if pinErr != nil {
		return nil, fmt.Errorf("SSH host key not pinned for %s — open Settings → SSH Credentials, run Test Connection, and confirm the fingerprint", sshHost)
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
