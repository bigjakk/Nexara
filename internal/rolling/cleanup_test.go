package rolling

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/proxmox"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func TestCleanupBackoff(t *testing.T) {
	tests := []struct {
		attempts int32
		want     time.Duration
	}{
		{0, 2 * time.Minute},
		{4, 2 * time.Minute},
		{5, 10 * time.Minute},
		{9, 10 * time.Minute},
		{10, time.Hour},
		{100, time.Hour},
	}
	for _, tt := range tests {
		if got := cleanupBackoff(tt.attempts); got != tt.want {
			t.Errorf("cleanupBackoff(%d) = %v, want %v", tt.attempts, got, tt.want)
		}
	}
}

func TestParseDisabledRules(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want int
	}{
		{"nil", nil, 0},
		{"empty", []byte{}, 0},
		{"null literal", []byte("null"), 0},
		{"empty array", []byte("[]"), 0},
		{"garbage", []byte("{not json"), 0},
		{"one rule", []byte(`[{"rule":"keep-apart","type":"resource-affinity"}]`), 1},
		{"two rules", []byte(`[{"rule":"a","type":"node-affinity"},{"rule":"b","type":"resource-affinity"}]`), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDisabledRules(tt.raw); len(got) != tt.want {
				t.Errorf("parseDisabledRules(%q) returned %d rules, want %d", tt.raw, len(got), tt.want)
			}
		})
	}
}

func TestMergeDisabledRules(t *testing.T) {
	a := DisabledHARule{Rule: "a", Type: "node-affinity"}
	b := DisabledHARule{Rule: "b", Type: "resource-affinity"}
	c := DisabledHARule{Rule: "c", Type: "resource-affinity"}

	tests := []struct {
		name     string
		existing []DisabledHARule
		add      []DisabledHARule
		want     []string
	}{
		{"both nil", nil, nil, nil},
		{"add to empty", nil, []DisabledHARule{a}, []string{"a"}},
		{"disjoint union", []DisabledHARule{a}, []DisabledHARule{b, c}, []string{"a", "b", "c"}},
		{"duplicate dropped", []DisabledHARule{a, b}, []DisabledHARule{b, c}, []string{"a", "b", "c"}},
		{"idempotent re-merge", []DisabledHARule{a, b}, []DisabledHARule{a, b}, []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeDisabledRules(tt.existing, tt.add)
			if len(got) != len(tt.want) {
				t.Fatalf("merged %d rules, want %d (%v)", len(got), len(tt.want), got)
			}
			for i, name := range tt.want {
				if got[i].Rule != name {
					t.Errorf("merged[%d] = %q, want %q", i, got[i].Rule, name)
				}
			}
		})
	}
}

func TestNonNilRulesMarshalsToEmptyArray(t *testing.T) {
	raw, err := json.Marshal(nonNilRules(nil))
	if err != nil {
		t.Fatal(err)
	}
	// "[]" marks a cleared record; "null" would be indistinguishable from
	// "never recorded" and would re-trigger the legacy fallback paths.
	if string(raw) != "[]" {
		t.Errorf("marshaled empty rule set = %s, want []", raw)
	}
}

func TestPassthroughGuests(t *testing.T) {
	guests := []GuestSnapshot{
		{VMID: 100, Type: "qemu", Passthrough: true},
		{VMID: 101, Type: "qemu"},
		{VMID: 102, Type: "lxc"},
		{VMID: 103, Type: "qemu", Passthrough: true},
	}
	got := passthroughGuests(guests)
	if len(got) != 2 || got[0].VMID != 100 || got[1].VMID != 103 {
		t.Errorf("passthroughGuests = %+v, want VMIDs [100 103]", got)
	}
	if empty := passthroughGuests(nil); empty == nil || len(empty) != 0 {
		t.Errorf("passthroughGuests(nil) = %v, want non-nil empty slice", empty)
	}
}

func TestStoppedPassthroughFor(t *testing.T) {
	o := &Orchestrator{}

	t.Run("explicit record wins over snapshot", func(t *testing.T) {
		node := db.RollingUpdateNode{
			StoppedPassthroughJson: []byte(`[{"vmid":100,"type":"qemu","passthrough":true}]`),
			GuestsJson:             []byte(`[{"vmid":200,"type":"qemu","passthrough":true}]`),
		}
		got := o.stoppedPassthroughFor(node)
		if len(got) != 1 || got[0].VMID != 100 {
			t.Errorf("got %+v, want the recorded VMID 100", got)
		}
	})

	t.Run("cleared record means nothing pending", func(t *testing.T) {
		node := db.RollingUpdateNode{
			StoppedPassthroughJson: []byte(`[]`),
			GuestsJson:             []byte(`[{"vmid":200,"type":"qemu","passthrough":true}]`),
		}
		if got := o.stoppedPassthroughFor(node); len(got) != 0 {
			t.Errorf("got %+v, want empty for a cleared record", got)
		}
	})

	t.Run("legacy NULL record derives from guest snapshot", func(t *testing.T) {
		node := db.RollingUpdateNode{
			Step:                   "draining",
			StoppedPassthroughJson: nil,
			GuestsJson:             []byte(`[{"vmid":200,"type":"qemu","passthrough":true},{"vmid":201,"type":"qemu"}]`),
		}
		got := o.stoppedPassthroughFor(node)
		if len(got) != 1 || got[0].VMID != 200 {
			t.Errorf("got %+v, want derived passthrough VMID 200", got)
		}
	})

	t.Run("legacy NULL record with no passthrough guests", func(t *testing.T) {
		node := db.RollingUpdateNode{
			Step:                   "draining",
			StoppedPassthroughJson: nil,
			GuestsJson:             []byte(`[{"vmid":201,"type":"qemu"}]`),
		}
		if got := o.stoppedPassthroughFor(node); len(got) != 0 {
			t.Errorf("got %+v, want empty", got)
		}
	})

	// A pre-000073 node that already completed had its passthrough guests
	// handled by the old code long ago — deriving work for it could power on
	// a guest an admin has since stopped on purpose.
	t.Run("legacy NULL record skipped for finished nodes", func(t *testing.T) {
		for _, step := range []string{"completed", "skipped"} {
			node := db.RollingUpdateNode{
				Step:                   step,
				StoppedPassthroughJson: nil,
				GuestsJson:             []byte(`[{"vmid":200,"type":"qemu","passthrough":true}]`),
			}
			if got := o.stoppedPassthroughFor(node); len(got) != 0 {
				t.Errorf("step %q: got %+v, want no derived work", step, got)
			}
		}
	})
}

func TestFormatGuestList(t *testing.T) {
	short := []string{"qemu 100 (web)", "lxc 200 (db)"}
	if got := formatGuestList(short); got != "qemu 100 (web), lxc 200 (db)" {
		t.Errorf("formatGuestList(short) = %q", got)
	}

	long := make([]string, 14)
	for i := range long {
		long[i] = "g"
	}
	got := formatGuestList(long)
	if !strings.HasSuffix(got, "and 4 more") {
		t.Errorf("formatGuestList(long) = %q, want suffix 'and 4 more'", got)
	}
	if strings.Count(got, "g") != 10 {
		t.Errorf("formatGuestList(long) listed %d entries, want 10", strings.Count(got, "g"))
	}
}

// TestVerifyNodeDrained covers the post-drain / pre-reboot safety check:
// running and paused guests are violations, templates and stopped guests are
// not, and a listing failure surfaces as an error (the callers then proceed
// with a warning instead of failing the node on an API blip).
func TestVerifyNodeDrained(t *testing.T) {
	o, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api2/json/nodes/pve1/qemu":
			payload, _ := json.Marshal(map[string]interface{}{
				"data": []proxmox.VirtualMachine{
					{VMID: 100, Name: "web", Status: "running"},
					{VMID: 101, Name: "old", Status: "stopped"},
					{VMID: 102, Name: "tmpl", Status: "stopped", Template: 1},
					{VMID: 103, Name: "frozen", Status: "paused"},
				},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
		case "/api2/json/nodes/pve1/lxc":
			payload, _ := json.Marshal(map[string]interface{}{
				"data": []proxmox.Container{
					{VMID: 200, Name: "ct-live", Status: "running"},
					{VMID: 201, Name: "ct-off", Status: "stopped"},
				},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	})
	defer closeStub()

	violations, err := o.verifyNodeDrained(context.Background(), client, "pve1")
	if err != nil {
		t.Fatalf("verifyNodeDrained: %v", err)
	}
	want := []string{"qemu 100 (web)", "qemu 103 (frozen)", "lxc 200 (ct-live)"}
	if len(violations) != len(want) {
		t.Fatalf("violations = %v, want %v", violations, want)
	}
	for i := range want {
		if violations[i] != want[i] {
			t.Errorf("violations[%d] = %q, want %q", i, violations[i], want[i])
		}
	}

	// Listing failure must be an error, not an empty (passing) result.
	if _, err := o.verifyNodeDrained(context.Background(), client, "gone-node"); err == nil {
		t.Error("verifyNodeDrained on a failing node listing returned nil error")
	}
}

// TestReenableHARules_UnsetsDisableOnTheWire pins what the restore actually
// puts on the wire, not just that it put something there.
//
// The restore was a silent no-op for as long as it sent `disable=0`: PVE's
// update_rule drops a falsy disable before it reads the config, so every
// rolling update answered 200, cleared its restore record and audited
// "ha_rules_restored" while the rules stayed off. Counting PUTs cannot see
// that — TestReenableHARules_DropsDeletedRules passed throughout — so the
// form is the only thing worth asserting here.
func TestReenableHARules_UnsetsDisableOnTheWire(t *testing.T) {
	var mu sync.Mutex
	var form url.Values
	o, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/api2/json/cluster/ha/rules/ok-rule" {
			_ = r.ParseForm()
			mu.Lock()
			form = r.PostForm
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":null}`))
			return
		}
		http.NotFound(w, r)
	})
	defer closeStub()

	rules := []DisabledHARule{{Rule: "ok-rule", Type: "resource-affinity"}}
	if remaining := o.reenableHARules(context.Background(), client, rules); len(remaining) != 0 {
		t.Fatalf("remaining = %+v, want none", remaining)
	}

	mu.Lock()
	defer mu.Unlock()
	if form == nil {
		t.Fatal("no PUT reached the rule")
	}
	if form.Get("delete") != "disable" {
		t.Errorf("delete form param: want %q, got %q", "disable", form.Get("delete"))
	}
	if _, sent := form["disable"]; sent {
		t.Errorf("disable=%q sent instead of unsetting it — PVE ignores a falsy disable", form.Get("disable"))
	}
	if form.Get("type") != "resource-affinity" {
		t.Errorf("type form param: %q", form.Get("type"))
	}
}

// TestReenableHARules_DropsDeletedRules ensures a rule that fails to
// re-enable is kept for retry — unless it no longer exists on the cluster,
// in which case it's dropped so the cleanup sweep can converge instead of
// retrying a rule that can never be re-enabled.
func TestReenableHARules_DropsDeletedRules(t *testing.T) {
	var putCalls atomic.Int32
	o, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api2/json/cluster/ha/rules/"):
			putCalls.Add(1)
			if strings.HasSuffix(r.URL.Path, "/deleted-rule") || strings.HasSuffix(r.URL.Path, "/broken-rule") {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"data":null,"message":"no such ha rule\n"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":null}`))
		case r.URL.Path == "/api2/json/cluster/ha/rules":
			// "deleted-rule" is gone from the cluster; "broken-rule" still
			// exists but errors on update.
			payload, _ := json.Marshal(map[string]interface{}{
				"data": []proxmox.HARuleEntry{
					{Rule: "ok-rule", Type: "resource-affinity"},
					{Rule: "broken-rule", Type: "resource-affinity", Disable: 1},
				},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	})
	defer closeStub()

	rules := []DisabledHARule{
		{Rule: "ok-rule", Type: "resource-affinity"},
		{Rule: "deleted-rule", Type: "resource-affinity"},
		{Rule: "broken-rule", Type: "resource-affinity"},
	}
	remaining := o.reenableHARules(context.Background(), client, rules)
	if len(remaining) != 1 || remaining[0].Rule != "broken-rule" {
		t.Errorf("remaining = %+v, want only broken-rule (deleted-rule dropped, ok-rule re-enabled)", remaining)
	}
	if got := putCalls.Load(); got != 3 {
		t.Errorf("SetHARuleDisabled calls = %d, want 3", got)
	}
}

// TestGuestStatusOnNode distinguishes the three outcomes the release path
// branches on: present (status returned), absent (found=false, no error),
// and unknown (listing error) — absent entries are pruned from the
// stopped-passthrough record while unknown ones must be retried.
func TestGuestStatusOnNode(t *testing.T) {
	o, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/nodes/pve1/qemu" {
			payload, _ := json.Marshal(map[string]interface{}{
				"data": []proxmox.VirtualMachine{{VMID: 100, Name: "web", Status: "stopped"}},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		http.NotFound(w, r)
	})
	defer closeStub()

	status, found, err := o.guestStatusOnNode(context.Background(), client, "pve1", GuestSnapshot{VMID: 100, Type: "qemu"})
	if err != nil || !found || status != "stopped" {
		t.Errorf("present guest: status=%q found=%v err=%v, want stopped/true/nil", status, found, err)
	}

	_, found, err = o.guestStatusOnNode(context.Background(), client, "pve1", GuestSnapshot{VMID: 999, Type: "qemu"})
	if err != nil || found {
		t.Errorf("absent guest: found=%v err=%v, want false/nil", found, err)
	}

	_, _, err = o.guestStatusOnNode(context.Background(), client, "down-node", GuestSnapshot{VMID: 100, Type: "qemu"})
	if err == nil {
		t.Error("listing failure returned nil error — release path would wrongly prune the record entry")
	}
}

// TestNoout_HeldAndReleasedOnlyWhenTheJobSetIt: noout is recorded before and
// after it is set and cleared when the job ends; one the operator set first is
// never the job's; a set Proxmox refuses leaves the job going without it, with
// the reason; a release the endpoint cannot make keeps the record for the sweep.
// "raced" is a release that ran during the set and wrote 'released': the record
// written after the set puts 'held' back.
func TestNoout_HeldAndReleasedOnlyWhenTheJobSetIt(t *testing.T) {
	t.Parallel()
	const notInstalled = `{"data":null,"message":"binary not installed: /usr/bin/ceph-mon\n"}`
	for _, tt := range []struct {
		name         string
		alreadySet   bool
		refuseSet    bool
		refuseUnset  bool
		raced        bool
		wantState    string
		wantReleased bool
		wantPuts     []string
	}{
		{name: "held and released", wantState: nooutHeld + "/", wantReleased: true, wantPuts: []string{"value=1", "value=0"}},
		{name: "already set by the operator, never cleared", alreadySet: true, wantState: nooutAlreadySet + "/", wantReleased: true},
		{name: "a refused set is not held", refuseSet: true,
			wantState: nooutNotHeld + "/noout is not held: the cluster's API token lacks Sys.Modify on /.", wantReleased: true, wantPuts: []string{"value=1"}},
		{name: "an endpoint without Ceph keeps it held", refuseUnset: true, wantState: nooutHeld + "/", wantPuts: []string{"value=1", "value=0"}},
		{name: "raced", raced: true, wantState: nooutHeld + "/", wantReleased: true, wantPuts: []string{"value=1", "value=0"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var puts []string
			d := &memberDB{t: t, audits: true, jobStatus: "running",
				accept: map[string]int64{"SetJobCephNooutState": 1, "ArmJobCleanupPending": 0}}
			o, client, _ := engineOn(t, d, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api2/json/cluster/ceph/flags/noout" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPut {
					_ = r.ParseForm()
					value := r.PostForm.Get("value")
					mu.Lock()
					puts = append(puts, "value="+value)
					mu.Unlock()
					switch {
					case tt.raced:
						// The other release, between the record and the set.
						_, _ = d.Exec(context.Background(), "-- name: SetJobCephNooutState", nooutReleased, "", uuid.Nil)
					case tt.refuseSet && value == "1":
						http.Error(w, `{"data":null,"message":"Permission check failed (/, Sys.Modify)\n"}`, http.StatusForbidden)
						return
					case tt.refuseUnset && value == "0":
						http.Error(w, notInstalled, http.StatusInternalServerError)
						return
					}
					_, _ = w.Write([]byte(`{"data":null}`))
					return
				}
				_, _ = w.Write([]byte(map[bool]string{true: `{"data":1}`, false: `{"data":0}`}[tt.alreadySet]))
			})
			job := db.RollingUpdateJob{ID: uuid.New(), Status: "running", CephNoout: true,
				CephPresent: pgtype.Bool{Bool: true, Valid: true}}

			o.holdNoout(context.Background(), client, job)
			if got := lastNooutState(d); got != tt.wantState {
				t.Fatalf("noout state after the hold = %q, want %q", got, tt.wantState)
			}
			// The job ends: release reads the state holdNoout left.
			job.CephNooutState, _, _ = strings.Cut(tt.wantState, "/")
			if released := o.releaseHeldNoout(context.Background(), client, job, true); released != tt.wantReleased {
				t.Errorf("released = %v, want %v (state %q)", released, tt.wantReleased, lastNooutState(d))
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(puts, tt.wantPuts) {
				t.Errorf("sent %v to the noout flag, want %v", puts, tt.wantPuts)
			}
		})
	}
}

// lastNooutState is the last SetJobCephNooutState memberDB took, "state/reason".
func lastNooutState(d *memberDB) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.writes) - 1; i >= 0; i-- {
		if d.writes[i] == "SetJobCephNooutState" {
			return strings.TrimPrefix(d.values[i], "SetJobCephNooutState=") + "/" + d.reasons[i]
		}
	}
	return ""
}

// TestNoout_WiredIntoTheJob: startNode sets noout before the node goes down
// (here, an in-place upgrade), and releaseJobState counts a held noout as state
// still to release when it cannot reach the cluster.
func TestNoout_WiredIntoTheJob(t *testing.T) {
	t.Parallel()
	t.Run("set before the node goes down", func(t *testing.T) {
		t.Parallel()
		cluster := uuid.New()
		d := &memberDB{t: t, cluster: cluster, members: []string{"pve-01"}, audits: true, jobStatus: "running",
			accept: map[string]int64{"SetJobCephNooutState": 1, "ArmJobCleanupPending": 0, "SetNodeInPlaceUpgradeManual": 1}}
		var setFirst atomic.Bool
		o, client, _ := engineOn(t, d, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api2/json/cluster/ceph/flags/noout" {
				http.Error(w, "unexpected", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPut {
				d.mu.Lock()
				setFirst.Store(!slices.Contains(d.writes, "SetNodeInPlaceUpgradeManual"))
				d.mu.Unlock()
				_, _ = w.Write([]byte(`{"data":null}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":0}`))
		})
		job := db.RollingUpdateJob{ID: uuid.New(), ClusterID: cluster, Status: "running", CephNoout: true,
			CephPresent: pgtype.Bool{Bool: true, Valid: true}}
		o.startNode(context.Background(), client, job, db.RollingUpdateNode{ID: uuid.New(), JobID: job.ID, NodeName: "pve-01", Step: "pending"})
		if !setFirst.Load() || !slices.Contains(d.writes, "SetNodeInPlaceUpgradeManual") {
			t.Errorf("noout set before the upgrade = %v (wrote %v)", setFirst.Load(), d.writes)
		}
	})
	t.Run("held and unreachable is not released", func(t *testing.T) {
		t.Parallel()
		jobID := uuid.New()
		d := &memberDB{t: t, audits: true, clusterErr: errors.New("gone"),
			rowFor:  map[string]any{"GetRollingUpdateJob": db.RollingUpdateJob{ID: jobID, Status: "cancelled", CephNooutState: nooutHeld}},
			rowsFor: map[string][][]any{"ListRollingUpdateNodes": {}}}
		o, _, _ := engineOn(t, d, nil)
		if released, didWork := o.releaseJobState(context.Background(), jobID, true); released || !didWork {
			t.Errorf("releaseJobState = %v, %v; want false, true", released, didWork)
		}
	})
}
