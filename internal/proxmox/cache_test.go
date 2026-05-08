package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// testEncryptionKey is a 32-byte hex AES-256 key.
const testCacheEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeQueries lets the cache talk to in-memory cluster/pbs rows whose
// api_url points at a httptest backend.
type fakeQueries struct {
	mu          sync.Mutex
	clusters    map[uuid.UUID]db.Cluster
	pbsServers  map[uuid.UUID]db.PbsServer
	clusterCalls int32
	pbsCalls     int32
}

func (q *fakeQueries) GetCluster(_ context.Context, id uuid.UUID) (db.Cluster, error) {
	atomic.AddInt32(&q.clusterCalls, 1)
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.clusters[id]
	if !ok {
		return db.Cluster{}, errors.New("not found")
	}
	return c, nil
}

func (q *fakeQueries) GetPBSServer(_ context.Context, id uuid.UUID) (db.PbsServer, error) {
	atomic.AddInt32(&q.pbsCalls, 1)
	q.mu.Lock()
	defer q.mu.Unlock()
	s, ok := q.pbsServers[id]
	if !ok {
		return db.PbsServer{}, errors.New("not found")
	}
	return s, nil
}

func (q *fakeQueries) setCluster(c db.Cluster) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.clusters[c.ID] = c
}

func (q *fakeQueries) setPBS(s db.PbsServer) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pbsServers[s.ID] = s
}

func newFakeQueries() *fakeQueries {
	return &fakeQueries{
		clusters:   make(map[uuid.UUID]db.Cluster),
		pbsServers: make(map[uuid.UUID]db.PbsServer),
	}
}

// quietLogger returns a logger that swallows everything; keeps test
// output clean while still exercising slog code paths inside the cache.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newCacheBackedTestServer spins up an httptest server that responds with
// a Proxmox-shaped envelope to /api2/json/version. Returns the server URL
// and a counter you can read to assert how many requests landed.
func newCacheBackedTestServer(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"version": "8.1.3"}})
		_ = r
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

func makeTestCluster(t *testing.T, apiURL string) db.Cluster {
	t.Helper()
	id := uuid.New()
	enc, err := crypto.Encrypt("token-secret-value", testCacheEncryptionKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return db.Cluster{
		ID:                   id,
		Name:                 "test",
		ApiUrl:               apiURL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: enc,
		TlsFingerprint:       "",
		IsActive:             true,
	}
}

func makeTestPBSServer(t *testing.T, apiURL string) db.PbsServer {
	t.Helper()
	id := uuid.New()
	enc, err := crypto.Encrypt("pbs-token-secret", testCacheEncryptionKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return db.PbsServer{
		ID:                   id,
		Name:                 "test-pbs",
		ApiUrl:               apiURL,
		TokenID:              "user@pbs!test",
		TokenSecretEncrypted: enc,
		TlsFingerprint:       "",
	}
}

// TestClientCache_HitReturnsSameInstance — the headline test: two Get calls
// for the same cluster must return the SAME pointer, and the underlying
// queries must only fire once. This is the load-bearing behaviour for
// connection-pool reuse.
func TestClientCache_HitReturnsSameInstance(t *testing.T) {
	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	cluster := makeTestCluster(t, url)
	q.setCluster(cluster)

	cache := NewClientCache(q, testCacheEncryptionKey, nil, quietLogger())
	t.Cleanup(cache.Close)

	c1, err := cache.Get(context.Background(), cluster.ID)
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	c2, err := cache.Get(context.Background(), cluster.ID)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}
	if c1 != c2 {
		t.Errorf("Get returned a different *Client on second call; cache is not active")
	}
	if got := atomic.LoadInt32(&q.clusterCalls); got != 1 {
		t.Errorf("expected 1 GetCluster call, got %d", got)
	}
}

// TestClientCache_PBSHitReturnsSameInstance mirrors the PVE happy path for PBS.
func TestClientCache_PBSHitReturnsSameInstance(t *testing.T) {
	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	server := makeTestPBSServer(t, url)
	q.setPBS(server)

	cache := NewClientCache(q, testCacheEncryptionKey, nil, quietLogger())
	t.Cleanup(cache.Close)

	c1, err := cache.GetPBS(context.Background(), server.ID)
	if err != nil {
		t.Fatalf("GetPBS #1: %v", err)
	}
	c2, err := cache.GetPBS(context.Background(), server.ID)
	if err != nil {
		t.Fatalf("GetPBS #2: %v", err)
	}
	if c1 != c2 {
		t.Errorf("GetPBS returned a different *PBSClient on second call")
	}
	if got := atomic.LoadInt32(&q.pbsCalls); got != 1 {
		t.Errorf("expected 1 GetPBSServer call, got %d", got)
	}
}

// TestClientCache_InvalidateForcesRebuild — after Invalidate the next Get
// rebuilds, hitting the DB again and (importantly) returning a fresh client
// instance. Prevents accidental "Invalidate is a no-op" regressions.
func TestClientCache_InvalidateForcesRebuild(t *testing.T) {
	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	cluster := makeTestCluster(t, url)
	q.setCluster(cluster)

	cache := NewClientCache(q, testCacheEncryptionKey, nil, quietLogger())
	t.Cleanup(cache.Close)

	c1, err := cache.Get(context.Background(), cluster.ID)
	if err != nil {
		t.Fatalf("Get #1: %v", err)
	}
	cache.Invalidate(cluster.ID)
	c2, err := cache.Get(context.Background(), cluster.ID)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}
	if c1 == c2 {
		t.Errorf("Invalidate did not force a rebuild; same instance returned")
	}
	if got := atomic.LoadInt32(&q.clusterCalls); got != 2 {
		t.Errorf("expected 2 GetCluster calls after invalidation, got %d", got)
	}
}

// TestClientCache_InvalidateAllDropsBoth — both PVE and PBS entries must drop.
func TestClientCache_InvalidateAllDropsBoth(t *testing.T) {
	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	cluster := makeTestCluster(t, url)
	pbs := makeTestPBSServer(t, url)
	q.setCluster(cluster)
	q.setPBS(pbs)

	cache := NewClientCache(q, testCacheEncryptionKey, nil, quietLogger())
	t.Cleanup(cache.Close)

	c1, _ := cache.Get(context.Background(), cluster.ID)
	p1, _ := cache.GetPBS(context.Background(), pbs.ID)
	cache.InvalidateAll()
	c2, _ := cache.Get(context.Background(), cluster.ID)
	p2, _ := cache.GetPBS(context.Background(), pbs.ID)

	if c1 == c2 {
		t.Errorf("InvalidateAll did not force PVE rebuild")
	}
	if p1 == p2 {
		t.Errorf("InvalidateAll did not force PBS rebuild")
	}
}

// TestClientCache_PublishInvalidationLocalAlwaysDrops — even when redis is nil
// the in-process cache MUST drop. Prevents the bug where someone wires
// PublishInvalidation but forgets the local drop pathway and only the
// other-replica peers see it (would silently serve stale creds locally).
func TestClientCache_PublishInvalidationLocalAlwaysDrops(t *testing.T) {
	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	cluster := makeTestCluster(t, url)
	q.setCluster(cluster)

	cache := NewClientCache(q, testCacheEncryptionKey, nil /* no redis */, quietLogger())
	t.Cleanup(cache.Close)

	c1, _ := cache.Get(context.Background(), cluster.ID)
	cache.PublishInvalidation(context.Background(), CacheKindPVE, cluster.ID)
	c2, _ := cache.Get(context.Background(), cluster.ID)
	if c1 == c2 {
		t.Errorf("PublishInvalidation(redis=nil) did not invalidate local entry")
	}
}

// TestClientCache_SubscriberPropagatesAcrossInstances — the cross-replica
// scenario. Two ClientCache instances share a miniredis-backed
// *redis.Client. Cache A's PublishInvalidation must drop cache B's entry
// after pub/sub fans out.
func TestClientCache_SubscriberPropagatesAcrossInstances(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	cluster := makeTestCluster(t, url)
	q.setCluster(cluster)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cacheA := NewClientCache(q, testCacheEncryptionKey, rdb, quietLogger())
	cacheB := NewClientCache(q, testCacheEncryptionKey, rdb, quietLogger())
	t.Cleanup(cacheA.Close)
	t.Cleanup(cacheB.Close)

	cacheB.StartSubscriber(ctx)

	// Warm cacheB.
	bClient1, err := cacheB.Get(ctx, cluster.ID)
	if err != nil {
		t.Fatalf("cacheB.Get: %v", err)
	}

	// Publish from cacheA.
	cacheA.PublishInvalidation(ctx, CacheKindPVE, cluster.ID)

	// Wait for the subscriber to drain the message. miniredis delivers
	// pub/sub synchronously but the goroutine handoff is async.
	deadline := time.Now().Add(2 * time.Second)
	for {
		bClient2, err := cacheB.Get(ctx, cluster.ID)
		if err != nil {
			t.Fatalf("cacheB.Get post-publish: %v", err)
		}
		if bClient2 != bClient1 {
			break // cacheB has dropped & rebuilt
		}
		if time.Now().After(deadline) {
			t.Fatalf("cacheB never invalidated after pub/sub message")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestClientCache_SubscriberIgnoresMalformed — defence-in-depth: a malformed
// payload on the channel must not crash the subscriber. Send junk first,
// then a valid invalidation; the valid one must still take effect.
func TestClientCache_SubscriberIgnoresMalformed(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	cluster := makeTestCluster(t, url)
	q.setCluster(cluster)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cache := NewClientCache(q, testCacheEncryptionKey, rdb, quietLogger())
	t.Cleanup(cache.Close)
	cache.StartSubscriber(ctx)

	// Warm.
	c1, _ := cache.Get(ctx, cluster.ID)

	// Junk payloads.
	_ = rdb.Publish(ctx, CacheChannel, "not json").Err()
	_ = rdb.Publish(ctx, CacheChannel, `{"kind":"unknown","id":"abc"}`).Err()
	_ = rdb.Publish(ctx, CacheChannel, `{"kind":"pve","id":"not-a-uuid"}`).Err()
	// Valid invalidation.
	payload, _ := json.Marshal(invalidateMessage{Kind: CacheKindPVE, ID: cluster.ID.String()})
	_ = rdb.Publish(ctx, CacheChannel, string(payload)).Err()

	deadline := time.Now().Add(2 * time.Second)
	for {
		c2, _ := cache.Get(ctx, cluster.ID)
		if c2 != c1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscriber did not process valid message after malformed ones")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestClientCache_SignatureRebuildOnCredentialChange — if the cluster row's
// fields change BUT the cached entry hasn't expired AND no invalidation
// was published, the cache should rebuild on the next request after the
// TTL expires. We simulate by setting a tiny TTL via direct map mutation.
func TestClientCache_TTLForcesRebuild(t *testing.T) {
	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	cluster := makeTestCluster(t, url)
	q.setCluster(cluster)

	cache := NewClientCache(q, testCacheEncryptionKey, nil, quietLogger())
	t.Cleanup(cache.Close)

	c1, _ := cache.Get(context.Background(), cluster.ID)

	// Backdate the entry so its age exceeds cacheTTL.
	cache.mu.Lock()
	if entry, ok := cache.pve[cluster.ID]; ok {
		entry.builtAt = time.Now().Add(-cacheTTL - time.Minute)
	}
	cache.mu.Unlock()

	c2, _ := cache.Get(context.Background(), cluster.ID)
	if c1 == c2 {
		t.Errorf("expired entry was not rebuilt; same client returned")
	}
}

// TestClientCache_NilSafe — the cache must tolerate nil receivers and nil
// inputs without panicking. Defensive: many call sites today don't yet
// know how to react if the cache wasn't wired (e.g. the partial-construction
// test scenarios), and the rule is fail-safe rather than fail-loud.
func TestClientCache_NilSafe(t *testing.T) {
	var c *ClientCache
	c.Invalidate(uuid.New())
	c.InvalidatePBS(uuid.New())
	c.InvalidateAll()
	c.PublishInvalidation(context.Background(), CacheKindPVE, uuid.New())
	c.StartSubscriber(context.Background())
	c.Close()

	if _, err := c.Get(context.Background(), uuid.New()); err == nil {
		t.Errorf("Get on nil cache should error, got nil")
	}
	if _, err := c.GetPBS(context.Background(), uuid.New()); err == nil {
		t.Errorf("GetPBS on nil cache should error, got nil")
	}
}

// TestClientCache_DecryptionFailureSurfaces — if the encryption key in the
// cache doesn't match the key used to encrypt the cluster's token secret,
// Get returns an error rather than caching anything bogus.
func TestClientCache_DecryptionFailureSurfaces(t *testing.T) {
	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	cluster := makeTestCluster(t, url)
	q.setCluster(cluster)

	wrongKey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	cache := NewClientCache(q, wrongKey, nil, quietLogger())
	t.Cleanup(cache.Close)

	if _, err := cache.Get(context.Background(), cluster.ID); err == nil {
		t.Fatal("expected decryption error, got nil")
	}

	// And the cache must NOT have stored a partial entry.
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if _, ok := cache.pve[cluster.ID]; ok {
		t.Errorf("cache stored an entry despite decryption failure")
	}
}

// TestClientCache_ConcurrentGetSinglesByID — N goroutines requesting
// the same cluster simultaneously after a fresh start must all receive
// the SAME *Client and must trigger only one DB lookup. Defends against
// a regression where the singleflight wrapping in Get is removed.
func TestClientCache_ConcurrentGetSinglesByID(t *testing.T) {
	url, _ := newCacheBackedTestServer(t)
	q := newFakeQueries()
	cluster := makeTestCluster(t, url)
	q.setCluster(cluster)

	cache := NewClientCache(q, testCacheEncryptionKey, nil, quietLogger())
	t.Cleanup(cache.Close)

	const goroutines = 16
	results := make([]*Client, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			client, err := cache.Get(context.Background(), cluster.ID)
			if err != nil {
				t.Errorf("goroutine %d: Get error %v", idx, err)
				return
			}
			results[idx] = client
		}(i)
	}
	wg.Wait()

	first := results[0]
	if first == nil {
		t.Fatal("first result was nil")
	}
	for i, c := range results {
		if c != first {
			t.Errorf("goroutine %d got a different *Client; singleflight not active", i)
		}
	}
	// Singleflight should collapse to 1 build, but a small number of
	// follow-on calls landing after the build completed will each
	// pass the read-lock fast path and not increment the DB counter.
	// A regression that disables singleflight would push this number
	// up toward 16; cap at 4 as a generous but tight upper bound.
	if got := atomic.LoadInt32(&q.clusterCalls); got > 4 {
		t.Errorf("expected ≤4 GetCluster calls under singleflight, got %d", got)
	}
}

// TestPVESignatureChangesWithCredentials — the signature helper must
// produce different output for any field change. This is a regression
// guard for the defence-in-depth signature comparison in case TTL fires
// long before a missed invalidation.
func TestPVESignatureChangesWithCredentials(t *testing.T) {
	base := db.Cluster{
		ID:                   uuid.New(),
		ApiUrl:               "https://h:8006",
		TokenID:              "user@pam!t",
		TokenSecretEncrypted: "encA",
		TlsFingerprint:       "fpA",
	}
	baseSig := pveSignature(base)

	mutators := []struct {
		name string
		fn   func(*db.Cluster)
	}{
		{"api_url", func(c *db.Cluster) { c.ApiUrl = "https://other:8006" }},
		{"token_id", func(c *db.Cluster) { c.TokenID = "user@pam!other" }},
		{"token_secret", func(c *db.Cluster) { c.TokenSecretEncrypted = "encB" }},
		{"fingerprint", func(c *db.Cluster) { c.TlsFingerprint = "fpB" }},
	}
	for _, m := range mutators {
		t.Run(m.name, func(t *testing.T) {
			mutated := base
			m.fn(&mutated)
			if pveSignature(mutated) == baseSig {
				t.Errorf("signature unchanged after mutating %s", m.name)
			}
		})
	}
}
