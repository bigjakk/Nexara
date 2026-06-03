package collector

import (
	"context"
	"fmt"
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
