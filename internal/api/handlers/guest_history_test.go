package handlers

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func TestGuestTaskOutcome(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                   string
		status, exit, recorded string
		want                   string
	}{
		{"task row running", "running", "", "", guestOutcomeRunning},
		{"task row completed", "completed", "OK", "RUNNING", guestOutcomeOK},
		{"task row failed", "failed", "VM 100 is locked", "", guestOutcomeFailed},
		{"raw stopped with warnings", "stopped", "WARNINGS: 2", "", guestOutcomeOK},
		{"raw stopped with an error", "stopped", "command failed", "", guestOutcomeFailed},
		// No task row: the status the collector stored at ingest decides.
		{"ingested while running", "", "", "RUNNING", ""},
		{"ingested finished", "", "", "OK", guestOutcomeOK},
		{"ingested failed", "", "", "unable to find configuration file", guestOutcomeFailed},
		{"nothing recorded", "", "", "", ""},
	} {
		if got := guestTaskOutcome(tt.status, tt.exit, tt.recorded); got != tt.want {
			t.Errorf("%s: guestTaskOutcome(%q, %q, %q) = %q, want %q",
				tt.name, tt.status, tt.exit, tt.recorded, got, tt.want)
		}
	}
}

// The cutoff decides whose history a reused VMID shows. Events are newest
// first, as ListGuestLifecycleEvents returns them.
func TestGuestHistoryCutoff(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	birth := func(h int) db.ListGuestLifecycleEventsRow { return db.ListGuestLifecycleEventsRow{CreatedAt: at(h)} }
	destroy := func(h int, status, exit, recorded string) db.ListGuestLifecycleEventsRow {
		return db.ListGuestLifecycleEventsRow{CreatedAt: at(h), IsDestroy: true,
			TaskStatus: status, TaskExitStatus: exit, RecordedStatus: recorded}
	}
	for _, tt := range []struct {
		name          string
		events        []db.ListGuestLifecycleEventsRow
		wantAt        time.Time
		wantInclusive bool
	}{
		{"no lifecycle events: the whole history", nil, time.Time{}, false},
		{"newest is a birth: from it, inclusive", []db.ListGuestLifecycleEventsRow{birth(5), destroy(3, "completed", "OK", "")}, at(5), true},
		{"newest is the previous guest's destroy: after it", []db.ListGuestLifecycleEventsRow{destroy(5, "completed", "OK", ""), birth(1)}, at(5), false},
		{"a failed destroy ended nothing", []db.ListGuestLifecycleEventsRow{destroy(5, "failed", "VM is locked", ""), birth(1)}, at(1), true},
		{"a running destroy has ended nothing yet", []db.ListGuestLifecycleEventsRow{destroy(5, "running", "", ""), destroy(4, "", "", "OK")}, at(4), false},
		{"an old destroy whose outcome was pruned counts", []db.ListGuestLifecycleEventsRow{destroy(5, "", "", "")}, at(5), false},
	} {
		got, inclusive := guestHistoryCutoff(tt.events)
		if got.Valid != !tt.wantAt.IsZero() || !got.Time.Equal(tt.wantAt) || inclusive != tt.wantInclusive {
			t.Errorf("%s: cutoff = (%v, valid %v, inclusive %v), want (%v, inclusive %v)",
				tt.name, got.Time, got.Valid, inclusive, tt.wantAt, tt.wantInclusive)
		}
	}
}

// Each permission opens exactly its own part of the timeline, and a lookup
// that fails fails the request instead of quietly dropping that part.
func TestGuestHistoryScope(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		grants    []string
		wantTasks bool
		wantTypes []string
	}{
		{"audit only", nil, false, []string{}},
		{"view:task opens the task-only rows", []string{"view:task"}, true, []string{}},
		{"view:vm opens a VM's status changes", []string{"view:vm"}, false, []string{"qemu"}},
		{"view:container opens a container's", []string{"view:container"}, false, []string{"lxc"}},
		{"everything", []string{"view:task", "view:vm", "view:container"}, true, []string{"qemu", "lxc"}},
	} {
		tasks, types, err := guestHistoryScope(func(action, resource string) (bool, error) {
			return slices.Contains(tt.grants, action+":"+resource), nil
		})
		if err != nil || tasks != tt.wantTasks || !slices.Equal(types, tt.wantTypes) {
			t.Errorf("%s: scope = (tasks %v, types %v, err %v), want (tasks %v, types %v)",
				tt.name, tasks, types, err, tt.wantTasks, tt.wantTypes)
		}
	}

	boom := errors.New("rbac unavailable")
	if _, _, err := guestHistoryScope(func(_, resource string) (bool, error) {
		if resource == "container" {
			return false, boom
		}
		return true, nil
	}); !errors.Is(err, boom) {
		t.Errorf("a failed permission lookup returned %v, want it to fail the request", err)
	}
}

// A task's exit text is Proxmox's own words and reaches only a view:task
// holder; the verdict stays. A redacted row keeps nothing SQL lifted out of
// its details.
func TestToGuestHistoryEntry(t *testing.T) {
	t.Parallel()
	row := db.ListGuestHistoryRow{
		Kind: "audit", ResourceType: "vm", Details: json.RawMessage(`{}`),
		Upid: "UPID:pve-01:00001A2B:00003344:5F2E1A00:qmigrate:100:root@pam:", Node: "pve-01",
		TaskStatus: "failed", TaskExitStatus: "migration aborted",
	}
	for _, tt := range []struct {
		showExit bool
		wantExit string
	}{{true, "migration aborted"}, {false, ""}} {
		if e := toGuestHistoryEntry(row, nil, tt.showExit); e.Outcome != guestOutcomeFailed || e.ExitStatus != tt.wantExit {
			t.Errorf("exit text shown=%v: outcome %q, exit %q; want %q, %q",
				tt.showExit, e.Outcome, e.ExitStatus, guestOutcomeFailed, tt.wantExit)
		}
	}

	// One reserved key proves the rule; they all share it.
	key := slices.Sorted(maps.Keys(reservedGlobalSettings))[0]
	redacted := row
	redacted.ResourceType, redacted.ResourceID = settingResourceType, key
	if e := toGuestHistoryEntry(redacted, map[string]bool{}, true); e.UPID != "" || e.Node != "" ||
		e.Outcome != "" || e.ExitStatus != "" {
		t.Errorf("redacted %s row still carries upid %q, node %q, outcome %q, exit %q",
			key, e.UPID, e.Node, e.Outcome, e.ExitStatus)
	}
}
