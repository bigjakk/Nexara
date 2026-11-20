package rolling

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

type haCounts struct{ listings, forced atomic.Int32 }

// passthroughPVE fakes a node whose only guest is VM 122. A status action
// answers at once: with the HA task that only queues it (hastop, hastart) when
// ha is set, else with the task that does it. The n-th listing shows the guest
// as status(n); the job reads back as jobStatus, and a node of one job with
// the stopped-passthrough record the last write left ([] before any).
func passthroughPVE(t *testing.T, ha bool, jobStatus string, status func(n int32) string) (*Orchestrator, *proxmox.Client, *haCounts, *memberDB) {
	t.Helper()
	counts := &haCounts{}
	task := map[string]string{"shutdown": "qmshutdown", "stop": "qmstop", "start": "qmstart"}
	if ha {
		task = map[string]string{"shutdown": "hastop", "stop": "hastop", "start": "hastart"}
	}
	d := &memberDB{t: t, audits: true, jobStatus: jobStatus,
		accept: map[string]int64{"SetNodeStoppedPassthrough": 1, "ArmJobCleanupPending": 0}}
	job := uuid.New()
	d.rowFor = map[string]any{"GetRollingUpdateNode": func(args []any) pgx.Row {
		d.mu.Lock()
		defer d.mu.Unlock()
		record := []byte(`[]`)
		for i := len(d.writes) - 1; i >= 0; i-- {
			if d.writes[i] == "SetNodeStoppedPassthrough" {
				record = []byte(d.reasons[i])
				break
			}
		}
		id, _ := args[0].(uuid.UUID)
		return structRow{db.RollingUpdateNode{ID: id, JobID: job, NodeName: "pve-01", StoppedPassthroughJson: record}}
	}}
	o, client, _ := engineOn(t, d, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/qemu/122/status/"):
			action := path[strings.LastIndex(path, "/")+1:]
			if action == "stop" {
				counts.forced.Add(1)
			}
			_, _ = w.Write([]byte(`{"data":"UPID:pve-01:00000001:00000000:68000000:` + task[action] + `:122:root@pam:"}`))
		case strings.Contains(path, "/tasks/"):
			_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
		case path == "/api2/json/nodes/pve-01/qemu":
			_, _ = w.Write([]byte(`{"data":[{"vmid":122,"status":"` + status(counts.listings.Add(1)) + `"}]}`))
		case path == "/api2/json/nodes/pve-01/lxc":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			http.Error(w, "refused", http.StatusInternalServerError)
		}
	})
	return o, client, counts, d
}

// An HA-managed guest's shutdown task (hastop) ends once HA has the request,
// and the guest stops a round later. The drain took the task for the shutdown
// and failed its own empty-node check with the guest still running.
func TestStopPassthroughGuest_WaitsForTheGuestNotTheHATask(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		ha          bool
		jobStatus   string
		stopsAfter  int32 // listings still showing it running; 0: never stops
		settle      time.Duration
		wantForced  bool
		wantFailure bool
		wantHalt    bool
	}{
		{"HA stops it a round after the task", true, "running", 2, time.Minute, false, false, false},
		{"a guest HA never stops fails the node", true, "running", 0, 10 * time.Millisecond, true, true, false},
		{"a cancelled job forces nothing off", true, "cancelled", 0, 10 * time.Millisecond, false, false, true},
		// A completed non-HA task means the guest is off: one running after it
		// was started by someone else and is not forced off.
		{"a non-HA shutdown is taken at its word", false, "running", 0, 10 * time.Millisecond, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o, client, counts, _ := passthroughPVE(t, tc.ha, tc.jobStatus, func(n int32) string {
				if tc.stopsAfter > 0 && n > tc.stopsAfter {
					return "stopped"
				}
				return "running"
			})
			o.pollEvery, o.settleWait = time.Millisecond, tc.settle
			guest := GuestSnapshot{VMID: 122, Type: "qemu", Passthrough: true}

			halt, failure := o.stopPassthroughGuest(context.Background(), client, db.RollingUpdateJob{ID: uuid.New()}, "pve-01", guest)

			if halt != tc.wantHalt || (failure != "") != tc.wantFailure {
				t.Errorf("halt %v, failure %q; want halt %v, a failure %v", halt, failure, tc.wantHalt, tc.wantFailure)
			}
			if got := counts.forced.Load() == 1; got != tc.wantForced {
				t.Errorf("forced stop issued: %v, want %v", got, tc.wantForced)
			}
			if tc.stopsAfter > 0 && counts.listings.Load() <= tc.stopsAfter {
				t.Errorf("returned after %d listings, before the guest had stopped", counts.listings.Load())
			}
			if !tc.ha && counts.listings.Load() != 0 {
				t.Errorf("listed the guest %d times after a non-HA shutdown", counts.listings.Load())
			}
		})
	}
}

// The restore's start of an HA-managed guest is an hastart that only queues
// it. Dropping the guest from the record when that task ended left a start HA
// then failed with nothing to retry it. A non-HA start is taken at its word.
func TestRestartStoppedPassthrough_KeepsAGuestHANeverStarts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		ha          bool
		wantPending int
	}{
		{"HA never starts it", true, 1},
		{"a non-HA start is taken at its word", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o, client, _, _ := passthroughPVE(t, tc.ha, "running", func(int32) string { return "stopped" })
			o.pollEvery, o.settleWait = time.Millisecond, 10*time.Millisecond
			node := db.RollingUpdateNode{ID: uuid.New(), NodeName: "pve-01",
				StoppedPassthroughJson: []byte(`[{"vmid":122,"type":"qemu","passthrough":true}]`)}

			pending, result := o.restartStoppedPassthrough(context.Background(), client, db.RollingUpdateJob{ID: uuid.New()}, node, true, true)

			if len(pending) != tc.wantPending || result != "ok" {
				t.Errorf("pending %d, result %q; want %d pending", len(pending), result, tc.wantPending)
			}
		})
	}
}

// The release drops a guest the drain shut down once it runs again: at once
// when the drain's stop of it is over, whatever ended the job, so a retry need
// not wait out pruneRunningAfter. A stop cut short (the job cancelled while HA
// still ran it), or one issued again, may yet turn it off, so it is kept.
// Recording it re-arms the cleanup sweep, as another node may have ended the job.
func TestStoppedPassthrough_DroppedAtOnceWhenItsStopIsOver(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		ha          bool
		jobStatus   string
		stoppedOnce bool // an earlier stop of it is recorded over
		wantKept    int
	}{
		{"its stop is over", false, "failed", false, 0},
		{"its stop was cut short", true, "cancelled", false, 1},
		{"stopped again and cut short", true, "cancelled", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Running at every look: HA never stops it, and it is back by the release.
			o, client, _, d := passthroughPVE(t, tc.ha, tc.jobStatus, func(int32) string { return "running" })
			o.pollEvery, o.settleWait = time.Millisecond, 10*time.Millisecond
			ctx, job := context.Background(), db.RollingUpdateJob{ID: uuid.New()}
			guest, nodeID := GuestSnapshot{VMID: 122, Type: "qemu", Passthrough: true}, uuid.New()
			if tc.stoppedOnce {
				over := guest
				over.StopFinished = true
				o.persistStoppedPassthrough(ctx, nodeID, []GuestSnapshot{over})
			}

			o.shutDownPassthrough(ctx, client, job, db.RollingUpdateNode{ID: nodeID, NodeName: "pve-01"}, guest)
			node, err := o.queries.GetRollingUpdateNode(ctx, nodeID) // as the drain left it
			if err != nil {
				t.Fatal(err)
			}
			kept, _ := o.restartStoppedPassthrough(ctx, client, job, node, false, false)

			if len(kept) != tc.wantKept {
				t.Errorf("the release kept %d of the record %s, want %d", len(kept), node.StoppedPassthroughJson, tc.wantKept)
			}
			if !slices.Contains(d.values, "ArmJobCleanupPending="+node.JobID.String()) {
				t.Errorf("recording the shutdown did not re-arm the job's cleanup (wrote %v)", d.values)
			}
		})
	}
}

// The release removes from the record only the guests it dropped, from the
// record as it then is: one it keeps (its start just issued), or one the drain
// has recorded since, or stopped again, stays. Writing back what the release
// read erased the last two, leaving them off with nothing to start them again.
func TestStoppedPassthrough_ReleaseRemovesOnlyWhatItDropped(t *testing.T) {
	t.Parallel()
	over := GuestSnapshot{VMID: 122, Type: "qemu", Passthrough: true, StopFinished: true}
	again, other := over, GuestSnapshot{VMID: 123, Type: "qemu", Passthrough: true, StopFinished: true}
	again.StopFinished = false
	for _, tc := range []struct {
		name            string
		status          string          // VM 122's; 123 is never listed, so gone
		read, now, want []GuestSnapshot // the record as the release reads it, writes it, after
	}{
		{"a guest it keeps", "stopped", []GuestSnapshot{over, other}, []GuestSnapshot{over, other}, []GuestSnapshot{over}},
		{"another guest recorded since", "running", []GuestSnapshot{over}, []GuestSnapshot{over, other}, []GuestSnapshot{other}},
		{"the same guest stopped again since", "running", []GuestSnapshot{over}, []GuestSnapshot{again}, []GuestSnapshot{again}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o, client, _, _ := passthroughPVE(t, false, "failed", func(int32) string { return tc.status })
			ctx, nodeID := context.Background(), uuid.New()
			raw, _ := json.Marshal(tc.read)
			read := db.RollingUpdateNode{ID: nodeID, NodeName: "pve-01", StoppedPassthroughJson: raw}
			o.persistStoppedPassthrough(ctx, nodeID, tc.now)

			o.restartStoppedPassthrough(ctx, client, db.RollingUpdateJob{ID: uuid.New()}, read, false, false)

			node, err := o.queries.GetRollingUpdateNode(ctx, nodeID)
			if got := o.stoppedPassthroughFor(node); err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("record after the release %+v (%v), want %+v", got, err, tc.want)
			}
		})
	}
}
