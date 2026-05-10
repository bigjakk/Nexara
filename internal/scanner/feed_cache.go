package scanner

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// externalFeedSource identifies an upstream feed cached in
// external_feed_cache. The string is the primary key in the DB; do not
// rename without a migration.
type externalFeedSource string

const (
	feedSourceDebianTracker externalFeedSource = "debian_tracker"
)

// loadFeedCache returns the persisted feed body if it exists and was fetched
// within ttl. Returns hit=false when the row is missing, stale, or
// decompression fails. DB errors other than ErrNoRows are returned so the
// caller can decide whether to fall through to a network fetch (recommended)
// or surface the failure.
//
//nolint:unparam // generic over feed sources by design; only Debian tracker is wired today
func loadFeedCache(ctx context.Context, q feedCacheStore, source externalFeedSource, ttl time.Duration) (body []byte, fetchedAt time.Time, hit bool, err error) {
	row, err := q.GetExternalFeedCache(ctx, string(source))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, time.Time{}, false, nil
		}
		return nil, time.Time{}, false, fmt.Errorf("read %s cache: %w", source, err)
	}
	age := time.Since(row.FetchedAt)
	if age > ttl {
		return nil, row.FetchedAt, false, nil
	}
	body, err = gunzipBytes(row.Body, maxTrackerSize)
	if err != nil {
		// Stored body unreadable — surface the error so the caller can
		// log + delete the corrupt row, then fall through to a fresh fetch.
		return nil, row.FetchedAt, false, fmt.Errorf("decompress %s cache: %w", source, err)
	}
	// Verify the stored content_hash against the freshly-decompressed body.
	// Mismatch indicates silent in-DB corruption; treat as a miss so we
	// re-fetch instead of feeding a tampered tracker into the scan.
	got := sha256Hex(body)
	if row.ContentHash != "" && got != row.ContentHash {
		return nil, row.FetchedAt, false, fmt.Errorf("%s cache: content_hash mismatch (stored=%q computed=%q)", source, row.ContentHash, got)
	}
	return body, row.FetchedAt, true, nil
}

// storeFeedCache compresses and persists the feed body. The caller is
// responsible for already-having-validated the body (parses correctly,
// passes plausibility checks). storeFeedCache errors are returned but not
// fatal — the body is also held in process memory after a successful fetch,
// so a DB write failure only costs us the next-restart cache hit.
//
//nolint:unparam // generic over feed sources by design; only Debian tracker is wired today
func storeFeedCache(ctx context.Context, q feedCacheStore, source externalFeedSource, body []byte) error {
	gz, err := gzipBytes(body)
	if err != nil {
		return fmt.Errorf("gzip %s feed: %w", source, err)
	}
	return q.UpsertExternalFeedCache(ctx, db.UpsertExternalFeedCacheParams{
		Source:      string(source),
		Body:        gz,
		ContentHash: sha256Hex(body),
	})
}

// feedCacheStore is the subset of db.Querier used by the feed cache
// helpers. Carved out so tests can inject a fake without standing up
// Postgres.
type feedCacheStore interface {
	GetExternalFeedCache(ctx context.Context, source string) (db.ExternalFeedCache, error)
	UpsertExternalFeedCache(ctx context.Context, arg db.UpsertExternalFeedCacheParams) error
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// gzipBytes compresses b at the default level. BestSpeed would be enough for
// the Debian tracker (it's mostly repetitive JSON), but the size win at
// default vs. fastest is worth it when the row gets TOAST-stored.
func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// gunzipBytes decompresses b, capping output at maxBytes to defend against
// a maliciously crafted compressed payload that would expand to gigabytes.
//
// Reads up to maxBytes+1 and returns an error if the +1 byte was consumed —
// io.LimitReader silently truncates without returning an error, which would
// pass a truncated body to the caller and (worse) make any downstream
// content_hash check pass against the *truncated* bytes the caller hashes
// later. Reading one extra byte distinguishes "decompressed exactly within
// the limit" from "decompressed AT LEAST limit+1 bytes".
func gunzipBytes(b []byte, maxBytes int64) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > maxBytes {
		return nil, fmt.Errorf("decompressed body exceeds %d bytes (possible zip-bomb)", maxBytes)
	}
	return out, nil
}
