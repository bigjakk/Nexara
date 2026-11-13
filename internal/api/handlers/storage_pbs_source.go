package handlers

import (
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// pbsSourceKeys are the pbs storage settings a registered PBS server supplies
// when a create names it with pbs_server_id: where Proxmox connects, and the
// token it connects with. The request may set none of them. The stored token
// goes only to the address it was registered with; were the caller to choose
// the server, port or fingerprint, it would go wherever they pointed it — the
// credential-redirect shape credentialRedirected closes on the PBS server's own
// update.
var pbsSourceKeys = []string{"server", "port", "username", "password", "fingerprint"}

// pbsSourceSettings resolves a create's pbs_server_id into the settings the
// server supplies, after refusing a request that contradicts it and
// authorizing the caller on the server.
//
// The refusals come in an order that tells the caller nothing they may not
// know: the request's own shape first, then the server, which answers 404 to a
// caller who may not view it, as a missing one does; then whether they may
// hand its token to Proxmox, which takes manage:pbs — the grant that lets them
// replace that token too — on the cluster the server is attached to.
//
// The token reaches Proxmox through clusterID's API URL, and moving that URL
// takes only a new secret for the cluster's own token, which can be made up: a
// caller who could move it, or register a cluster of their own, could have the
// token sent wherever they liked. So it goes only to the cluster the server is
// attached to, and only while that cluster's URL is the one it had when the
// token was last entered (pbs_servers.token_cluster_api_url, which a move of
// the server clears). It returns that cluster row, which the create must reach
// Proxmox through as it stands — not through the client cache, whose failover
// can pick another member's address, recorded from whatever answered at the
// cluster's URL before it was moved back.
func (h *StorageHandler) pbsSourceSettings(c fiber.Ctx, clusterID, pbsID uuid.UUID, storageType string, form url.Values) (url.Values, db.Cluster, error) {
	if storageType != "pbs" {
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusBadRequest,
			"pbs_server_id applies only to a Proxmox Backup Server storage (type pbs)")
	}
	var named []string
	for _, k := range pbsSourceKeys {
		if form.Has(k) {
			named = append(named, k)
		}
	}
	if len(named) > 0 {
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusBadRequest,
			"With pbs_server_id the registered PBS server supplies the server, port, username, password "+
				"and fingerprint: leave "+strings.Join(named, ", ")+" out of params")
	}

	server, err := h.queries.GetPBSServer(c.Context(), pbsID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, db.Cluster{}, fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}
	if ok, err := pbsServerPerm(c, server, "view"); err != nil {
		return nil, db.Cluster{}, err
	} else if !ok {
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusNotFound, "PBS server not found")
	}
	if ok, err := pbsServerPerm(c, server, "manage"); err != nil {
		return nil, db.Cluster{}, err
	} else if !ok {
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}
	if !server.ClusterID.Valid || uuid.UUID(server.ClusterID.Bytes) != clusterID {
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusBadRequest,
			"A PBS server's own token can be used only for storage on the cluster it is attached to: "+
				"enter another token instead")
	}
	cluster, err := h.queries.GetCluster(c.Context(), clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
	}
	if err != nil {
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}
	if server.TokenClusterApiUrl == "" || server.TokenClusterApiUrl != cluster.ApiUrl {
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusConflict,
			"This cluster's API URL, or the PBS server's cluster, has changed since the PBS server's token "+
				"was entered. Re-enter the token secret on the PBS server under Backup, or enter another token here")
	}

	settings, err := pbsConnectionSettings(server.ApiUrl, server.TlsFingerprint)
	if err != nil {
		return nil, db.Cluster{}, err
	}
	secret, err := crypto.Decrypt(server.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return nil, db.Cluster{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt PBS credentials")
	}
	settings.Set("username", server.TokenID)
	settings.Set("password", secret)
	return settings, cluster, nil
}

// pbsServerPerm reports whether the caller holds action on a registered PBS
// server: on the cluster it is attached to, or instance-wide for a standalone
// one.
func pbsServerPerm(c fiber.Ctx, server db.PbsServer, action string) (bool, error) {
	if server.ClusterID.Valid {
		return hasClusterPerm(c, action, "pbs", uuid.UUID(server.ClusterID.Bytes))
	}
	return hasGlobalPerm(c, action, "pbs")
}

// pbsConnectionSettings is where a pbs storage connects to reach a registered
// PBS server: the host of its API URL, its port unless that is the default,
// and the certificate fingerprint Nexara pinned for it.
//
// A URL naming no port is read as 8007, as storageTie reads one, so a restore
// ties the storage this writes back to the same server by its address. An IPv6
// host goes in bare: Proxmox keeps it so in storage.cfg and adds the brackets
// itself. With no fingerprint pinned, none is sent, and Proxmox checks the
// certificate against its own trusted roots.
func pbsConnectionSettings(apiURL, fingerprint string) (url.Values, error) {
	u, err := url.Parse(apiURL)
	if err != nil || u.Hostname() == "" {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "The PBS server's API URL cannot be read")
	}
	settings := url.Values{}
	settings.Set("server", u.Hostname())
	if port := u.Port(); port != "" && port != strconv.Itoa(defaultPBSPort) {
		settings.Set("port", port)
	}
	if fingerprint != "" {
		pinned, ok := pveFingerprint(fingerprint)
		if !ok {
			return nil, fiber.NewError(fiber.StatusBadRequest,
				"The PBS server's TLS fingerprint is not a SHA-256 fingerprint. Correct it on the server under "+
					"Backup, or enter the connection details yourself")
		}
		settings.Set("fingerprint", pinned)
	}
	return settings, nil
}

// pveFingerprint writes a SHA-256 certificate fingerprint the one way a
// Proxmox pbs storage accepts it: 32 colon-separated hex pairs. Nexara keeps
// the string it was given — the add dialog sends Proxmox's own form, the API
// takes any — and compares with the colons stripped (proxmox.NormalizeFingerprint),
// so a bare digest serves Nexara and would be refused by Proxmox.
func pveFingerprint(fingerprint string) (string, bool) {
	digest := strings.ToUpper(proxmox.NormalizeFingerprint(fingerprint))
	if len(digest) != 64 {
		return "", false
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", false
	}
	pairs := make([]string, 0, 32)
	for i := 0; i < len(digest); i += 2 {
		pairs = append(pairs, digest[i:i+2])
	}
	return strings.Join(pairs, ":"), true
}
