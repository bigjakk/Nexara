package collector

import (
	"context"
	"fmt"
	"strings"
	"testing"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// fakeProxmoxClient implements just enough of ProxmoxClient for failover tests:
// only GetNodes is exercised. The embedded interface supplies (nil) stubs for
// every other method, which would panic if unexpectedly called.
type fakeProxmoxClient struct {
	ProxmoxClient
	nodes []proxmox.NodeListEntry
	err   error
}

func (f *fakeProxmoxClient) GetNodes(context.Context) ([]proxmox.NodeListEntry, error) {
	return f.nodes, f.err
}

func TestFailoverCluster(t *testing.T) {
	aliveNodes := []proxmox.NodeListEntry{{Node: "pve3", Status: "online"}}

	t.Run("non-connection error is not retried", func(t *testing.T) {
		var builds int
		s := &Syncer{
			queries:       &mockQueries{nodesByCluster: []db.Node{{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2"}}},
			encryptionKey: testEncryptionKey,
			clientFactory: func(string, string, string, string) (ProxmoxClient, error) {
				builds++
				return &fakeProxmoxClient{nodes: aliveNodes}, nil
			},
			logger: testLogger(),
		}
		cluster := makeCluster(t, "https://10.0.0.1:8006/")
		if _, _, ok := s.failoverCluster(context.Background(), cluster, proxmox.ErrForbidden); ok {
			t.Fatal("expected ok=false for a non-connection error")
		}
		if builds != 0 {
			t.Fatalf("expected no client builds for a non-connection error, got %d", builds)
		}
	})

	t.Run("fails over to first responsive alternate with its own fingerprint", func(t *testing.T) {
		type build struct{ url, fp string }
		var builds []build
		const aliveURL = "https://10.0.0.3:8006"
		s := &Syncer{
			queries: &mockQueries{nodesByCluster: []db.Node{
				{Name: "pve1", Address: "10.0.0.1", SslFingerprint: "FP1"}, // == primary host, must be skipped
				{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2"}, // alternate, down
				{Name: "pve3", Address: "10.0.0.3", SslFingerprint: "FP3"}, // alternate, up
			}},
			encryptionKey: testEncryptionKey,
			clientFactory: func(apiURL, _, _, fingerprint string) (ProxmoxClient, error) {
				builds = append(builds, build{apiURL, fingerprint})
				if apiURL == aliveURL {
					return &fakeProxmoxClient{nodes: aliveNodes}, nil
				}
				return &fakeProxmoxClient{err: proxmox.ErrConnectionFailed}, nil
			},
			logger: testLogger(),
		}
		cluster := makeCluster(t, "https://10.0.0.1:8006/")

		// Pass the connection error wrapped, as SyncCluster would, to exercise errors.Is.
		gotClient, gotNodes, ok := s.failoverCluster(context.Background(), cluster,
			fmt.Errorf("get nodes: %w", proxmox.ErrConnectionFailed))
		if !ok {
			t.Fatal("expected failover to succeed")
		}
		if gotClient == nil || len(gotNodes) != 1 || gotNodes[0].Node != "pve3" {
			t.Fatalf("unexpected failover result: client=%v nodes=%+v", gotClient, gotNodes)
		}
		// pve1 (the primary host) must be skipped; only pve2 and pve3 attempted.
		if len(builds) != 2 {
			t.Fatalf("expected 2 client builds (pve2, pve3), got %d: %+v", len(builds), builds)
		}
		// Each alternate must be pinned to its own node fingerprint, not the cluster's.
		want := map[string]string{"https://10.0.0.2:8006": "FP2", "https://10.0.0.3:8006": "FP3"}
		for _, b := range builds {
			if b.url == "https://10.0.0.1:8006" {
				t.Fatalf("primary host must not be retried: %+v", builds)
			}
			if want[b.url] != b.fp {
				t.Fatalf("endpoint %s used fingerprint %q, want %q", b.url, b.fp, want[b.url])
			}
		}
	})

	t.Run("no responsive alternate returns ok=false", func(t *testing.T) {
		s := &Syncer{
			queries: &mockQueries{nodesByCluster: []db.Node{
				{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2"},
				{Name: "pve3", Address: "10.0.0.3", SslFingerprint: "FP3"},
			}},
			encryptionKey: testEncryptionKey,
			clientFactory: func(string, string, string, string) (ProxmoxClient, error) {
				return &fakeProxmoxClient{err: proxmox.ErrConnectionFailed}, nil
			},
			logger: testLogger(),
		}
		cluster := makeCluster(t, "https://10.0.0.1:8006/")
		if _, _, ok := s.failoverCluster(context.Background(), cluster, proxmox.ErrConnectionFailed); ok {
			t.Fatal("expected ok=false when no alternate responds")
		}
	})

	t.Run("only the primary endpoint known returns ok=false", func(t *testing.T) {
		var builds int
		s := &Syncer{
			queries:       &mockQueries{nodesByCluster: []db.Node{{Name: "pve1", Address: "10.0.0.1", SslFingerprint: "FP1"}}},
			encryptionKey: testEncryptionKey,
			clientFactory: func(string, string, string, string) (ProxmoxClient, error) {
				builds++
				return &fakeProxmoxClient{nodes: aliveNodes}, nil
			},
			logger: testLogger(),
		}
		cluster := makeCluster(t, "https://10.0.0.1:8006/")
		if _, _, ok := s.failoverCluster(context.Background(), cluster, proxmox.ErrConnectionFailed); ok {
			t.Fatal("expected ok=false when only the primary endpoint is known")
		}
		if builds != 0 {
			t.Fatalf("primary host must be skipped without a client build, got %d", builds)
		}
	})

	t.Run("skips alternates with no recorded fingerprint", func(t *testing.T) {
		var builds []string
		s := &Syncer{
			queries: &mockQueries{nodesByCluster: []db.Node{
				{Name: "pve2", Address: "10.0.0.2", SslFingerprint: ""}, // unpinned — must be skipped
				{Name: "pve3", Address: "10.0.0.3", SslFingerprint: "FP3"},
			}},
			encryptionKey: testEncryptionKey,
			clientFactory: func(apiURL, _, _, _ string) (ProxmoxClient, error) {
				builds = append(builds, apiURL)
				return &fakeProxmoxClient{nodes: aliveNodes}, nil
			},
			logger: testLogger(),
		}
		cluster := makeCluster(t, "https://10.0.0.1:8006/")
		if _, _, ok := s.failoverCluster(context.Background(), cluster, proxmox.ErrConnectionFailed); !ok {
			t.Fatal("expected failover to succeed via the fingerprinted node")
		}
		// Only pve3 (which has a fingerprint) may be built; the fingerprint-less
		// pve2 must never be dialed unpinned.
		if len(builds) != 1 || builds[0] != "https://10.0.0.3:8006" {
			t.Fatalf("expected only the fingerprinted node to be built, got %+v", builds)
		}
	})
}

// Failing over is not a silent success. The cluster keeps working — inventory
// and metrics flow through the alternate member, and the client cache routes
// live calls there too — which is exactly why it needs recording: nothing
// looks wrong until the last usable member goes as well. Before this it was
// reported nowhere, since reportSyncError only runs when the whole sync fails
// and failover prevents that.
func TestFailoverClusterReportsToAuditLog(t *testing.T) {
	aliveNodes := []proxmox.NodeListEntry{{Node: "pve3", Status: "online"}}

	newSyncer := func(q *mockQueries) *Syncer {
		return &Syncer{
			queries:       q,
			encryptionKey: testEncryptionKey,
			clientFactory: func(string, string, string, string) (ProxmoxClient, error) {
				return &fakeProxmoxClient{nodes: aliveNodes}, nil
			},
			logger: testLogger(),
		}
	}

	t.Run("a stale certificate is reported as such, not as a generic failure", func(t *testing.T) {
		// The operator's next step differs entirely: a down node resolves
		// itself, a changed certificate needs them to verify and re-pin.
		q := &mockQueries{nodesByCluster: []db.Node{
			{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2"},
		}}
		s := newSyncer(q)
		cluster := makeCluster(t, "https://10.0.0.1:8006/")

		mismatch := fmt.Errorf("%w: proxmox: TLS fingerprint mismatch: got aa, want bb",
			proxmox.ErrConnectionFailed)
		if _, _, ok := s.failoverCluster(context.Background(), cluster, mismatch); !ok {
			t.Fatal("expected failover to succeed")
		}

		if len(q.auditLogs) != 1 {
			t.Fatalf("expected exactly one audit entry, got %d", len(q.auditLogs))
		}
		if got := q.auditLogs[0].Action; got != "tls_fingerprint_mismatch" {
			t.Errorf("action = %q, want %q", got, "tls_fingerprint_mismatch")
		}
	})

	t.Run("an unreachable node is reported as a failover", func(t *testing.T) {
		q := &mockQueries{nodesByCluster: []db.Node{
			{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2"},
		}}
		s := newSyncer(q)
		cluster := makeCluster(t, "https://10.0.0.1:8006/")

		if _, _, ok := s.failoverCluster(context.Background(), cluster, proxmox.ErrConnectionFailed); !ok {
			t.Fatal("expected failover to succeed")
		}

		if len(q.auditLogs) != 1 {
			t.Fatalf("expected exactly one audit entry, got %d", len(q.auditLogs))
		}
		if got := q.auditLogs[0].Action; got != "cluster_endpoint_failover" {
			t.Errorf("action = %q, want %q", got, "cluster_endpoint_failover")
		}
	})

	t.Run("repeat failovers are rate limited", func(t *testing.T) {
		// The collector syncs every few seconds; without this the audit log
		// would gain an entry per sync for as long as the primary is broken.
		q := &mockQueries{nodesByCluster: []db.Node{
			{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2"},
		}}
		s := newSyncer(q)
		cluster := makeCluster(t, "https://10.0.0.1:8006/")

		for range 5 {
			s.failoverCluster(context.Background(), cluster, proxmox.ErrConnectionFailed)
		}

		if len(q.auditLogs) != 1 {
			t.Fatalf("expected 1 audit entry across 5 failovers, got %d", len(q.auditLogs))
		}
	})

	t.Run("a real failure is not swallowed by a preceding failover", func(t *testing.T) {
		// Failover is reported first because it happens in the successful
		// branch. A single shared budget would let the info-level "we routed
		// around it" silence the error-level "the sync failed" arriving
		// moments later — exactly backwards.
		q := &mockQueries{nodesByCluster: []db.Node{
			{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2"},
		}}
		s := newSyncer(q)
		cluster := makeCluster(t, "https://10.0.0.1:8006/")

		s.failoverCluster(context.Background(), cluster, proxmox.ErrConnectionFailed)
		s.reportSyncError(context.Background(), cluster, proxmox.ErrConnectionFailed)

		if len(q.auditLogs) != 2 {
			t.Fatalf("expected both reports, got %d", len(q.auditLogs))
		}
		if q.auditLogs[1].Action != "sync_failed" {
			t.Errorf("second action = %q, want sync_failed", q.auditLogs[1].Action)
		}
	})

	t.Run("repeat failures are still rate limited among themselves", func(t *testing.T) {
		q := &mockQueries{}
		s := newSyncer(q)
		cluster := makeCluster(t, "https://10.0.0.1:8006/")

		for range 4 {
			s.reportSyncError(context.Background(), cluster, proxmox.ErrConnectionFailed)
		}
		if len(q.auditLogs) != 1 {
			t.Fatalf("expected 1 entry across 4 failures, got %d", len(q.auditLogs))
		}
	})
}

// reportEndpointSubstitution covers the case the reactive path stopped seeing.
// Once the client cache learned to pick a healthy endpoint up front, GetNodes
// succeeded on the first try, failoverCluster was never reached, and a cluster
// quietly running on substitutes went unrecorded again — the exact gap the
// reactive report was added to close.
func TestReportEndpointSubstitution(t *testing.T) {
	const pinned = "AA:BB:CC"

	newSyncer := func(q *mockQueries) *Syncer {
		return &Syncer{queries: q, encryptionKey: testEncryptionKey, logger: testLogger()}
	}

	t.Run("says nothing when the configured endpoint is fine", func(t *testing.T) {
		// The failure that would matter most: nagging about a healthy cluster
		// on every sync, five minutes apart, forever.
		q := &mockQueries{nodesByCluster: []db.Node{
			{Name: "pve1", Address: "10.0.0.1", SslFingerprint: pinned, Status: "online"},
			{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2", Status: "online"},
		}}
		s := newSyncer(q)
		cluster := makeCluster(t, "https://10.0.0.1:8006/")
		cluster.TlsFingerprint = pinned

		s.reportEndpointSubstitution(context.Background(), cluster)
		if len(q.auditLogs) != 0 {
			t.Fatalf("reported a healthy cluster: %+v", q.auditLogs)
		}
	})

	t.Run("names a rotated certificate as such", func(t *testing.T) {
		q := &mockQueries{nodesByCluster: []db.Node{
			{Name: "pve1", Address: "10.0.0.1", SslFingerprint: "DIFFERENT", Status: "online"},
			{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2", Status: "online"},
		}}
		s := newSyncer(q)
		cluster := makeCluster(t, "https://10.0.0.1:8006/")
		cluster.TlsFingerprint = pinned

		s.reportEndpointSubstitution(context.Background(), cluster)
		if len(q.auditLogs) != 1 {
			t.Fatalf("expected one entry, got %d", len(q.auditLogs))
		}
		if got := q.auditLogs[0].Action; got != "tls_fingerprint_mismatch" {
			t.Errorf("action = %q, want tls_fingerprint_mismatch", got)
		}
	})

	t.Run("an offline primary is a failover, not a certificate problem", func(t *testing.T) {
		q := &mockQueries{nodesByCluster: []db.Node{
			{Name: "pve1", Address: "10.0.0.1", SslFingerprint: pinned, Status: "offline"},
			{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2", Status: "online"},
		}}
		s := newSyncer(q)
		cluster := makeCluster(t, "https://10.0.0.1:8006/")
		cluster.TlsFingerprint = pinned

		s.reportEndpointSubstitution(context.Background(), cluster)
		if len(q.auditLogs) != 1 {
			t.Fatalf("expected one entry, got %d", len(q.auditLogs))
		}
		if got := q.auditLogs[0].Action; got != "cluster_endpoint_failover" {
			t.Errorf("action = %q, want cluster_endpoint_failover", got)
		}
		if got := q.auditLogs[0].Details; !strings.Contains(string(got), "pve2") {
			t.Errorf("details do not name the substitute member: %s", got)
		}
	})

	t.Run("a proxy-fronted endpoint is not reported", func(t *testing.T) {
		// Mirrors SelectClusterEndpoint's port guard: on a non-8006 URL the
		// node fingerprint describes a different listener, so a mismatch there
		// is expected rather than a problem to announce.
		q := &mockQueries{nodesByCluster: []db.Node{
			{Name: "pve1", Address: "10.0.0.1", SslFingerprint: "DIFFERENT", Status: "online"},
			{Name: "pve2", Address: "10.0.0.2", SslFingerprint: "FP2", Status: "online"},
		}}
		s := newSyncer(q)
		cluster := makeCluster(t, "https://10.0.0.1/")
		cluster.TlsFingerprint = pinned

		s.reportEndpointSubstitution(context.Background(), cluster)
		if len(q.auditLogs) != 0 {
			t.Fatalf("reported a proxy-fronted endpoint: %+v", q.auditLogs)
		}
	})
}
