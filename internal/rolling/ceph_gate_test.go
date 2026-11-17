package rolling

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// cephStub answers the Ceph status with osdsUp OSDs up and the PG states given
// ("active+clean=128,peering=4"), and everything else with a 500. Empty pgs is
// a cluster without Ceph; a "mixed:" prefix is one whose endpoint, and pve-01,
// have none while pve-02 has, so only pve-02's node route answers.
func cephStub(osdsUp int, pgs string) http.HandlerFunc {
	mixed := strings.HasPrefix(pgs, "mixed:")
	pgs = strings.TrimPrefix(pgs, "mixed:")
	parts := strings.Split(pgs, ",")
	states := make([]string, 0, len(parts))
	for _, kv := range parts {
		name, count, _ := strings.Cut(kv, "=")
		states = append(states, fmt.Sprintf(`{"state_name":%q,"count":%s}`, name, count))
	}
	body := fmt.Sprintf(`{"data":{"health":{"status":"HEALTH_WARN"},"pgmap":{"num_pools":2,"pgs_by_state":[%s]},"osdmap":{"num_up_osds":%d}}}`,
		strings.Join(states, ","), osdsUp)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch path := r.URL.Path; {
		case path == "/api2/json/nodes":
			_, _ = w.Write([]byte(`{"data":[{"node":"pve-01","status":"online"},{"node":"pve-02","status":"online"}]}`))
		case pgs != "" && (path == "/api2/json/cluster/ceph/status" && !mixed || path == "/api2/json/nodes/pve-02/ceph/status"):
			_, _ = w.Write([]byte(body))
		case strings.HasSuffix(path, "/ceph/status"):
			http.Error(w, `{"data":null,"message":"binary not installed: /usr/bin/ceph-mon\n"}`, http.StatusInternalServerError)
		default:
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}
}

// TestCephGate_APendingNodeWaitsForCleanCeph: with ceph_wait on, the next
// pending node is not started (drained, or upgraded in place) while PGs are
// degraded, and says why; once Ceph is clean it is, with the OSD baseline taken.
// A compute-only endpoint does not hide a degraded Ceph, and on Ceph the nodes
// go one at a time whatever the parallelism.
func TestCephGate_APendingNodeWaitsForCleanCeph(t *testing.T) {
	t.Parallel()
	const degraded = "active+clean=100,active+undersized+degraded=28"
	for _, tt := range []struct {
		name        string
		pgs         string
		parallelism int32
		active      int64
		wantStarted bool
	}{
		{"degraded", degraded, 1, 0, false},
		{"clean", "active+clean=120,active+clean+scrubbing=8", 1, 0, true},
		{"no Ceph", "", 1, 0, true},
		{"degraded behind a compute-only endpoint", "mixed:" + degraded, 1, 0, false},
		{"parallelism 2, the first node still under way", "active+clean=128", 2, 1, false},
		{"parallelism 2, the first node done", "active+clean=128", 2, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cluster := uuid.New()
			job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: "running", Parallelism: tt.parallelism, CephWait: true}
			// pve-01 is not one of the cluster's nodes, so a started node fails
			// at its first check: the membership lookup is what says it started.
			node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "pending"}
			d := &memberDB{t: t, cluster: cluster,
				rowFor: map[string]any{"CountActiveNodes": struct{ N int64 }{tt.active}, "GetNextPendingNode": node},
				accept: map[string]int64{"SetJobCephPresent": 1, "SetNodeCephWait": 1, "SetJobCephOSDBaseline": 1}}
			o, client, _ := engineOn(t, d, cephStub(3, tt.pgs))
			o.cephConfirm.since[job.ID] = cephRun{time.Now().Add(-15 * time.Second), time.Now().Add(-15 * time.Second)} // A clean reading a tick ago.

			o.startNextNode(context.Background(), client, job)

			if started := len(d.asked) > 0; started != tt.wantStarted {
				t.Errorf("started = %v, want %v (wrote %v)", started, tt.wantStarted, d.writes)
			}
			if tt.pgs == "" && !slices.Contains(d.writes, "SetJobCephPresent") {
				t.Errorf("did not remember the cluster has no Ceph (wrote %v)", d.writes)
			}
			if waited := slices.Contains(d.values, "SetNodeCephWait=28 of 128 placement groups are not active+clean"); waited != strings.HasSuffix(tt.pgs, degraded) {
				t.Errorf("recorded the wait = %v (%v)", waited, d.values)
			}
			if baseline := slices.Contains(d.values, "SetJobCephOSDBaseline=3"); baseline != (tt.wantStarted && tt.pgs != "") {
				t.Errorf("recorded the OSD baseline = %v (%v)", baseline, d.values)
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
			d := &memberDB{t: t, cluster: cluster, members: []string{"pve-01"}, audits: true, jobStatus: "running",
				accept: map[string]int64{"SetNodeCephWait": 1, "ClearNodeCephWait": 1, "SetNodeRestoreStarted": 1,
					"SetNodeRestoreCompleted": 1, "PauseRollingUpdateJobWithReason": 1}}
			o, client, _ := engineOn(t, d, cephStub(tt.osdsUp, "active+clean=128"))
			job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: "running", CephWait: true,
				CephPresent: pgtype.Bool{Bool: true, Valid: true}, CephOsdsBaseline: 3, AutoRestoreGuests: true, ResumedAt: tt.resumed}
			o.cephConfirm.since[job.ID] = cephRun{time.Now().Add(-15 * time.Second), time.Now().Add(-15 * time.Second)} // A clean reading a tick ago.
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

// TestCephGate_PassesOnTwoCleanReadingsATickApart: during a manager failover
// the PG map can read clean on stale data for a few seconds, so a gate passes
// only on a clean reading confirmed by one at least cephConfirmInterval older,
// with nothing else between. "stale" is the live sequence: clean with an OSD
// short, clean, peering, clean, clean; the node is healthy only at the last.
func TestCephGate_PassesOnTwoCleanReadingsATickApart(t *testing.T) {
	t.Parallel()
	const clean, peering = "3/active+clean=97", "3/peering=97"
	for _, tt := range []struct {
		name     string
		readings []string        // "osdsUp/pgs", one a tick (15 s) apart unless gaps says
		gaps     []time.Duration // the time before each reading after the first
		wantAt   int             // the reading the node is called healthy at; -1 never
	}{
		{"a single clean reading", []string{clean}, nil, -1},
		{"a second clean reading a tick later", []string{clean, clean}, nil, 1},
		{"a second clean reading a second later", []string{clean, clean}, []time.Duration{time.Second}, -1},
		{"a clean reading after a long gap", []string{clean, clean, clean}, []time.Duration{5 * time.Minute, 15 * time.Second}, 2},
		{"a not-clean reading in between", []string{clean, peering, clean}, nil, -1},
		{"stale", []string{"2/active+clean=97", clean, peering, clean, clean}, nil, 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var reading atomic.Value
			cluster := uuid.New()
			d := &memberDB{t: t, cluster: cluster, members: []string{"pve-01"},
				accept: map[string]int64{"SetNodeCephWait": 1, "ClearNodeCephWait": 1, "SetNodeRestoreCompleted": 1}}
			o, client, _ := engineOn(t, d, func(w http.ResponseWriter, r *http.Request) {
				up, pgs, _ := strings.Cut(reading.Load().(string), "/")
				n, _ := strconv.Atoi(up)
				cephStub(n, pgs)(w, r)
			})
			var now atomic.Int64
			now.Store(time.Now().UnixNano())
			o.cephConfirm = newCephConfirm(func() time.Time { return time.Unix(0, now.Load()) })
			job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: "running", CephWait: true,
				CephPresent: pgtype.Bool{Bool: true, Valid: true}, CephOsdsBaseline: 3}
			node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "health_check",
				StoppedPassthroughJson: []byte(`[]`)}
			at := -1
			for i, r := range tt.readings {
				if i > 0 {
					gap := 15 * time.Second
					if i-1 < len(tt.gaps) {
						gap = tt.gaps[i-1]
					}
					now.Add(int64(gap))
				}
				reading.Store(r)
				o.advanceNode(context.Background(), client, job, node)
				if at < 0 && slices.Contains(d.writes, "SetNodeRestoreCompleted") {
					at = i
				}
			}
			if at != tt.wantAt {
				t.Errorf("called healthy at reading %d, want %d (wrote %v)", at, tt.wantAt, d.writes)
			}
		})
	}

	t.Run("a node taken down starts over", func(t *testing.T) {
		t.Parallel()
		cluster := uuid.New()
		d := &memberDB{t: t, cluster: cluster, accept: map[string]int64{"SetNodeCephWait": 1, "SetJobCephOSDBaseline": 1}}
		o, client, _ := engineOn(t, d, cephStub(3, "active+clean=97"))
		job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: "running", CephWait: true,
			CephPresent: pgtype.Bool{Bool: true, Valid: true}}
		node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01"}
		o.cephConfirm.since[job.ID] = cephRun{time.Now().Add(-15 * time.Second), time.Now().Add(-15 * time.Second)}
		if !o.cephAllowsStart(context.Background(), client, &job, node) {
			t.Fatal("a confirmed clean Ceph did not let the node start")
		}
		if o.cephAllowsHealthy(context.Background(), client, &job, node) {
			t.Error("the health gate passed on the first reading after a node went down")
		}
	})

	t.Run("the next node starts in the tick the gate passed", func(t *testing.T) {
		t.Parallel()
		cluster := uuid.New()
		d := &memberDB{t: t, cluster: cluster, accept: map[string]int64{"SetJobCephOSDBaseline": 1}}
		o, client, _ := engineOn(t, d, cephStub(3, "active+clean=97"))
		job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: "running", CephWait: true,
			CephPresent: pgtype.Bool{Bool: true, Valid: true}}
		node := db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01"}
		o.cephConfirm.since[job.ID] = cephRun{time.Now().Add(-15 * time.Second), time.Now().Add(-15 * time.Second)}
		if !o.cephAllowsHealthy(context.Background(), client, &job, node) || !o.cephAllowsStart(context.Background(), client, &job, node) {
			t.Error("the health gate and the next start did not both pass in one tick")
		}
	})
}
