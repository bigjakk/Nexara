package rolling

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// cephStub answers /cluster/ceph/status with osdsUp OSDs up and the PG states
// given ("active+clean=128,peering=4"), or as a node without Ceph does when pgs
// is empty, and everything else with a 500.
func cephStub(osdsUp int, pgs string) http.HandlerFunc {
	parts := strings.Split(pgs, ",")
	states := make([]string, 0, len(parts))
	for _, kv := range parts {
		name, count, _ := strings.Cut(kv, "=")
		states = append(states, fmt.Sprintf(`{"state_name":%q,"count":%s}`, name, count))
	}
	body := fmt.Sprintf(`{"data":{"health":{"status":"HEALTH_WARN"},"pgmap":{"num_pools":2,"pgs_by_state":[%s]},"osdmap":{"num_up_osds":%d}}}`,
		strings.Join(states, ","), osdsUp)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/cluster/ceph/status" && pgs == "" {
			http.Error(w, `{"data":null,"message":"binary not installed: /usr/bin/ceph-mon\n"}`, http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/api2/json/cluster/ceph/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}
}

// TestCephGate_APendingNodeWaitsForCleanCeph: with ceph_wait on, the next
// pending node is not started (drained, or upgraded in place) while PGs are
// degraded, and says why; once Ceph is clean it is, with the OSD baseline taken.
func TestCephGate_APendingNodeWaitsForCleanCeph(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		pgs         string
		wantStarted bool
	}{
		{"degraded", "active+clean=100,active+undersized+degraded=28", false},
		{"clean", "active+clean=120,active+clean+scrubbing=8", true},
		{"no Ceph", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cluster := uuid.New()
			job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: "running", Parallelism: 1, CephWait: true}
			// pve-01 is not one of the cluster's nodes, so a started node fails
			// at its first check: the membership lookup is what says it started.
			node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "pending"}
			d := &memberDB{t: t, cluster: cluster,
				rowFor: map[string]any{"CountActiveNodes": struct{ N int64 }{0}, "GetNextPendingNode": node},
				accept: map[string]int64{"SetJobCephPresent": 1, "SetNodeCephWait": 1, "SetJobCephOSDBaseline": 1}}
			o, client, _ := engineOn(t, d, cephStub(3, tt.pgs))

			o.startNextNode(context.Background(), client, job)

			if started := len(d.asked) > 0; started != tt.wantStarted {
				t.Errorf("started = %v, want %v (wrote %v)", started, tt.wantStarted, d.writes)
			}
			if tt.pgs == "" && !slices.Contains(d.writes, "SetJobCephPresent") {
				t.Errorf("did not remember the cluster has no Ceph (wrote %v)", d.writes)
			}
			if waited := slices.Contains(d.values, "SetNodeCephWait=28 of 128 placement groups are not active+clean"); waited == tt.wantStarted {
				t.Errorf("recorded the wait = %v, want %v (%v)", waited, !tt.wantStarted, d.values)
			}
		})
	}
}

// TestCephGate_ANodeIsNotCalledHealthyUntilCephIsClean: a node back from its
// reboot stays in health_check, its guests not moved back, while its OSD is
// still down; once Ceph is clean its restore starts. A wait over the limit
// pauses the job rather than failing it, and a resume restarts the clock.
func TestCephGate_ANodeIsNotCalledHealthyUntilCephIsClean(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		osdsUp      int
		waitStarted pgtype.Timestamptz
		resumed     pgtype.Timestamptz
		wantRestore bool
		wantPause   bool
	}{
		{"an OSD still down", 2, pgtype.Timestamptz{}, pgtype.Timestamptz{}, false, false},
		{"clean", 3, ago(5 * time.Minute), pgtype.Timestamptz{}, true, false},
		{"waited past the limit", 2, ago(31 * time.Minute), pgtype.Timestamptz{}, false, true},
		{"past the limit but resumed since", 2, ago(31 * time.Minute), ago(time.Minute), false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cluster := uuid.New()
			d := &memberDB{t: t, cluster: cluster, members: []string{"pve-01"}, audits: true,
				accept: map[string]int64{"SetNodeCephWait": 1, "ClearNodeCephWait": 1, "SetNodeRestoreStarted": 1,
					"PauseRollingUpdateJobWithReason": 1}}
			o, client, _ := engineOn(t, d, cephStub(tt.osdsUp, "active+clean=128"))
			job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: "running", CephWait: true,
				CephPresent: pgtype.Bool{Bool: true, Valid: true}, CephOsdsBaseline: 3, AutoRestoreGuests: true, ResumedAt: tt.resumed}
			node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "health_check",
				GuestsJson: []byte(`[{"vmid":101,"type":"qemu"}]`), StoppedPassthroughJson: []byte(`[]`),
				CephWaitStartedAt: tt.waitStarted}

			o.advanceNode(context.Background(), client, job, node)

			restored := slices.Contains(d.writes, "SetNodeRestoreStarted")
			paused := slices.Contains(d.writes, "PauseRollingUpdateJobWithReason")
			if restored != tt.wantRestore || paused != tt.wantPause {
				t.Errorf("restore started = %v, paused = %v; want %v and %v (wrote %v)",
					restored, paused, tt.wantRestore, tt.wantPause, d.writes)
			}
		})
	}
}

// TestAnalyzeCeph_WarnsAndNeverBlocks: the pre-flight's Ceph part says whether
// the cluster runs Ceph and what is not clean, and warns (never errors, so a
// strict job is not refused) for that and for more than one node at a time. A
// cluster without Ceph gets neither.
func TestAnalyzeCeph_WarnsAndNeverBlocks(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		pgs        string
		wantStatus string
		wantTypes  []string
	}{
		{"degraded", "active+clean=100,active+undersized+degraded=28", "not_clean", []string{"ceph_not_clean", "ceph_parallelism"}},
		{"no Ceph", "", "absent", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, client, _ := engineOn(t, &memberDB{t: t}, cephStub(3, tt.pgs))
			got, conflicts := AnalyzeCeph(context.Background(), client, 2)
			var types []string
			for _, c := range conflicts {
				if c.Severity != "warning" {
					t.Errorf("%s is %q; the Ceph part only warns", c.Type, c.Severity)
				}
				types = append(types, c.Type)
			}
			if got.Status != tt.wantStatus || !slices.Equal(types, tt.wantTypes) {
				t.Errorf("status %q with %v, want %q with %v", got.Status, types, tt.wantStatus, tt.wantTypes)
			}
		})
	}
}
