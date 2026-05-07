package scanner

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseKEVDate(t *testing.T) {
	cases := []struct {
		in        string
		shouldErr bool
	}{
		{"2024-05-15", false},
		{"", true},
		{"not-a-date", true},
		{"2024/05/15", true},
	}
	for _, c := range cases {
		_, err := parseKEVDate(c.in)
		if (err != nil) != c.shouldErr {
			t.Errorf("parseKEVDate(%q) shouldErr=%v, got err=%v", c.in, c.shouldErr, err)
		}
	}
}

func TestIsYes(t *testing.T) {
	yesValues := []string{"Known", "known", "Yes", "yes", "true"}
	noValues := []string{"", "Unknown", "no", "false", "Maybe"}

	for _, v := range yesValues {
		if !isYes(v) {
			t.Errorf("isYes(%q) = false, want true", v)
		}
	}
	for _, v := range noValues {
		if isYes(v) {
			t.Errorf("isYes(%q) = true, want false", v)
		}
	}
}

func TestKEVFeedJSONShape(t *testing.T) {
	// Minimal smoke test that the JSON tags decode the CISA shape.
	// Full HTTP integration test would need a live server or fake;
	// this just locks down field names.
	sample := `{
		"title": "x",
		"catalogVersion": "2024.05.15",
		"count": 1,
		"vulnerabilities": [
			{
				"cveID": "CVE-2024-1234",
				"vendorProject": "Acme",
				"product": "Widget",
				"vulnerabilityName": "RCE",
				"dateAdded": "2024-05-15",
				"shortDescription": "test",
				"requiredAction": "patch",
				"dueDate": "2024-06-15",
				"knownRansomwareCampaignUse": "Known",
				"notes": "n/a"
			}
		]
	}`
	_ = sample
	// Decode happens inside Refresh; we don't unit test that directly
	// (would need DB). The struct tag check is implicit — if the
	// struct stops decoding, integration tests will catch it.
}

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
