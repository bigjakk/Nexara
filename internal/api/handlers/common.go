// Package handlers provides HTTP request handlers for the Nexara API.
package handlers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// AuditLog writes an audit log entry and publishes an audit_entry WS event.
// clusterID may be zero-value pgtype.UUID for actions without a cluster context.
// details may be nil (defaults to {}).
//
// When the request was authenticated via API key, the key's id is injected
// into the details JSON as `api_key_id` so an action can be correlated to a
// specific key (not just the owning user).
func AuditLog(c *fiber.Ctx, queries *db.Queries, eventPub *events.Publisher, clusterID pgtype.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return
	}
	if details == nil {
		details = json.RawMessage(`{}`)
	}

	// Inject api_key_id into details for API-key-authed requests so audit
	// rows can be attributed to the specific key, not just the user.
	if apiKeyID, ok := c.Locals("api_key_id").(uuid.UUID); ok {
		var m map[string]any
		if err := json.Unmarshal(details, &m); err == nil {
			if m == nil {
				m = map[string]any{}
			}
			m["api_key_id"] = apiKeyID.String()
			if enriched, err := json.Marshal(m); err == nil {
				details = enriched
			}
		}
	}

	_ = queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    clusterID,
		UserID:       UserUUID(uid),
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      details,
	})

	var cidStr string
	if clusterID.Valid {
		cid, _ := uuid.FromBytes(clusterID.Bytes[:])
		cidStr = cid.String()
	}

	eventPub.ClusterEvent(c.Context(), cidStr, events.KindAuditEntry, resourceType, resourceID, action)

	// Forward to syslog if configured.
	if fwd := eventPub.SyslogForwarder(); fwd != nil {
		fwd.Forward(proxsyslog.Message{
			Timestamp:    time.Now().UTC(),
			UserID:       uid.String(),
			ClusterID:    cidStr,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			Action:       action,
			Details:      string(details),
		})
	}
}

// ClusterUUID is a convenience helper that converts a uuid.UUID to a valid pgtype.UUID.
func ClusterUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// UserUUID wraps a uuid.UUID into a non-null pgtype.UUID for audit_log.user_id.
// The column is nullable (so audit rows can survive user deletion), but inserts
// always carry the user who performed the action.
func UserUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// proxmoxCacheLocalsKey is the fiber Locals key under which the per-server
// *proxmox.ClientCache is exposed to handlers. server.go installs a
// middleware that sets this on every request; CreateProxmoxClient picks
// it up to route through the cache instead of constructing per-call.
const proxmoxCacheLocalsKey = "proxmoxCache"

// SetProxmoxCacheLocal stores the cache pointer on a fiber.Ctx Locals slot.
// Tests and the production middleware both call this to wire the cache in.
func SetProxmoxCacheLocal(c *fiber.Ctx, cache *proxmox.ClientCache) {
	c.Locals(proxmoxCacheLocalsKey, cache)
}

// proxmoxCacheFromCtx retrieves the cache from Locals; returns nil if
// nothing was wired.
func proxmoxCacheFromCtx(c *fiber.Ctx) *proxmox.ClientCache {
	v := c.Locals(proxmoxCacheLocalsKey)
	if v == nil {
		return nil
	}
	cache, _ := v.(*proxmox.ClientCache)
	return cache
}

// CreateProxmoxClient returns a Proxmox API client for the given cluster.
// When a *proxmox.ClientCache has been wired into fiber Locals (via the
// server's middleware), it's used so repeated requests for the same
// cluster reuse the http.Transport's idle connection pool. Falls back
// to a per-call NewClient construction otherwise (tests / unwired paths).
//
// The optional timeout argument overrides the default on the fallback
// path; cached clients always use the cache's internal timeout. ISO
// upload callers (which want a 30-minute floor) hit the multipart
// escape hatch inside the apiClient that already runs without an
// http.Client.Timeout.
func CreateProxmoxClient(c *fiber.Ctx, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, timeout ...time.Duration) (*proxmox.Client, error) {
	if cache := proxmoxCacheFromCtx(c); cache != nil {
		client, err := cache.Get(c.Context(), clusterID)
		if err == nil {
			return client, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		// Decrypt / construction failures surface as 500. The cache
		// already drops failed entries, so retrying after fixing the
		// root cause works without further intervention.
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	cluster, err := queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, encryptionKey)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt cluster credentials")
	}

	t := 30 * time.Second
	if len(timeout) > 0 {
		t = timeout[0]
	}

	pxClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        t,
	})
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	return pxClient, nil
}
