package collector

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestPlanNodeRemovals protects the one automatic delete of node rows: a row
// goes only when two readings in a row that can be trusted both lack its node,
// and never when the reading failed, came back empty or partial, or would leave
// the cluster with no row at all. An offline member is listed, so it stays.
func TestPlanNodeRemovals(t *testing.T) {
	t.Parallel()

	rows := []db.Node{{ID: uuid.New(), Name: "pve-01"}, {ID: uuid.New(), Name: "pve-02"}, {ID: uuid.New(), Name: "pve-03"}}
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
		{name: "lacks every row on record", listed: listed("pve-09"), status: status(1, "pve-09"),
			prev: map[string]bool{"pve-01": true, "pve-02": true, "pve-03": true}, wantSkip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			remove, absent, skip := planNodeRemovals(rows, tc.listed, tc.status, tc.statusErr, tc.prev)
			if (skip != "") != tc.wantSkip {
				t.Fatalf("skip = %q, want skip %v", skip, tc.wantSkip)
			}
			var got []string
			for _, r := range remove {
				got = append(got, r.Name)
			}
			if !slices.Equal(got, tc.wantRemove) {
				t.Errorf("remove = %v, want %v", got, tc.wantRemove)
			}
			var gotAbsent []string
			for n := range absent {
				gotAbsent = append(gotAbsent, n)
			}
			if !slices.Equal(gotAbsent, tc.wantAbsent) {
				t.Errorf("absent = %v, want %v", gotAbsent, tc.wantAbsent)
			}
		})
	}
}

// TestSyncCluster_RemovesANodeTheClusterStopsListing wires the plan into the
// pass: the second pass without pve-02 deletes its row, audits it as a system
// action and publishes the inventory change.
func TestSyncCluster_RemovesANodeTheClusterStopsListing(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.NodeListEntry{{Node: "pve-01", Status: "online"}})
		},
		"/api2/json/cluster/status": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []proxmox.ClusterStatusEntry{
				{Type: "cluster", Name: "cluster01", Quorate: 1, Nodes: 1},
				{Type: "node", Name: "pve-01", Online: 1},
			})
		},
	})
	defer srv.Close()

	mq := newMockQueries()
	pub := &mockEventPub{}
	syncer := newTestSyncer(mq)
	syncer.SetEventPublisher(pub)
	cluster := makeCluster(t, srv.URL)
	gone := db.Node{ID: uuid.New(), ClusterID: cluster.ID, Name: "pve-02", Status: "online"}
	mq.nodes[cluster.ID.String()+":pve-02"] = gone

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
	if pub.countKind(events.KindInventoryChange) == 0 {
		t.Errorf("the removing pass published no inventory_change: %+v", pub.events)
	}
}
