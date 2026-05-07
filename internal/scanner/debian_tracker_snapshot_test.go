package scanner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// minimalTrackerJSON is a stand-in for the Debian Security Tracker JSON
// big enough to trigger the parser branch but small enough to not hammer
// any goroutine.
const minimalTrackerJSON = `{
  "openssl": {
    "CVE-2024-0001": {
      "description": "test",
      "releases": {
        "bookworm": {"status": "open", "fixed_version": "", "urgency": "high"}
      }
    }
  }
}`

// TestCVEClientSnapshot_NetworkOnlyOncePerTTL is the unit-test analogue of
// the plan's "trigger two cluster scans within a minute, confirm only one
// fetch" test. We don't run real cluster scans here — we just exercise the
// snapshot loader directly. It must hit the network on the first call and
// reuse the in-memory cache on the second.
func TestCVEClientSnapshot_NetworkOnlyOncePerTTL(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(minimalTrackerJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewCVEClient(nil, newScannerHTTPClient(5*time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.feedURL = srv.URL
	c.cacheTTL = time.Hour // effectively infinite for this test

	for i := 0; i < 3; i++ {
		got, err := c.snapshot(context.Background())
		if err != nil {
			t.Fatalf("snapshot call %d failed: %v", i, err)
		}
		if _, ok := got["openssl"]; !ok {
			t.Fatalf("call %d: parsed tracker missing openssl entry", i)
		}
	}

	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected exactly 1 network fetch across 3 snapshot calls, got %d", hits)
	}
}

// TestCVEClientSnapshot_ConcurrentFetchesSerialiseToOneRequest exercises the
// mu-locking branch: many goroutines call snapshot at once before any has
// loaded; only the first should hit the network.
func TestCVEClientSnapshot_ConcurrentFetchesSerialiseToOneRequest(t *testing.T) {
	t.Parallel()

	var hits int32
	gate := make(chan struct{}) // released by the test once goroutines have all started
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Block until the test releases gate so multiple goroutines have
		// definitely entered snapshot() before the first fetch returns.
		<-gate
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(minimalTrackerJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewCVEClient(nil, newScannerHTTPClient(5*time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.feedURL = srv.URL
	c.cacheTTL = time.Hour

	var wg sync.WaitGroup
	const goroutines = 8
	wg.Add(goroutines)
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, err := c.snapshot(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	// Give the goroutines a moment to all enter snapshot() and queue on mu.
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent snapshot error: %v", err)
	}

	// Multi-goroutine serialised behaviour: the first goroutine to acquire
	// mu does the fetch; the rest enter, see trackerData != nil, and return.
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected 1 network fetch under concurrent load, got %d", hits)
	}
}

// TestCVEClientSnapshot_StaleProcessCacheRefetches verifies that after the
// in-memory cache ages past cacheTTL, snapshot fetches again (rather than
// silently serving the stale data forever).
func TestCVEClientSnapshot_StaleProcessCacheRefetches(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(minimalTrackerJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewCVEClient(nil, newScannerHTTPClient(5*time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.feedURL = srv.URL
	c.cacheTTL = 10 * time.Millisecond

	if _, err := c.snapshot(context.Background()); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if hits != 1 {
		t.Fatalf("after first call expected hits=1, got %d", hits)
	}
	// Force the in-memory cache stale.
	c.mu.Lock()
	c.trackerLoadedAt = time.Now().Add(-time.Hour)
	c.mu.Unlock()

	if _, err := c.snapshot(context.Background()); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("expected stale TTL to force re-fetch (hits=2), got %d", hits)
	}
}

// TestCVEClientSnapshot_NetworkFailReusesRecentMemory verifies the
// fallback-to-recent-memory path: if the network fetch fails AFTER a
// successful prior load AND the in-memory copy is within 2× cacheTTL,
// the recent snapshot is returned with a warning rather than the whole
// scan failing. Falls through to error otherwise — we deliberately do
// NOT silently serve a stale tracker that may be missing recent CVEs.
func TestCVEClientSnapshot_NetworkFailReusesRecentMemory(t *testing.T) {
	t.Parallel()

	var hits int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(minimalTrackerJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewCVEClient(nil, newScannerHTTPClient(5*time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.feedURL = srv.URL
	c.cacheTTL = time.Hour

	// First call hits the network and populates the in-memory cache.
	if _, err := c.snapshot(context.Background()); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	// Force the in-memory copy to look just past TTL (still < 2× TTL),
	// then make the upstream return 500. Should fall back to memory.
	c.mu.Lock()
	c.trackerLoadedAt = time.Now().Add(-90 * time.Minute)
	c.mu.Unlock()
	fail.Store(true)

	got, err := c.snapshot(context.Background())
	if err != nil {
		t.Fatalf("expected recent-memory fallback to succeed, got %v", err)
	}
	if _, ok := got["openssl"]; !ok {
		t.Fatal("fallback snapshot missing prior data")
	}
}

// TestCVEClientSnapshot_NetworkFailRefusesStaleMemory verifies the
// security-reviewer H2 fix: when the in-memory copy is more than 2×
// cacheTTL old AND the network fails, we surface the error instead of
// silently serving an arbitrarily-old snapshot.
func TestCVEClientSnapshot_NetworkFailRefusesStaleMemory(t *testing.T) {
	t.Parallel()

	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(minimalTrackerJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewCVEClient(nil, newScannerHTTPClient(5*time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.feedURL = srv.URL
	c.cacheTTL = time.Hour

	if _, err := c.snapshot(context.Background()); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}

	// Force the in-memory copy past 2× cacheTTL.
	c.mu.Lock()
	c.trackerLoadedAt = time.Now().Add(-3 * time.Hour)
	c.mu.Unlock()
	fail.Store(true)

	if _, err := c.snapshot(context.Background()); err == nil {
		t.Fatal("expected error when network fails and in-memory copy is too stale")
	}
}

// TestCVEClientSnapshot_RedirectRejected verifies that a redirecting
// upstream is treated as an error (no silent following).
func TestCVEClientSnapshot_RedirectRejected(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://attacker.example/")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	c := NewCVEClient(nil, newScannerHTTPClient(5*time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.feedURL = srv.URL
	c.cacheTTL = time.Hour

	_, err := c.snapshot(context.Background())
	if err == nil {
		t.Fatal("expected error on redirect")
	}
	if !errors.Is(err, errUnexpectedRedirect) {
		t.Fatalf("expected errUnexpectedRedirect, got %v", err)
	}
}

// TestCVEClientSnapshot_BodyTooLargeRejected ensures the size cap is
// enforced — defends against memory exhaustion if the upstream returns
// something huge.
func TestCVEClientSnapshot_BodyTooLargeRejected(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Stream more than maxTrackerSize bytes of zeros. Tests would
		// take seconds to allocate the full 100 MB; we set the client's
		// upper bound separately and assert the limit triggers.
		buf := make([]byte, 1<<20) // 1 MB
		for i := 0; i < 102; i++ {
			if _, err := w.Write(buf); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	c := NewCVEClient(nil, newScannerHTTPClient(60*time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.feedURL = srv.URL
	c.cacheTTL = time.Hour

	_, err := c.snapshot(context.Background())
	if err == nil {
		t.Fatal("expected oversized body to fail")
	}
}
