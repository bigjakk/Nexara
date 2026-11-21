package collector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// fakeClock is a clock the test moves.
type fakeClock struct{ ns atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ns.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *fakeClock) now() time.Time      { return time.Unix(0, c.ns.Load()).UTC() }
func (c *fakeClock) add(d time.Duration) { c.ns.Add(int64(d)) }

// A planned cluster shutdown filed a "Sync failed" every five minutes for as
// long as it lasted. The Activity feed now hears of an outage when it starts,
// when it changes and once it has ended for good; the end reads as info.
func TestSyncOutcomes_ReportAnOutageOnceAndItsEnd(t *testing.T) {
	t.Parallel()
	certChanged := fmt.Errorf("%w: proxmox: TLS fingerprint mismatch: got aa, want bb", proxmox.ErrConnectionFailed)
	for _, tc := range []struct {
		name   string
		passes string // f/c: failed (unreachable / on the pinned certificate); x: failed after failing over;
		// d/m: synced through another member (down / certificate changed); o: synced; +: clear long enough
		want []string
	}{
		{"one failed pass is a blip", "fo", nil},
		{"an outage is told once, and its end once it holds", "ffffo+o", []string{"sync_failed", "sync_restored"}},
		{"a recovery that does not hold ends nothing", "ffoffo+o", []string{"sync_failed", "sync_restored"}},
		{"a changed certificate is told at once", "co+o", []string{"tls_fingerprint_mismatch", "sync_restored"}},
		{"a brief failover is not told", "ddo+o", nil},
		{"a failover is told once it lasts, and its end", "d+ddo+o", []string{"cluster_endpoint_failover", "cluster_endpoint_restored"}},
		{"a failover that becomes an outage", "d+dffo+o", []string{"cluster_endpoint_failover", "sync_failed", "sync_restored"}},
		{"a pass that fails after failing over tells the failure alone", "fxxo+o", []string{"sync_failed", "sync_restored"}},
		{"a certificate that then fails the sync is told again", "mco+o",
			[]string{"tls_fingerprint_mismatch", "tls_fingerprint_mismatch", "sync_restored"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := &mockQueries{}
			s := &Syncer{queries: q, logger: testLogger()}
			clock := newFakeClock()
			s.outages.now = clock.now
			cluster := makeCluster(t, "https://192.0.2.10:8006/")
			ctx := context.Background()
			for _, pass := range tc.passes {
				switch pass {
				case 'f':
					s.syncFailed(ctx, cluster, proxmox.ErrConnectionFailed)
				case 'c':
					s.syncFailed(ctx, cluster, certChanged)
				case 'x':
					s.reportEndpointFailover(ctx, cluster, "pve-02", "cluster_endpoint_failover", "configured endpoint is not usable")
					s.syncFailed(ctx, cluster, proxmox.ErrConnectionFailed)
				case 'd', 'm':
					action := map[rune]string{'d': "cluster_endpoint_failover", 'm': "tls_fingerprint_mismatch"}[pass]
					s.reportEndpointFailover(ctx, cluster, "pve-02", action, "configured endpoint is not usable")
					s.syncSucceeded(ctx, cluster)
				case 'o':
					s.syncSucceeded(ctx, cluster)
				case '+':
					clock.add(outageClearAfter)
				}
				clock.add(10 * time.Second)
			}

			got := make([]string, 0, len(q.auditLogs))
			for _, row := range q.auditLogs {
				got = append(got, row.Action)
				if strings.HasSuffix(row.Action, "_restored") && strings.Contains(string(row.Details), `"error"`) {
					t.Errorf("%s carries an error, so the feed counts it as one: %s", row.Action, row.Details)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("told %v, want %v", got, tc.want)
			}
		})
	}
}

// SyncAll hands each pass's outcome to the outage tracker.
func TestSyncAll_TellsAnOutageOnceAndItsEnd(t *testing.T) {
	t.Parallel()
	var up atomic.Bool
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			if !up.Load() {
				http.Error(w, "down", http.StatusServiceUnavailable)
				return
			}
			jsonResponse(w, []proxmox.NodeListEntry{})
		},
	})
	defer srv.Close()
	q := newMockQueries()
	q.clusters = []db.Cluster{makeCluster(t, srv.URL)}
	s := newTestSyncer(q)
	clock := newFakeClock()
	s.outages.now = clock.now
	pass := func() { s.SyncAll(context.Background(), func(*ClusterMetricResult) {}) }

	for range 3 {
		pass()
	}
	up.Store(true)
	pass()
	clock.add(outageClearAfter)
	pass()

	got := make([]string, 0, len(q.auditLogs))
	for _, row := range q.auditLogs {
		got = append(got, row.Action)
	}
	if want := []string{"sync_failed", "sync_restored"}; !slices.Equal(got, want) {
		t.Errorf("told %v, want %v", got, want)
	}
}

// A PBS server that cannot be reached failed each of its reads the same way,
// one warning apiece every pass; two reads in a row that cannot connect now
// end the pass.
func TestSyncPBSServer_EndsAtAnUnreachableServer(t *testing.T) {
	t.Parallel()
	secret, err := crypto.Encrypt("secret-token-value", testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	s := &Syncer{
		queries:       &mockQueries{},
		encryptionKey: testEncryptionKey,
		pbsClientFactory: func(string, string, string, string) (PBSProxmoxClient, error) {
			return &pbsFakeClient{statusErr: proxmox.ErrConnectionFailed, syncErr: proxmox.ErrConnectionFailed}, nil
		},
		logger: testLogger(),
	}
	server := db.PbsServer{ID: uuid.New(), ApiUrl: "https://192.0.2.20:8007", TokenSecretEncrypted: secret}

	if _, err := s.syncPBSServer(context.Background(), server); !errors.Is(err, proxmox.ErrConnectionFailed) {
		t.Errorf("sync = %v, want the server's connection failure", err)
	}
}
