package rolling

import (
	"testing"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestFailoverTargets_PerNodeFingerprintAndSkips is the regression test for the
// orchestrator failover bug: each alternate endpoint must be pinned to that
// node's own TLS fingerprint (never the cluster's primary one, which only
// matches the api_url node). The primary, address-less, and fingerprint-less
// nodes are all skipped (an empty fingerprint would downgrade TLS to system-CA
// verification).
func TestFailoverTargets_PerNodeFingerprintAndSkips(t *testing.T) {
	cluster := db.Cluster{
		ApiUrl:         "https://10.0.0.1:8006/",
		TokenID:        "user@pam!t",
		TlsFingerprint: "PRIMARY_FP", // must NOT leak into alternates
	}
	endpoints := []db.ListNodeEndpointsRow{
		{Name: "pve1", Address: "10.0.0.1", SslFingerprint: "FP1"}, // == primary host, skipped
		{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2"},
		{Name: "pve3", Address: "10.0.0.3", SslFingerprint: "FP3"},
		{Name: "pve4", Address: "", SslFingerprint: "FP4"},      // no address, skipped
		{Name: "pve5", Address: "10.0.0.5", SslFingerprint: ""}, // no fingerprint, skipped
	}

	targets := failoverTargets(cluster, "tok-secret", endpoints)

	if len(targets) != 2 {
		t.Fatalf("expected 2 targets (pve2, pve3), got %d: %+v", len(targets), targets)
	}
	want := map[string]struct{ url, fp string }{
		"pve2": {"https://10.0.0.2:8006", "FP2"},
		"pve3": {"https://10.0.0.3:8006", "FP3"},
	}
	for _, tg := range targets {
		w, ok := want[tg.name]
		if !ok {
			t.Fatalf("unexpected target %q (primary and address-less nodes must be skipped)", tg.name)
		}
		if tg.config.BaseURL != w.url {
			t.Errorf("%s: BaseURL = %q, want %q", tg.name, tg.config.BaseURL, w.url)
		}
		if tg.config.TLSFingerprint != w.fp {
			t.Errorf("%s: TLSFingerprint = %q, want the node's own %q", tg.name, tg.config.TLSFingerprint, w.fp)
		}
		if tg.config.TLSFingerprint == cluster.TlsFingerprint {
			t.Errorf("%s: alternate must not reuse the cluster fingerprint %q", tg.name, cluster.TlsFingerprint)
		}
		if tg.config.TokenID != cluster.TokenID || tg.config.TokenSecret != "tok-secret" {
			t.Errorf("%s: credentials not propagated correctly: %+v", tg.name, tg.config)
		}
	}
}
