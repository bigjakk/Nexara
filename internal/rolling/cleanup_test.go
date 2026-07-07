package rolling

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
