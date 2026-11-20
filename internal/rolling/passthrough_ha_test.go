package rolling

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

type haCounts struct{ listings, forced atomic.Int32 }

// passthroughPVE fakes a node whose only guest is VM 122. A status action
// answers at once: with the HA task that only queues it (hastop, hastart) when
// ha is set, else with the task that does it. The n-th listing shows the guest
// as status(n); the job reads back as jobStatus.
func passthroughPVE(t *testing.T, ha bool, jobStatus string, status func(n int32) string) (*Orchestrator, *proxmox.Client, *haCounts) {
	t.Helper()
	counts := &haCounts{}
	task := map[string]string{"shutdown": "qmshutdown", "stop": "qmstop", "start": "qmstart"}
	if ha {
		task = map[string]string{"shutdown": "hastop", "stop": "hastop", "start": "hastart"}
	}
	d := &memberDB{t: t, audits: true, jobStatus: jobStatus, accept: map[string]int64{"SetNodeStoppedPassthrough": 1}}
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
	return o, client, counts
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
			o, client, counts := passthroughPVE(t, tc.ha, tc.jobStatus, func(n int32) string {
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
			o, client, _ := passthroughPVE(t, tc.ha, "running", func(int32) string { return "stopped" })
			o.pollEvery, o.settleWait = time.Millisecond, 10*time.Millisecond
			node := db.RollingUpdateNode{ID: uuid.New(), NodeName: "pve-01",
				StoppedPassthroughJson: []byte(`[{"vmid":122,"type":"qemu","passthrough":true}]`)}

			pending, result := o.restartStoppedPassthrough(context.Background(), client, db.RollingUpdateJob{ID: uuid.New()}, node, true, true)

			if pending != tc.wantPending || result != "ok" {
				t.Errorf("pending %d, result %q; want %d pending", pending, result, tc.wantPending)
			}
		})
	}
}
