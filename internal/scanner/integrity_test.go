package scanner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKEVPlausibilityChecks_Accepts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	feed := &kevFeed{
		Title:           "CISA Catalog of Known Exploited Vulnerabilities",
		CatalogVersion:  "2026.05.06",
		DateReleased:    "2026-05-06T12:00:00.000Z",
		Count:           3,
		Vulnerabilities: []kevEntry{{CVEID: "CVE-2024-1"}, {CVEID: "CVE-2024-2"}, {CVEID: "CVE-2024-3"}},
	}
	if err := kevPlausibilityChecks(feed, now); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestKEVPlausibilityChecks_AllowsNearbyDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	feed := &kevFeed{
		Title:          "x",
		CatalogVersion: "1",
		// 12h in the future is within the 24h skew tolerance.
		DateReleased:    now.Add(12 * time.Hour).Format(time.RFC3339Nano),
		Count:           1,
		Vulnerabilities: []kevEntry{{CVEID: "CVE-1"}},
	}
	if err := kevPlausibilityChecks(feed, now); err != nil {
		t.Fatalf("expected accept within skew, got %v", err)
	}
}

func TestKEVPlausibilityChecks_RejectsNilFeed(t *testing.T) {
	t.Parallel()
	if err := kevPlausibilityChecks(nil, time.Now()); err == nil || !errors.Is(err, errKEVPlausibility) {
		t.Fatalf("expected errKEVPlausibility, got %v", err)
	}
}

func TestKEVPlausibilityChecks_RejectsEmptyTitle(t *testing.T) {
	t.Parallel()
	feed := &kevFeed{CatalogVersion: "1"}
	if err := kevPlausibilityChecks(feed, time.Now()); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("expected title error, got %v", err)
	}
}

func TestKEVPlausibilityChecks_RejectsEmptyVersion(t *testing.T) {
	t.Parallel()
	feed := &kevFeed{Title: "x"}
	if err := kevPlausibilityChecks(feed, time.Now()); err == nil || !strings.Contains(err.Error(), "catalogVersion") {
		t.Fatalf("expected catalogVersion error, got %v", err)
	}
}

func TestKEVPlausibilityChecks_RejectsFutureDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	feed := &kevFeed{
		Title:          "x",
		CatalogVersion: "1",
		DateReleased:   now.Add(72 * time.Hour).Format(time.RFC3339Nano),
	}
	if err := kevPlausibilityChecks(feed, now); err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("expected future-date error, got %v", err)
	}
}

func TestKEVPlausibilityChecks_AcceptsCountOffByOne(t *testing.T) {
	t.Parallel()
	feed := &kevFeed{
		Title:           "x",
		CatalogVersion:  "1",
		Count:           3,
		Vulnerabilities: []kevEntry{{CVEID: "a"}, {CVEID: "b"}}, // off by 1
	}
	if err := kevPlausibilityChecks(feed, time.Now()); err != nil {
		t.Fatalf("expected accept (off-by-1 in tolerance), got %v", err)
	}
}

func TestKEVPlausibilityChecks_RejectsBigCountMismatch(t *testing.T) {
	t.Parallel()
	feed := &kevFeed{
		Title:           "x",
		CatalogVersion:  "1",
		Count:           100,
		Vulnerabilities: []kevEntry{{CVEID: "only-one"}},
	}
	if err := kevPlausibilityChecks(feed, time.Now()); err == nil || !strings.Contains(err.Error(), "count=") {
		t.Fatalf("expected count-mismatch error, got %v", err)
	}
}

func TestFetchDetachedSignature_404IsNotAnError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	t.Cleanup(srv.Close)

	client := newScannerHTTPClient(5 * time.Second)
	body, ok, err := fetchDetachedSignature(context.Background(), client, srv.URL+"/sig.asc")
	if err != nil {
		t.Fatalf("expected nil error on 404, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false on 404")
	}
	if body != nil {
		t.Fatalf("expected nil body on 404, got %d bytes", len(body))
	}
}

func TestFetchDetachedSignature_EmptyURLIsNoOp(t *testing.T) {
	t.Parallel()
	body, ok, err := fetchDetachedSignature(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("expected nil error for empty URL, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for empty URL")
	}
	if body != nil {
		t.Fatal("expected nil body for empty URL")
	}
}

func TestFetchDetachedSignature_ReturnsBodyOn200(t *testing.T) {
	t.Parallel()
	want := []byte("-----BEGIN PGP SIGNATURE-----\nfake\n-----END PGP SIGNATURE-----\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(want)
	}))
	t.Cleanup(srv.Close)

	client := newScannerHTTPClient(5 * time.Second)
	body, ok, err := fetchDetachedSignature(context.Background(), client, srv.URL+"/sig.asc")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for 200")
	}
	if string(body) != string(want) {
		t.Fatalf("body mismatch: got %q want %q", body, want)
	}
}

// TestKEVClient_RefusesUnverifiedSignature exercises the security-reviewer
// H3 fix: when a signature URL is configured AND returns bytes BUT no
// verifier is wired, KEVClient.Refresh must fail closed rather than
// importing the feed and lying about integrity in the log line.
func TestKEVClient_RefusesUnverifiedSignature(t *testing.T) {
	t.Parallel()

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"title": "x",
			"catalogVersion": "1",
			"count": 0,
			"vulnerabilities": []
		}`))
	}))
	t.Cleanup(feedSrv.Close)

	sigSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("-----BEGIN PGP SIGNATURE-----\nfake\n-----END PGP SIGNATURE-----\n"))
	}))
	t.Cleanup(sigSrv.Close)

	c := NewKEVClient(nil, newScannerHTTPClient(5*time.Second), nil)
	c.feedURL = feedSrv.URL
	c.signatureURL = sigSrv.URL // Operator turned the URL on but no verifier is wired.

	_, err := c.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected Refresh to fail when signature is present but no verifier is wired")
	}
	if !strings.Contains(err.Error(), "no verifier is wired") {
		t.Fatalf("expected 'no verifier is wired' in error, got %v", err)
	}
}

func TestFetchDetachedSignature_RejectsRedirect(t *testing.T) {
	t.Parallel()
	// CheckRedirect=ErrUseLastResponse means a 302 surfaces to the caller
	// with a non-200 status; checkUpstreamStatus inside fetchDetachedSignature
	// classifies it as errUnexpectedRedirect.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://attacker.example/")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	client := newScannerHTTPClient(5 * time.Second)
	_, ok, err := fetchDetachedSignature(context.Background(), client, srv.URL+"/sig.asc")
	if err == nil {
		t.Fatal("expected redirect to fail")
	}
	if !errors.Is(err, errUnexpectedRedirect) {
		t.Fatalf("expected errUnexpectedRedirect, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false on redirect")
	}
}

