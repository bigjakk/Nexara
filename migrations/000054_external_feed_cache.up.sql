-- External-feed cache.
--
-- The CVE scanner pulls three public feeds during every cluster scan: the
-- Debian Security Tracker JSON dump (~80 MB), CISA's Known Exploited
-- Vulnerabilities catalog, and FIRST EPSS scores. KEV and EPSS already
-- persist per-CVE rows in their own tables (kev_cache, epss_cache); the
-- Debian tracker, however, was only ever held in process memory — every
-- scheduler restart re-downloaded the whole dump.
--
-- This table is the single-row-per-source backing store for whole-feed
-- snapshots. Today the Debian tracker is the only cached feed; KEV/EPSS
-- could move here later if it turns out per-CVE upserts are too lossy
-- (e.g. when CISA removes a CVE from KEV).
--
-- Layout:
--   * source       — feed identifier (e.g. "debian_tracker"). Primary key.
--   * body         — gzip-compressed response bytes. The raw JSON is large
--                    (~80 MB) but compresses to single-digit MBs, well
--                    inside Postgres TOAST limits.
--   * content_hash — sha256 of the *uncompressed* body, used as an
--                    integrity sanity check against silent in-DB
--                    corruption and to short-circuit identical-body
--                    upserts at the application layer.
--   * fetched_at   — wall-clock time of the last successful fetch. The
--                    24h TTL is enforced in application code; the DB
--                    only stores the timestamp.
CREATE TABLE IF NOT EXISTS external_feed_cache (
    source       TEXT PRIMARY KEY,
    body         BYTEA NOT NULL,
    content_hash TEXT NOT NULL,
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_external_feed_cache_fetched_at
    ON external_feed_cache(fetched_at DESC);
