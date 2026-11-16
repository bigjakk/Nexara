package collector

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestPlanNodeRemovals protects the one automatic delete of node rows: a row
// goes only when two readings in a row that can be trusted both lack its node,
// and never when the reading failed, came back empty or partial, names another
// cluster, or keeps no row that was on record while the node was still seen.
// An offline member is listed, so it stays.
func TestPlanNodeRemovals(t *testing.T) {
	t.Parallel()

	created, seen := time.Now().Add(-time.Hour), time.Now()
	rows := make([]db.Node, 0, 3)
	for _, n := range []string{"pve-01", "pve-02", "pve-03"} {
		rows = append(rows, db.Node{ID: uuid.New(), Name: n, CreatedAt: created, LastSeenAt: seen})
	}
	listed := func(names ...string) []proxmox.NodeListEntry {
		out := make([]proxmox.NodeListEntry, 0, len(names))
		for _, n := range names {
			out = append(out, proxmox.NodeListEntry{Node: n, Status: "online"})
		}
		return out
	}
	status := func(quorate int, names ...string) []proxmox.ClusterStatusEntry {
		out := make([]proxmox.ClusterStatusEntry, 0, 1+len(names))
		out = append(out, proxmox.ClusterStatusEntry{Type: "cluster", Name: "cluster01", Quorate: quorate, Nodes: len(names)})
		for _, n := range names {
			out = append(out, proxmox.ClusterStatusEntry{Type: "node", Name: n})
		}
		return out
	}
	offline := listed("pve-01", "pve-02", "pve-03")
	offline[2].Status = "offline"
	twoOfThree := listed("pve-01", "pve-02")
	pending := map[string]bool{"pve-03": true}

	for _, tc := range []struct {
		name       string
		listed     []proxmox.NodeListEntry
		status     []proxmox.ClusterStatusEntry
		statusErr  error
		prev       map[string]bool
		expected   string
		wantRemove []string
		wantAbsent []string
		wantSkip   bool
	}{
		{name: "removed: absent from a second reading in a row", listed: twoOfThree,
			status: status(1, "pve-01", "pve-02"), prev: pending,
			wantRemove: []string{"pve-03"}, wantAbsent: []string{"pve-03"}},
		{name: "absent from one reading only: marked, kept", listed: twoOfThree,
			status: status(1, "pve-01", "pve-02"), wantAbsent: []string{"pve-03"}},
		{name: "offline member: listed, kept", listed: offline,
			status: status(1, "pve-01", "pve-02", "pve-03"), prev: pending},
		{name: "failed cluster status read", listed: twoOfThree, status: status(1, "pve-01", "pve-02"),
			statusErr: errors.New("403"), prev: pending, wantSkip: true},
		{name: "empty node list", listed: nil, status: status(1), prev: pending, wantSkip: true},
		{name: "standalone answer: no cluster entry", listed: listed("pve-01"),
			status: []proxmox.ClusterStatusEntry{{Type: "node", Name: "pve-01", Local: 1}}, prev: pending, wantSkip: true},
		{name: "not quorate", listed: twoOfThree, status: status(0, "pve-01", "pve-02"), prev: pending, wantSkip: true},
		{name: "node list and cluster status disagree", listed: twoOfThree,
			status: status(1, "pve-01", "pve-02", "pve-03"), prev: pending, wantSkip: true},
		// clinfo's cluster node count lags the nodelist mid-change.
		{name: "cluster node count disagrees with the lists", listed: twoOfThree,
			status: []proxmox.ClusterStatusEntry{{Type: "cluster", Name: "cluster01", Quorate: 1, Nodes: 3},
				{Type: "node", Name: "pve-01"}, {Type: "node", Name: "pve-02"}},
			prev: pending, wantSkip: true},
		{name: "lacks every row on record", listed: listed("pve-09"), status: status(1, "pve-09"),
			prev:       map[string]bool{"pve-01": true, "pve-02": true, "pve-03": true},
			wantAbsent: []string{"pve-01", "pve-02", "pve-03"}},
		{name: "names another cluster", listed: twoOfThree, status: status(1, "pve-01", "pve-02"),
			prev: pending, expected: "cluster02", wantSkip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := planNodeRemovals(rows, tc.listed, tc.status, tc.statusErr, tc.prev, tc.expected)
			if (plan.skip != "") != tc.wantSkip {
				t.Fatalf("skip = %q, want skip %v", plan.skip, tc.wantSkip)
			}
			var got []string
			for _, r := range plan.remove {
				got = append(got, r.Name)
			}
			if !slices.Equal(got, tc.wantRemove) {
				t.Errorf("remove = %v, want %v", got, tc.wantRemove)
			}
			if gotAbsent := slices.Sorted(maps.Keys(plan.absent)); !slices.Equal(gotAbsent, tc.wantAbsent) {
				t.Errorf("absent = %v, want %v", gotAbsent, tc.wantAbsent)
			}
		})
	}
}

// fakeMembership answers /nodes and /cluster/status as one quorate cluster
// whose name and members a test can change between passes.
type fakeMembership struct {
	mu      sync.Mutex
	cluster string
	nodes   []string
}

func (f *fakeMembership) set(cluster string, nodes ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cluster, f.nodes = cluster, nodes
}

func (f *fakeMembership) server(t *testing.T) *httptest.Server {
	t.Helper()
	return newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			out := make([]proxmox.NodeListEntry, 0, len(f.nodes))
			for _, n := range f.nodes {
				out = append(out, proxmox.NodeListEntry{Node: n, Status: "online"})
			}
			jsonResponse(w, out)
		},
		"/api2/json/cluster/status": func(w http.ResponseWriter, _ *http.Request) {
			f.mu.Lock()
			defer f.mu.Unlock()
			out := make([]proxmox.ClusterStatusEntry, 0, 1+len(f.nodes))
			out = append(out, proxmox.ClusterStatusEntry{Type: "cluster", Name: f.cluster, Quorate: 1, Nodes: len(f.nodes)})
			for _, n := range f.nodes {
				out = append(out, proxmox.ClusterStatusEntry{Type: "node", Name: n, Online: 1})
			}
			jsonResponse(w, out)
		},
	})
}

// seedNodes records nodes the collector has known for an hour, last seen now.
func seedNodes(mq *mockQueries, clusterID uuid.UUID, names ...string) {
	for _, n := range names {
		mq.nodes[clusterID.String()+":"+n] = db.Node{ID: uuid.New(), ClusterID: clusterID, Name: n, Status: "online",
			CreatedAt: time.Now().Add(-time.Hour), LastSeenAt: time.Now()}
	}
}

// recordingNotifier stands in for the alert engine's NotifyResolved.
type recordingNotifier struct{ got []uuid.UUID }

func (r *recordingNotifier) NotifyResolved(_ context.Context, a db.AlertHistory) {
	r.got = append(r.got, a.ID)
}

// TestSyncCluster_RemovesANodeTheClusterStopsListing wires the plan into the
// pass: the second pass without pve-02 deletes its row, turns off its node
// rules, resolves its active alerts (and only its), audits it as a system
// action with those counts, and publishes the inventory change.
func TestSyncCluster_RemovesANodeTheClusterStopsListing(t *testing.T) {
	t.Parallel()

	fake := &fakeMembership{}
	fake.set("cluster01", "pve-01")
	srv := fake.server(t)
	defer srv.Close()

	mq := newMockQueries()
	pub := &mockEventPub{}
	syncer := newTestSyncer(mq)
	syncer.SetEventPublisher(pub)
	cluster := makeCluster(t, srv.URL)
	seedNodes(mq, cluster.ID, "pve-01", "pve-02")
	goneID, keptID := mq.nodes[cluster.ID.String()+":pve-02"].ID, mq.nodes[cluster.ID.String()+":pve-01"].ID
	on := func(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
	firing, pending, raced, other := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	mq.activeAlerts = []db.AlertHistory{{ID: firing, State: "firing", NodeID: on(goneID)},
		{ID: pending, State: "pending", NodeID: on(goneID)}, {ID: raced, State: "firing", NodeID: on(goneID)},
		{ID: other, State: "firing", NodeID: on(keptID)}}
	// The engine resolved this one first and sent its "resolved": no second.
	mq.resolvedElsewhere = map[uuid.UUID]bool{raced: true}
	mq.enabledNodeRules = map[uuid.UUID]int64{goneID: 2, keptID: 1}
	notifier := &recordingNotifier{}
	syncer.SetAlertResolveNotifier(notifier)

	for pass := 1; pass <= 2; pass++ {
		pub.events = nil
		if _, err := syncer.SyncCluster(context.Background(), cluster); err != nil {
			t.Fatalf("pass %d: SyncCluster: %v", pass, err)
		}
		_, kept := mq.nodes[cluster.ID.String()+":pve-02"]
		if kept != (pass == 1) {
			t.Fatalf("pass %d: pve-02's row kept = %v", pass, kept)
		}
	}

	if len(mq.auditLogs) != 1 {
		t.Fatalf("audit entries = %+v, want one", mq.auditLogs)
	}
	a := mq.auditLogs[0]
	if a.Action != "node_removed" || a.ResourceType != "node" || a.ResourceID != "pve-02" ||
		a.UserID.Bytes != auth.SystemUserID || a.ClusterID.Bytes != cluster.ID {
		t.Errorf("audit entry = %+v, want node_removed of pve-02 by the system user", a)
	}
	var details struct {
		RulesDisabled  int `json:"rules_disabled"`
		AlertsResolved int `json:"alerts_resolved"`
	}
	if err := json.Unmarshal(a.Details, &details); err != nil || details.RulesDisabled != 2 || details.AlertsResolved != 2 {
		t.Errorf("audit details = %s, want rules_disabled 2 and alerts_resolved 2", a.Details)
	}
	if want := []uuid.UUID{firing, pending, raced}; !slices.Equal(mq.autoResolved, want) {
		t.Errorf("resolve attempts %v, want %v", mq.autoResolved, want)
	}
	if want := []uuid.UUID{firing, pending}; !slices.Equal(notifier.got, want) {
		t.Errorf("notified %v, want %v", notifier.got, want)
	}
	if mq.enabledNodeRules[keptID] != 1 {
		t.Errorf("pve-01's rules were turned off")
	}
	if pub.countKind(events.KindAlertStateChange) != 2 {
		t.Errorf("alert_state_change events = %d, want 2", pub.countKind(events.KindAlertStateChange))
	}
	if pub.countKind(events.KindInventoryChange) == 0 {
		t.Errorf("the removing pass published no inventory_change: %+v", pub.events)
	}
}

// TestSyncCluster_KeepsNodesWhenTheURLReachesAnotherCluster: a cluster row whose
// API URL is moved to another cluster keeps its nodes however many passes run,
// whether or not the process saw the old cluster first, and when the two
// clusters share a node name.
func TestSyncCluster_KeepsNodesWhenTheURLReachesAnotherCluster(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		seenFirst bool     // a pass reads the old cluster before the URL moves
		other     []string // the other cluster's nodes
	}{
		{name: "no node in common, first pass after a restart", other: []string{"pve-09"}},
		{name: "shares a node name", seenFirst: true, other: []string{"pve-01", "pve-09"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeMembership{}
			fake.set("cluster01", "pve-01", "pve-02", "pve-03")
			srv := fake.server(t)
			defer srv.Close()

			mq := newMockQueries()
			syncer := newTestSyncer(mq)
			cluster := makeCluster(t, srv.URL)
			seedNodes(mq, cluster.ID, "pve-01", "pve-02", "pve-03")
			if tc.seenFirst {
				if _, err := syncer.SyncCluster(context.Background(), cluster); err != nil {
					t.Fatalf("pass on the old cluster: %v", err)
				}
			}

			fake.set("cluster02", tc.other...)
			for pass := 1; pass <= 4; pass++ {
				if _, err := syncer.SyncCluster(context.Background(), cluster); err != nil {
					t.Fatalf("pass %d: SyncCluster: %v", pass, err)
				}
			}
			for _, n := range []string{"pve-01", "pve-02", "pve-03"} {
				if _, kept := mq.nodes[cluster.ID.String()+":"+n]; !kept {
					t.Errorf("%s's row was removed", n)
				}
			}
			if len(mq.auditLogs) != 0 {
				t.Errorf("audit entries = %+v, want none", mq.auditLogs)
			}
		})
	}
}
