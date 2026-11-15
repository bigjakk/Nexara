package scanner

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func TestVulnSetSignatureStable(t *testing.T) {
	a := []db.ListVulnsBySSVCInScanRow{
		{CveID: "CVE-2024-3", PackageName: "pkg-c"},
		{CveID: "CVE-2024-1", PackageName: "pkg-a"},
		{CveID: "CVE-2024-2", PackageName: "pkg-b"},
	}
	b := []db.ListVulnsBySSVCInScanRow{
		{CveID: "CVE-2024-1", PackageName: "pkg-a"},
		{CveID: "CVE-2024-2", PackageName: "pkg-b"},
		{CveID: "CVE-2024-3", PackageName: "pkg-c"},
	}
	if vulnSetSignature(a) != vulnSetSignature(b) {
		t.Errorf("signature must be order-independent")
	}
}

func TestVulnSetSignatureChangesOnAdd(t *testing.T) {
	a := []db.ListVulnsBySSVCInScanRow{{CveID: "CVE-2024-1"}}
	b := []db.ListVulnsBySSVCInScanRow{
		{CveID: "CVE-2024-1"},
		{CveID: "CVE-2024-2"},
	}
	if vulnSetSignature(a) == vulnSetSignature(b) {
		t.Errorf("signature must change when set changes")
	}
}

func TestVulnSetSignatureChangesOnRemove(t *testing.T) {
	a := []db.ListVulnsBySSVCInScanRow{
		{CveID: "CVE-2024-1"},
		{CveID: "CVE-2024-2"},
	}
	b := []db.ListVulnsBySSVCInScanRow{{CveID: "CVE-2024-1"}}
	if vulnSetSignature(a) == vulnSetSignature(b) {
		t.Errorf("signature must change when CVE removed")
	}
}

func TestVulnSetSignatureEmpty(t *testing.T) {
	got := vulnSetSignature(nil)
	if got == "" {
		t.Errorf("signature of empty set should not be empty string (would alias with default cfg)")
	}
}

func TestFormatVulnListTruncates(t *testing.T) {
	rows := make([]db.ListVulnsBySSVCInScanRow, 30)
	for i := range rows {
		rows[i] = db.ListVulnsBySSVCInScanRow{
			CveID:       "CVE-2024-" + intToStr(i),
			PackageName: "pkg",
			RiskScore:   5.0,
		}
	}
	out := formatVulnList(rows)
	if !contains(out, "and 10 more") {
		t.Errorf("expected truncation marker, got: %q", out)
	}
}

func TestFormatVulnListEmpty(t *testing.T) {
	if got := formatVulnList(nil); got != "" {
		t.Errorf("empty input should produce empty string, got %q", got)
	}
}

// intToStr is a tiny helper to avoid an extra import in the test file.
func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Each CVE notice has a PagerDuty key of its own: a new CVE opens an incident
// rather than joining the last notice's. Its message lists the CVEs.
func TestCVENoticePayload(t *testing.T) {
	t.Parallel()
	c1, c2 := uuid.New(), uuid.New()
	a := []db.ListVulnsBySSVCInScanRow{{CveID: "CVE-2024-1", PackageName: "pkg-a", RiskScore: 9}}
	b := append(slices.Clone(a), db.ListVulnsBySSVCInScanRow{CveID: "CVE-2024-2", PackageName: "pkg-b", RiskScore: 7})
	key := func(c uuid.UUID, v []db.ListVulnsBySSVCInScanRow) string {
		return cveNoticePayload(c, "cluster01", vulnSetSignature(v), v).DedupKey
	}
	if key(c1, a) == "" || key(c1, a) == key(c1, b) || key(c1, a) == key(c2, a) {
		t.Errorf("keys %q %q %q: want one per cluster and CVE set", key(c1, a), key(c1, b), key(c2, a))
	}
	if msg := cveNoticePayload(c1, "cluster01", "s", b).Message; !strings.Contains(msg, "CVE-2024-1") || !strings.Contains(msg, "CVE-2024-2") {
		t.Errorf("message %q does not list the CVEs", msg)
	}
}
