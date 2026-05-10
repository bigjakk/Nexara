package scanner

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// fakeFeedStore is an in-memory implementation of feedCacheStore. Used by
// the cache-helper tests to exercise the load/store behaviour without
// standing up Postgres.
type fakeFeedStore struct {
	mu    sync.Mutex
	rows  map[string]db.ExternalFeedCache
	errOn string // when non-empty, GetExternalFeedCache returns this for matching source
}

func newFakeFeedStore() *fakeFeedStore {
	return &fakeFeedStore{rows: make(map[string]db.ExternalFeedCache)}
}

func (f *fakeFeedStore) GetExternalFeedCache(_ context.Context, source string) (db.ExternalFeedCache, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errOn == source {
		return db.ExternalFeedCache{}, errors.New("fake: forced error")
	}
	row, ok := f.rows[source]
	if !ok {
		return db.ExternalFeedCache{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeFeedStore) UpsertExternalFeedCache(_ context.Context, arg db.UpsertExternalFeedCacheParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[arg.Source] = db.ExternalFeedCache{
		Source:      arg.Source,
		Body:        arg.Body,
		ContentHash: arg.ContentHash,
		FetchedAt:   time.Now(),
	}
	return nil
}

// setFetchedAt rewinds a row's fetched_at so we can simulate a stale entry
// without sleeping.
func (f *fakeFeedStore) setFetchedAt(source string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[source]
	row.FetchedAt = t
	f.rows[source] = row
}

func TestStoreAndLoadFeedCache_RoundTrip(t *testing.T) {
	t.Parallel()
	body := []byte(`{"hello":"world","numbers":[1,2,3,4,5]}`)
	store := newFakeFeedStore()

	if err := storeFeedCache(context.Background(), store, feedSourceDebianTracker, body); err != nil {
		t.Fatalf("storeFeedCache: %v", err)
	}

	got, fetchedAt, hit, err := loadFeedCache(context.Background(), store, feedSourceDebianTracker, time.Hour)
	if err != nil {
		t.Fatalf("loadFeedCache: %v", err)
	}
	if !hit {
		t.Fatal("expected cache hit on freshly-written row")
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body roundtrip mismatch: got %q want %q", got, body)
	}
	if fetchedAt.IsZero() {
		t.Fatal("expected non-zero fetchedAt")
	}
}

func TestLoadFeedCache_StaleReturnsMiss(t *testing.T) {
	t.Parallel()
	store := newFakeFeedStore()
	body := []byte("hello world")
	if err := storeFeedCache(context.Background(), store, feedSourceDebianTracker, body); err != nil {
		t.Fatalf("storeFeedCache: %v", err)
	}
	store.setFetchedAt(string(feedSourceDebianTracker), time.Now().Add(-48*time.Hour))

	_, _, hit, err := loadFeedCache(context.Background(), store, feedSourceDebianTracker, 24*time.Hour)
	if err != nil {
		t.Fatalf("expected nil error on stale row, got %v", err)
	}
	if hit {
		t.Fatal("expected stale row to return hit=false")
	}
}

func TestLoadFeedCache_MissingRow(t *testing.T) {
	t.Parallel()
	store := newFakeFeedStore()
	_, _, hit, err := loadFeedCache(context.Background(), store, feedSourceDebianTracker, time.Hour)
	if err != nil {
		t.Fatalf("expected nil error on ErrNoRows, got %v", err)
	}
	if hit {
		t.Fatal("expected hit=false for missing row")
	}
}

func TestLoadFeedCache_PropagatesNonNoRowsError(t *testing.T) {
	t.Parallel()
	store := newFakeFeedStore()
	store.errOn = string(feedSourceDebianTracker)
	_, _, hit, err := loadFeedCache(context.Background(), store, feedSourceDebianTracker, time.Hour)
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if hit {
		t.Fatal("expected hit=false on error")
	}
}

func TestLoadFeedCache_ContentHashMismatchTreatsAsMiss(t *testing.T) {
	t.Parallel()
	store := newFakeFeedStore()
	body := []byte("legit-body")
	if err := storeFeedCache(context.Background(), store, feedSourceDebianTracker, body); err != nil {
		t.Fatalf("storeFeedCache: %v", err)
	}
	// Tamper with the stored content_hash to simulate silent corruption.
	store.mu.Lock()
	row := store.rows[string(feedSourceDebianTracker)]
	row.ContentHash = "deadbeef"
	store.rows[string(feedSourceDebianTracker)] = row
	store.mu.Unlock()

	_, _, hit, err := loadFeedCache(context.Background(), store, feedSourceDebianTracker, time.Hour)
	if hit {
		t.Fatal("expected hash mismatch to treat row as miss")
	}
	if err == nil || !strings.Contains(err.Error(), "content_hash mismatch") {
		t.Fatalf("expected content_hash mismatch error, got %v", err)
	}
}

func TestLoadFeedCache_CorruptGzip(t *testing.T) {
	t.Parallel()
	store := newFakeFeedStore()
	// Insert a row with garbage in the body field (not valid gzip).
	store.rows[string(feedSourceDebianTracker)] = db.ExternalFeedCache{
		Source:      string(feedSourceDebianTracker),
		Body:        []byte("not gzip"),
		ContentHash: "x",
		FetchedAt:   time.Now(),
	}

	_, _, hit, err := loadFeedCache(context.Background(), store, feedSourceDebianTracker, time.Hour)
	if hit {
		t.Fatal("expected corrupt body to return hit=false")
	}
	if err == nil || !strings.Contains(err.Error(), "decompress") {
		t.Fatalf("expected decompress error, got %v", err)
	}
}

func TestGzipBytes_RoundTrip(t *testing.T) {
	t.Parallel()
	original := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog. ", 200))
	compressed, err := gzipBytes(original)
	if err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}
	if len(compressed) >= len(original) {
		t.Errorf("expected compression to shrink repetitive payload (orig=%d compressed=%d)", len(original), len(compressed))
	}

	// Verify magic header.
	if len(compressed) < 2 || compressed[0] != 0x1f || compressed[1] != 0x8b {
		t.Fatalf("compressed payload missing gzip magic")
	}

	roundtrip, err := gunzipBytes(compressed, int64(len(original)*2))
	if err != nil {
		t.Fatalf("gunzipBytes: %v", err)
	}
	if !bytes.Equal(roundtrip, original) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestGunzipBytes_MaxBytesEnforced(t *testing.T) {
	t.Parallel()
	// Build a payload that decompresses larger than maxBytes. Since the
	// security-reviewer H1 fix, an oversized payload returns an error
	// rather than silently truncating — silent truncation would mean a
	// content_hash check downstream passes against the truncated bytes.
	original := bytes.Repeat([]byte{'A'}, 10_000)
	compressed, err := gzipBytes(original)
	if err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}
	if _, err := gunzipBytes(compressed, 100); err == nil {
		t.Fatal("expected gunzipBytes to error on oversized payload")
	} else if !strings.Contains(err.Error(), "exceeds 100 bytes") {
		t.Fatalf("expected zip-bomb error, got %v", err)
	}
	// At-the-limit output succeeds.
	at := bytes.Repeat([]byte{'A'}, 100)
	atC, err := gzipBytes(at)
	if err != nil {
		t.Fatalf("gzipBytes at-limit: %v", err)
	}
	out, err := gunzipBytes(atC, 100)
	if err != nil {
		t.Fatalf("expected at-limit success, got %v", err)
	}
	if len(out) != 100 {
		t.Fatalf("expected 100 bytes at limit, got %d", len(out))
	}
}

func TestGunzipBytes_InvalidHeader(t *testing.T) {
	t.Parallel()
	if _, err := gunzipBytes([]byte("not gzip"), 1024); err == nil {
		t.Fatal("expected error on non-gzip input")
	}
}

func TestSha256Hex_Stable(t *testing.T) {
	t.Parallel()
	if sha256Hex([]byte("")) != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatal("sha256Hex of empty string changed")
	}
	if sha256Hex([]byte("abc")) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("sha256Hex of \"abc\" changed")
	}
}

