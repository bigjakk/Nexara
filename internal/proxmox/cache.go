package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// CacheChannel is the Redis pub/sub channel used to fan out cache
// invalidation events across replicas. Subscribers in every replica's
// ClientCache listen on this channel and drop their local entries on
// receipt; publishers (cluster Update/Delete handlers and the PBS
// equivalents) call cache.PublishInvalidation, which performs the
// in-process drop AND emits a Redis message for any peer replicas.
const CacheChannel = "nexara:cache:invalidate"

// cacheTTL bounds the worst-case staleness window if a Redis pub/sub
// invalidation message is dropped (e.g. broker restart between PUBLISH
// and SUBSCRIBE). At expiry the entry is rebuilt from a fresh DB read
// + crypto.Decrypt — a small periodic cost that keeps cached creds
// from drifting indefinitely from authoritative state.
const cacheTTL = 10 * time.Minute

// cachedClientTimeout is the http.Client.Timeout used by every cached
// PVE/PBS client. The per-call ctx deadline is the primary cancellation
// signal in this codebase; this is a safety floor for callers that
// neglected to set one. Generous enough to cover routine API calls
// (storage migration polls, rolling-update upgrades) without being so
// long that a wedged peer holds a pool slot indefinitely.
const cachedClientTimeout = 5 * time.Minute

// CacheKind identifies which sub-cache an invalidation message targets.
type CacheKind string

const (
	CacheKindPVE CacheKind = "pve"
	CacheKindPBS CacheKind = "pbs"
	CacheKindAll CacheKind = "all"
)

// invalidateMessage is the JSON payload published on CacheChannel.
type invalidateMessage struct {
	Kind CacheKind `json:"kind"`
	ID   string    `json:"id,omitempty"` // empty for CacheKindAll
}

// CacheQueries is the narrow surface ClientCache needs from db.Queries.
// Defined as an interface so tests can supply a fake without spinning
// up Postgres.
type CacheQueries interface {
	GetCluster(ctx context.Context, id uuid.UUID) (db.Cluster, error)
	GetPBSServer(ctx context.Context, id uuid.UUID) (db.PbsServer, error)
}

// pveEntry is a cached PVE client with the credential signature we built
// it from and the build timestamp (for TTL checks).
type pveEntry struct {
	client    *Client
	signature string
	builtAt   time.Time
}

// pbsEntry mirrors pveEntry for PBS.
type pbsEntry struct {
	client    *PBSClient
	signature string
	builtAt   time.Time
}

// ClientCache memoises *Client and *PBSClient instances keyed by
// cluster_id / pbs_server_id. Constructing those clients is non-trivial
// (TLS handshake, transport setup, AEAD key schedule); reusing them
// across calls keeps idle connections warm in the http.Transport pool
// and amortises TLS session resumption.
//
// Invalidation is event-driven: cluster credential rotation publishes
// on CacheChannel, every replica's subscriber drops the matching
// entry. As a safety net every entry has a 10-minute TTL so a missed
// invalidation surfaces as a small periodic rebuild rather than a
// permanently-stale cached client.
type ClientCache struct {
	queries CacheQueries
	encKey  string
	redis   *redis.Client // nil-safe — single-replica deployments don't need pub/sub
	logger  *slog.Logger

	mu  sync.RWMutex
	pve map[uuid.UUID]*pveEntry
	pbs map[uuid.UUID]*pbsEntry

	sf singleflight.Group

	subOnce   sync.Once
	closeOnce sync.Once
	closed    chan struct{}
}

// NewClientCache constructs a ClientCache. encryptionKey must be the
// 64-hex-character AES-256 key used to encrypt cluster API tokens.
// Pass nil rdb when running without Redis; the cache still works
// in-process, just without cross-replica invalidation.
func NewClientCache(queries CacheQueries, encryptionKey string, rdb *redis.Client, logger *slog.Logger) *ClientCache {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClientCache{
		queries: queries,
		encKey:  encryptionKey,
		redis:   rdb,
		logger:  logger,
		pve:     make(map[uuid.UUID]*pveEntry),
		pbs:     make(map[uuid.UUID]*pbsEntry),
		closed:  make(chan struct{}),
	}
}

// StartSubscriber begins consuming Redis invalidation messages. It is
// idempotent — repeated calls are no-ops; the first call wins. Pass
// the per-server shutdown context so the goroutine returns cleanly
// on SIGTERM. Safe to call when redis is nil (returns immediately).
//
// Returns once the SUBSCRIBE round-trip has been acknowledged by
// Redis (or has timed out — Redis-unreachable-at-boot mustn't block
// process startup), so a caller that immediately publishes (in tests
// or in the in-process invalidation flow) is guaranteed not to race
// the loop on the happy path. A subscribe-time error is logged and
// the cache stays in in-process-only mode.
func (c *ClientCache) StartSubscriber(ctx context.Context) {
	if c == nil || c.redis == nil {
		return
	}
	c.subOnce.Do(func() {
		// Use a short timeout for the SUBSCRIBE round-trip so a Redis
		// outage at boot doesn't gate process startup. Once the timeout
		// fires we log and return; the cache then runs in-process-only
		// for the lifetime of this process — invalidations from peer
		// replicas won't be observed until restart. A separate
		// healthcheck on Redis surfaces the condition to the operator.
		subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		pubsub := c.redis.Subscribe(subCtx, CacheChannel)
		if _, err := pubsub.Receive(subCtx); err != nil {
			_ = pubsub.Close()
			c.logger.Warn("proxmox cache: subscriber failed to start",
				"channel", CacheChannel, "error", err)
			return
		}
		go c.runSubscriber(ctx, pubsub)
	})
}

// Close stops the subscriber goroutine. Safe to call multiple times.
func (c *ClientCache) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.closed)
	})
}

// Get returns a *Client for the given cluster ID, building from the
// stored credentials on cache miss. The same instance is returned to
// every caller, so callers MUST treat the returned client as shared
// (no per-call state mutation, no calling Close on the underlying
// transport).
func (c *ClientCache) Get(ctx context.Context, clusterID uuid.UUID) (*Client, error) {
	if c == nil {
		return nil, errors.New("proxmox: nil ClientCache")
	}

	c.mu.RLock()
	entry, ok := c.pve[clusterID]
	c.mu.RUnlock()
	if ok && time.Since(entry.builtAt) < cacheTTL {
		return entry.client, nil
	}

	res, err, _ := c.sf.Do("pve:"+clusterID.String(), func() (any, error) {
		// Re-check after acquiring the singleflight slot in case another
		// goroutine completed the build while we were waiting.
		c.mu.RLock()
		entry, ok := c.pve[clusterID]
		c.mu.RUnlock()
		if ok && time.Since(entry.builtAt) < cacheTTL {
			return entry.client, nil
		}

		cluster, err := c.queries.GetCluster(ctx, clusterID)
		if err != nil {
			return nil, fmt.Errorf("get cluster %s: %w", clusterID, err)
		}
		tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, c.encKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt cluster %s credentials: %w", clusterID, err)
		}
		client, err := NewClient(ClientConfig{
			BaseURL:        cluster.ApiUrl,
			TokenID:        cluster.TokenID,
			TokenSecret:    tokenSecret,
			TLSFingerprint: cluster.TlsFingerprint,
			Timeout:        cachedClientTimeout,
		})
		if err != nil {
			return nil, fmt.Errorf("create proxmox client for cluster %s: %w", clusterID, err)
		}

		newEntry := &pveEntry{
			client:    client,
			signature: pveSignature(cluster),
			builtAt:   time.Now(),
		}
		c.mu.Lock()
		// If something else slotted an entry while we were building, prefer
		// keeping ours (we're newer). Drain the prior client's idle conns.
		if prior, ok := c.pve[clusterID]; ok {
			closeIdleClient(prior.client)
		}
		c.pve[clusterID] = newEntry
		c.mu.Unlock()
		return client, nil
	})
	if err != nil {
		return nil, err
	}
	client, ok := res.(*Client)
	if !ok || client == nil {
		return nil, fmt.Errorf("proxmox: cache returned nil/invalid client for cluster %s", clusterID)
	}
	return client, nil
}

// GetPBS returns a *PBSClient for the given PBS server ID. Same caveats
// apply as Get — the returned client is shared.
func (c *ClientCache) GetPBS(ctx context.Context, pbsID uuid.UUID) (*PBSClient, error) {
	if c == nil {
		return nil, errors.New("proxmox: nil ClientCache")
	}

	c.mu.RLock()
	entry, ok := c.pbs[pbsID]
	c.mu.RUnlock()
	if ok && time.Since(entry.builtAt) < cacheTTL {
		return entry.client, nil
	}

	res, err, _ := c.sf.Do("pbs:"+pbsID.String(), func() (any, error) {
		c.mu.RLock()
		entry, ok := c.pbs[pbsID]
		c.mu.RUnlock()
		if ok && time.Since(entry.builtAt) < cacheTTL {
			return entry.client, nil
		}

		server, err := c.queries.GetPBSServer(ctx, pbsID)
		if err != nil {
			return nil, fmt.Errorf("get pbs server %s: %w", pbsID, err)
		}
		tokenSecret, err := crypto.Decrypt(server.TokenSecretEncrypted, c.encKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt pbs server %s credentials: %w", pbsID, err)
		}
		client, err := NewPBSClient(ClientConfig{
			BaseURL:        server.ApiUrl,
			TokenID:        server.TokenID,
			TokenSecret:    tokenSecret,
			TLSFingerprint: server.TlsFingerprint,
			Timeout:        cachedClientTimeout,
		})
		if err != nil {
			return nil, fmt.Errorf("create pbs client for server %s: %w", pbsID, err)
		}

		newEntry := &pbsEntry{
			client:    client,
			signature: pbsSignature(server),
			builtAt:   time.Now(),
		}
		c.mu.Lock()
		if prior, ok := c.pbs[pbsID]; ok {
			closeIdlePBSClient(prior.client)
		}
		c.pbs[pbsID] = newEntry
		c.mu.Unlock()
		return client, nil
	})
	if err != nil {
		return nil, err
	}
	client, ok := res.(*PBSClient)
	if !ok || client == nil {
		return nil, fmt.Errorf("proxmox: cache returned nil/invalid PBS client for server %s", pbsID)
	}
	return client, nil
}

// Invalidate drops the cached PVE client for clusterID. The next Get
// will rebuild from the database. Safe to call even if no entry exists.
func (c *ClientCache) Invalidate(clusterID uuid.UUID) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if entry, ok := c.pve[clusterID]; ok {
		closeIdleClient(entry.client)
		delete(c.pve, clusterID)
	}
	c.mu.Unlock()
}

// InvalidatePBS drops the cached PBS client for pbsID.
func (c *ClientCache) InvalidatePBS(pbsID uuid.UUID) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if entry, ok := c.pbs[pbsID]; ok {
		closeIdlePBSClient(entry.client)
		delete(c.pbs, pbsID)
	}
	c.mu.Unlock()
}

// InvalidateAll drops every cached client. Used on encryption-key
// rotation (currently restart-only; reserved for future runtime
// rotation flows).
func (c *ClientCache) InvalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	for _, entry := range c.pve {
		closeIdleClient(entry.client)
	}
	for _, entry := range c.pbs {
		closeIdlePBSClient(entry.client)
	}
	c.pve = make(map[uuid.UUID]*pveEntry)
	c.pbs = make(map[uuid.UUID]*pbsEntry)
	c.mu.Unlock()
}

// PublishInvalidation drops the local entry AND fans out the event
// to peer replicas via Redis. Callers wire this into cluster/PBS
// Update + Delete handlers immediately after the DB write commits.
//
// Best-effort: if Redis is unavailable, the local invalidation still
// fires and the error is logged. Peer replicas will see the change at
// most cacheTTL later when their local TTL fires.
func (c *ClientCache) PublishInvalidation(ctx context.Context, kind CacheKind, id uuid.UUID) {
	if c == nil {
		return
	}

	switch kind {
	case CacheKindPVE:
		c.Invalidate(id)
	case CacheKindPBS:
		c.InvalidatePBS(id)
	case CacheKindAll:
		c.InvalidateAll()
	}

	if c.redis == nil {
		return
	}
	payload, err := json.Marshal(invalidateMessage{Kind: kind, ID: id.String()})
	if err != nil {
		c.logger.Warn("proxmox cache: failed to marshal invalidation",
			"kind", kind, "id", id, "error", err)
		return
	}
	if err := c.redis.Publish(ctx, CacheChannel, payload).Err(); err != nil {
		c.logger.Warn("proxmox cache: failed to publish invalidation",
			"kind", kind, "id", id, "error", err)
	}
}

// runSubscriber blocks until ctx is done or Close is called. Intended
// to run on its own goroutine. Takes the already-subscribed *PubSub
// from StartSubscriber so the loop entry is deterministic.
func (c *ClientCache) runSubscriber(ctx context.Context, pubsub *redis.PubSub) {
	defer func() { _ = pubsub.Close() }()

	ch := pubsub.Channel()
	c.logger.Info("proxmox cache subscriber started", "channel", CacheChannel)

	for {
		select {
		case <-c.closed:
			return
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			c.handleInvalidation(msg.Payload)
		}
	}
}

// handleInvalidation parses the payload and applies the local drop.
// Unknown kinds and malformed messages are logged and ignored — the
// channel is shared and may grow new event shapes over time.
func (c *ClientCache) handleInvalidation(payload string) {
	var m invalidateMessage
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		c.logger.Warn("proxmox cache: malformed invalidation payload", "error", err)
		return
	}

	switch m.Kind {
	case CacheKindAll:
		c.InvalidateAll()
		return
	case CacheKindPVE, CacheKindPBS:
		// fall through
	default:
		c.logger.Warn("proxmox cache: unknown invalidation kind", "kind", m.Kind)
		return
	}

	id, err := uuid.Parse(m.ID)
	if err != nil {
		c.logger.Warn("proxmox cache: invalid invalidation id", "id", m.ID, "error", err)
		return
	}

	switch m.Kind {
	case CacheKindPVE:
		c.Invalidate(id)
	case CacheKindPBS:
		c.InvalidatePBS(id)
	}
}

// pveSignature hashes the load-bearing credential fields. Two clusters
// with the same row produce the same signature; any change to api_url,
// token_id, encrypted secret, or fingerprint produces a fresh
// signature so the cached client gets rebuilt on the next Get even
// if a pub/sub invalidation got lost.
func pveSignature(cl db.Cluster) string {
	h := sha256.New()
	_, _ = h.Write([]byte(cl.ApiUrl))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(cl.TokenID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(cl.TokenSecretEncrypted))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(cl.TlsFingerprint))
	return hex.EncodeToString(h.Sum(nil))
}

func pbsSignature(srv db.PbsServer) string {
	h := sha256.New()
	_, _ = h.Write([]byte(srv.ApiUrl))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(srv.TokenID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(srv.TokenSecretEncrypted))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(srv.TlsFingerprint))
	return hex.EncodeToString(h.Sum(nil))
}

func closeIdleClient(c *Client) {
	if c == nil || c.apiClient == nil || c.httpClient == nil {
		return
	}
	if t, ok := c.httpClient.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
}

func closeIdlePBSClient(c *PBSClient) {
	if c == nil || c.apiClient == nil || c.httpClient == nil {
		return
	}
	if t, ok := c.httpClient.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
}
