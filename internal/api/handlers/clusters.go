package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// ClusterHandler handles cluster CRUD endpoints.
type ClusterHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewClusterHandler creates a new cluster handler.
func NewClusterHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *ClusterHandler {
	return &ClusterHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
	}
}

func (h *ClusterHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

type createClusterRequest struct {
	Name                string `json:"name"`
	APIURL              string `json:"api_url"`
	TokenID             string `json:"token_id"`
	TokenSecret         string `json:"token_secret"`
	TLSFingerprint      string `json:"tls_fingerprint"`
	SyncIntervalSeconds *int32 `json:"sync_interval_seconds"`
}

type updateClusterRequest struct {
	Name                *string `json:"name"`
	APIURL              *string `json:"api_url"`
	TokenID             *string `json:"token_id"`
	TokenSecret         *string `json:"token_secret"`
	TLSFingerprint      *string `json:"tls_fingerprint"`
	SyncIntervalSeconds *int32  `json:"sync_interval_seconds"`
	IsActive            *bool   `json:"is_active"`
}

type clusterResponse struct {
	ID                  uuid.UUID `json:"id"`
	Name                string    `json:"name"`
	APIURL              string    `json:"api_url"`
	TokenID             string    `json:"token_id"`
	TLSFingerprint      string    `json:"tls_fingerprint"`
	SyncIntervalSeconds int32     `json:"sync_interval_seconds"`
	IsActive            bool      `json:"is_active"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type connectivityResult struct {
	Reachable bool   `json:"reachable"`
	Message   string `json:"message"`
}

type createClusterResponse struct {
	Cluster      clusterResponse    `json:"cluster"`
	Connectivity connectivityResult `json:"connectivity"`
}

type updateClusterResponse struct {
	Cluster      clusterResponse    `json:"cluster"`
	Connectivity connectivityResult `json:"connectivity"`
}

// nodeStatusInfo holds per-cluster node counts for computing cluster status.
type nodeStatusInfo struct {
	Total  int64
	Online int64
}

func computeClusterStatus(c db.Cluster, nsi nodeStatusInfo) string {
	if !c.IsActive {
		return "inactive"
	}
	if nsi.Total == 0 {
		return "unknown"
	}
	if nsi.Online == 0 {
		return "offline"
	}
	if nsi.Online < nsi.Total {
		return "degraded"
	}
	return "online"
}

func toClusterResponse(c db.Cluster, nsi nodeStatusInfo) clusterResponse {
	return clusterResponse{
		ID:                  c.ID,
		Name:                c.Name,
		APIURL:              c.ApiUrl,
		TokenID:             c.TokenID,
		TLSFingerprint:      c.TlsFingerprint,
		SyncIntervalSeconds: c.SyncIntervalSeconds,
		IsActive:            c.IsActive,
		Status:              computeClusterStatus(c, nsi),
		CreatedAt:           c.CreatedAt,
		UpdatedAt:           c.UpdatedAt,
	}
}

// Create handles POST /api/v1/clusters.
func (h *ClusterHandler) Create(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}

	var req createClusterRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Name == "" || req.APIURL == "" || req.TokenID == "" || req.TokenSecret == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name, api_url, token_id, and token_secret are required")
	}

	if len(req.Name) > 255 {
		return fiber.NewError(fiber.StatusBadRequest, "name must be 255 characters or fewer")
	}

	if err := validateURL(req.APIURL); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	syncInterval := int32(30)
	if req.SyncIntervalSeconds != nil {
		if *req.SyncIntervalSeconds < 10 || *req.SyncIntervalSeconds > 86400 {
			return fiber.NewError(fiber.StatusBadRequest, "sync_interval_seconds must be between 10 and 86400")
		}
		syncInterval = *req.SyncIntervalSeconds
	}

	encrypted, err := crypto.Encrypt(req.TokenSecret, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt token secret")
	}

	cluster, err := h.queries.CreateCluster(c.Context(), db.CreateClusterParams{
		Name:                 req.Name,
		ApiUrl:               req.APIURL,
		TokenID:              req.TokenID,
		TokenSecretEncrypted: encrypted,
		TlsFingerprint:       req.TLSFingerprint,
		SyncIntervalSeconds:  syncInterval,
		IsActive:             true,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create cluster")
	}

	details, _ := json.Marshal(map[string]string{"name": cluster.Name})
	h.auditLog(c, cluster.ID, "cluster", cluster.ID.String(), "cluster_created", details)

	testResult := testClusterConnectivity(req.APIURL, req.TokenID, req.TokenSecret, req.TLSFingerprint)

	// Pre-populate node entries with addresses from corosync discovery.
	if testResult.Result.Reachable {
		for _, entry := range testResult.Nodes {
			if entry.Type != "node" || entry.Name == "" {
				continue
			}
			_, nodeErr := h.queries.UpsertNode(c.Context(), db.UpsertNodeParams{
				ClusterID:      cluster.ID,
				Name:           entry.Name,
				Status:         "unknown",
				CpuCount:       0,
				MemTotal:        0,
				DiskTotal:       0,
				PveVersion:     "",
				SslFingerprint: "",
				Uptime:          0,
			})
			if nodeErr == nil && entry.IP != "" {
				_ = h.queries.UpdateNodeAddress(c.Context(), db.UpdateNodeAddressParams{
					ClusterID: cluster.ID,
					Name:      entry.Name,
					Address:   entry.IP,
				})
			}
		}
	}

	return c.Status(fiber.StatusCreated).JSON(createClusterResponse{
		Cluster:      toClusterResponse(cluster, nodeStatusInfo{}),
		Connectivity: testResult.Result,
	})
}

// List handles GET /api/v1/clusters.
func (h *ClusterHandler) List(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
		return err
	}

	clusters, err := h.queries.ListClusters(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list clusters")
	}

	// Build a map of cluster_id → node status counts for computing cluster status.
	nsiMap := make(map[uuid.UUID]nodeStatusInfo)
	if rows, nsErr := h.queries.CountNodeStatusesByCluster(c.Context()); nsErr == nil {
		for _, r := range rows {
			nsiMap[r.ClusterID] = nodeStatusInfo{Total: r.Total, Online: r.Online}
		}
	}

	resp := make([]clusterResponse, len(clusters))
	for i, cl := range clusters {
		resp[i] = toClusterResponse(cl, nsiMap[cl.ID])
	}

	return c.JSON(resp)
}

// Get handles GET /api/v1/clusters/:id.
func (h *ClusterHandler) Get(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cluster"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	cluster, err := h.queries.GetCluster(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	var nsi nodeStatusInfo
	if rows, nsErr := h.queries.CountNodeStatusesByCluster(c.Context()); nsErr == nil {
		for _, r := range rows {
			if r.ClusterID == id {
				nsi = nodeStatusInfo{Total: r.Total, Online: r.Online}
				break
			}
		}
	}

	return c.JSON(toClusterResponse(cluster, nsi))
}

// Update handles PUT /api/v1/clusters/:id.
func (h *ClusterHandler) Update(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	var req updateClusterRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	existing, err := h.queries.GetCluster(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	// Merge changed fields.
	params := db.UpdateClusterParams{
		ID:                   id,
		Name:                 existing.Name,
		ApiUrl:               existing.ApiUrl,
		TokenID:              existing.TokenID,
		TokenSecretEncrypted: existing.TokenSecretEncrypted,
		TlsFingerprint:       existing.TlsFingerprint,
		SyncIntervalSeconds:  existing.SyncIntervalSeconds,
		IsActive:             existing.IsActive,
	}

	if req.Name != nil {
		if len(*req.Name) > 255 {
			return fiber.NewError(fiber.StatusBadRequest, "name must be 255 characters or fewer")
		}
		params.Name = *req.Name
	}
	if req.APIURL != nil {
		if err := validateURL(*req.APIURL); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		params.ApiUrl = *req.APIURL
	}
	if req.TokenID != nil {
		params.TokenID = *req.TokenID
	}
	if req.TokenSecret != nil {
		encrypted, err := crypto.Encrypt(*req.TokenSecret, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt token secret")
		}
		params.TokenSecretEncrypted = encrypted
	}
	if req.TLSFingerprint != nil {
		params.TlsFingerprint = *req.TLSFingerprint
	}
	if req.SyncIntervalSeconds != nil {
		if *req.SyncIntervalSeconds < 10 || *req.SyncIntervalSeconds > 86400 {
			return fiber.NewError(fiber.StatusBadRequest, "sync_interval_seconds must be between 10 and 86400")
		}
		params.SyncIntervalSeconds = *req.SyncIntervalSeconds
	}
	if req.IsActive != nil {
		params.IsActive = *req.IsActive
	}

	cluster, err := h.queries.UpdateCluster(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update cluster")
	}

	updateDetails, _ := json.Marshal(map[string]string{"name": cluster.Name})
	h.auditLog(c, cluster.ID, "cluster", cluster.ID.String(), "cluster_updated", updateDetails)

	// Determine the token secret for connectivity test.
	var tokenSecret string
	if req.TokenSecret != nil {
		tokenSecret = *req.TokenSecret
	} else {
		tokenSecret, _ = crypto.Decrypt(existing.TokenSecretEncrypted, h.encryptionKey)
	}

	connectivity := testClusterConnectivity(params.ApiUrl, params.TokenID, tokenSecret, params.TlsFingerprint)

	var updateNsi nodeStatusInfo
	if rows, nsErr := h.queries.CountNodeStatusesByCluster(c.Context()); nsErr == nil {
		for _, r := range rows {
			if r.ClusterID == cluster.ID {
				updateNsi = nodeStatusInfo{Total: r.Total, Online: r.Online}
				break
			}
		}
	}

	return c.JSON(updateClusterResponse{
		Cluster:      toClusterResponse(cluster, updateNsi),
		Connectivity: connectivity.Result,
	})
}

// Delete handles DELETE /api/v1/clusters/:id.
func (h *ClusterHandler) Delete(c *fiber.Ctx) error {
	if err := requirePerm(c, "delete", "cluster"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster ID")
	}

	// Verify the cluster exists.
	_, err = h.queries.GetCluster(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	h.auditLog(c, id, "cluster", id.String(), "cluster_deleted", nil)

	if err := h.queries.DeleteCluster(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete cluster")
	}

	return c.SendStatus(fiber.StatusNoContent)
}

type connectivityTestResult struct {
	Result connectivityResult
	Nodes  []proxmox.ClusterStatusEntry
}

func testClusterConnectivity(apiURL, tokenID, tokenSecret, tlsFingerprint string) connectivityTestResult {
	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        apiURL,
		TokenID:        tokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: tlsFingerprint,
		Timeout:        10 * time.Second,
	})
	if err != nil {
		return connectivityTestResult{Result: connectivityResult{Reachable: false, Message: "Failed to create client"}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	entries, err := client.GetClusterStatus(ctx)
	if err != nil {
		msg := "Connection failed"
		if errors.Is(err, proxmox.ErrForbidden) {
			msg = "Authentication failed: check token credentials"
		} else if errors.Is(err, proxmox.ErrConnectionFailed) {
			errStr := err.Error()
			if strings.Contains(errStr, "fingerprint mismatch") {
				msg = "TLS certificate has changed. The stored fingerprint no longer matches the server certificate. Please re-fetch the fingerprint."
			} else {
				msg = "Host unreachable or connection refused"
			}
		}
		return connectivityTestResult{Result: connectivityResult{Reachable: false, Message: msg}}
	}

	return connectivityTestResult{
		Result: connectivityResult{Reachable: true, Message: "Successfully connected to cluster"},
		Nodes:  entries,
	}
}

// requireAdmin checks that the request was made by an admin user.
func requireAdmin(c *fiber.Ctx) error {
	role, _ := c.Locals("role").(string)
	if role != "admin" {
		return fiber.NewError(fiber.StatusForbidden, "Admin access required")
	}
	return nil
}

type fetchFingerprintRequest struct {
	APIURL string `json:"api_url"`
}

type fetchFingerprintResponse struct {
	Fingerprint string `json:"fingerprint"`
	SelfSigned  bool   `json:"self_signed"`
}

// FetchFingerprint handles POST /api/v1/clusters/fetch-fingerprint.
// It connects to the Proxmox host, retrieves the TLS certificate, and returns the SHA-256 fingerprint.
func (h *ClusterHandler) FetchFingerprint(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cluster"); err != nil {
		return err
	}

	var req fetchFingerprintRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}
	if req.APIURL == "" {
		return fiber.NewError(fiber.StatusBadRequest, "api_url is required")
	}
	if err := validateURL(req.APIURL); err != nil {
		return err
	}

	u, _ := url.Parse(req.APIURL)
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	// Connect with InsecureSkipVerify to get the certificate regardless of CA trust.
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Intentional: we're fetching the fingerprint for user verification
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusBadGateway, fmt.Sprintf("Failed to connect to %s: %s", u.Host, err.Error()))
	}
	defer func() { _ = conn.Close() }()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fiber.NewError(fiber.StatusBadGateway, "Server presented no TLS certificates")
	}

	// SHA-256 fingerprint of the leaf certificate in colon-separated hex format.
	sum := sha256.Sum256(certs[0].Raw)
	hexStr := hex.EncodeToString(sum[:])
	var parts []string
	for i := 0; i < len(hexStr); i += 2 {
		parts = append(parts, strings.ToUpper(hexStr[i:i+2]))
	}
	fingerprint := strings.Join(parts, ":")

	// Check if the cert is trusted by the system CA pool.
	// Proxmox typically uses an internal CA (not self-signed leaf, but still untrusted).
	systemPool, _ := x509.SystemCertPool()
	untrusted := true
	if systemPool != nil {
		_, verifyErr := certs[0].Verify(x509.VerifyOptions{
			Roots: systemPool,
		})
		untrusted = verifyErr != nil
	}

	return c.JSON(fetchFingerprintResponse{
		Fingerprint: fingerprint,
		SelfSigned:  untrusted,
	})
}

// validateURL checks that a string is a valid HTTPS URL with scheme and host.
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid URL format")
	}
	if u.Scheme == "" || u.Host == "" {
		return fiber.NewError(fiber.StatusBadRequest, "URL must include scheme and host")
	}
	if u.Scheme != "https" {
		return fiber.NewError(fiber.StatusBadRequest, "URL must use HTTPS scheme")
	}
	if u.User != nil {
		return fiber.NewError(fiber.StatusBadRequest, "URL must not contain credentials")
	}
	return nil
}
