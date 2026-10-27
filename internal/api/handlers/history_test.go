package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

func TestHistoryTaskOutcome(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                   string
		status, exit, recorded string
		want                   string
	}{
		{"task row running", "running", "", "", historyOutcomeRunning},
		{"task row completed", "completed", "OK", "RUNNING", historyOutcomeOK},
		{"task row failed", "failed", "VM 100 is locked", "", historyOutcomeFailed},
		{"raw stopped with warnings", "stopped", "WARNINGS: 2", "", historyOutcomeOK},
		{"raw stopped with an error", "stopped", "command failed", "", historyOutcomeFailed},
		// No task row: the status the collector stored at ingest decides.
		{"ingested while running", "", "", "RUNNING", ""},
		{"ingested finished", "", "", "OK", historyOutcomeOK},
		{"ingested failed", "", "", "unable to find configuration file", historyOutcomeFailed},
		{"nothing recorded", "", "", "", ""},
	} {
		if got := historyTaskOutcome(tt.status, tt.exit, tt.recorded); got != tt.want {
			t.Errorf("%s: historyTaskOutcome(%q, %q, %q) = %q, want %q",
				tt.name, tt.status, tt.exit, tt.recorded, got, tt.want)
		}
	}
}

// Each permission opens exactly its own part of the timeline, and a lookup
// that fails fails the request instead of quietly dropping that part.
func TestHistoryScope(t *testing.T) {
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
		tasks, types, err := historyScope(func(action, resource string) (bool, error) {
			return slices.Contains(tt.grants, action+":"+resource), nil
		})
		if err != nil || tasks != tt.wantTasks || !slices.Equal(types, tt.wantTypes) {
			t.Errorf("%s: scope = (tasks %v, types %v, err %v), want (tasks %v, types %v)",
				tt.name, tasks, types, err, tt.wantTasks, tt.wantTypes)
		}
	}

	boom := errors.New("rbac unavailable")
	if _, _, err := historyScope(func(_, resource string) (bool, error) {
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
func TestToHistoryEntry(t *testing.T) {
	t.Parallel()
	row := db.ListHistoryRow{
		Kind: "audit", ResourceType: "vm", Details: json.RawMessage(`{}`),
		Upid: "UPID:pve-01:00001A2B:00003344:5F2E1A00:qmigrate:100:root@pam:", Node: "pve-01",
		TaskStatus: "failed", TaskExitStatus: "migration aborted",
	}
	for _, tt := range []struct {
		showExit bool
		wantExit string
	}{{true, "migration aborted"}, {false, ""}} {
		if e := toHistoryEntry(row, nil, tt.showExit); e.Outcome != historyOutcomeFailed || e.ExitStatus != tt.wantExit {
			t.Errorf("exit text shown=%v: outcome %q, exit %q; want %q, %q",
				tt.showExit, e.Outcome, e.ExitStatus, historyOutcomeFailed, tt.wantExit)
		}
	}

	// A backup entry ends when the guest's own backup did, not the job.
	jobEnd, guestEnd := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	backup := row
	backup.Kind, backup.LogStart, backup.LogEnd = "backup", 41, 60
	backup.TaskFinishedAt = pgtype.Timestamptz{Time: jobEnd, Valid: true}
	backup.BackupFinishedAt = pgtype.Timestamptz{Time: guestEnd, Valid: true}
	if e := toHistoryEntry(backup, nil, true); e.FinishedAt == nil ||
		*e.FinishedAt != guestEnd.Format(time.RFC3339Nano) || e.LogStart != 41 || e.LogEnd != 60 {
		t.Errorf("backup entry: finished %v, lines %d-%d; want %s, 41-60", e.FinishedAt, e.LogStart, e.LogEnd, guestEnd)
	}

	// One reserved key proves the rule; they all share it.
	key := slices.Sorted(maps.Keys(reservedGlobalSettings))[0]
	redacted := row
	redacted.ResourceType, redacted.ResourceID = settingResourceType, key
	if e := toHistoryEntry(redacted, map[string]bool{}, true); e.UPID != "" || e.Node != "" ||
		e.Outcome != "" || e.ExitStatus != "" {
		t.Errorf("redacted %s row still carries upid %q, node %q, outcome %q, exit %q",
			key, e.UPID, e.Node, e.Outcome, e.ExitStatus)
	}
}

// The registry calls Validate at startup, so each refusal here is a kind
// that would otherwise ship a type filter that 400s or always comes back
// empty.
func TestHistoryKindValidate(t *testing.T) {
	t.Parallel()
	valid := func() HistoryKind {
		return HistoryKind{
			Name: "pool", Categories: []string{"membership", "other"}, Default: "other",
			Rules:  []HistoryRule{{Category: "membership", Field: HistoryFieldAction, Equals: []string{"set_pool"}}},
			Select: SelectObjects("pool", HistoryObject{ResourceType: "pool"}),
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("a valid kind was refused: %v", err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*HistoryKind)
		want   string
	}{
		{"a name that is not a namespace suffix", func(k *HistoryKind) { k.Name = "HA rule" }, "Name must be"},
		{"no selector", func(k *HistoryKind) { k.Select = nil }, "no Select"},
		{"an unknown field", func(k *HistoryKind) { k.Rules[0].Field = "node" }, "unknown field"},
		{"a rule matching nothing", func(k *HistoryKind) { k.Rules[0].Equals = nil }, "matches nothing"},
		{"a category the filter refuses", func(k *HistoryKind) { k.Rules[0].Category = "pools" }, "not one of its Categories"},
		{"a category nothing produces", func(k *HistoryKind) { k.Categories = append(k.Categories, "ha") }, "no rule produces"},
		{"a category twice", func(k *HistoryKind) { k.Categories = append(k.Categories, "other") }, "twice"},
		{"a default the filter refuses", func(k *HistoryKind) { k.Default = "misc" }, "Default"},
	} {
		k := valid()
		tt.mutate(&k)
		if err := k.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Validate() = %v, want an error naming %q", tt.name, err, tt.want)
		}
	}
}

// An Equals value matches only itself under LIKE: "_" and "%" are the
// wildcards, so "set_pool" unescaped would match "setXpool" too.
func TestHistoryKindSQLRules(t *testing.T) {
	t.Parallel()
	k := HistoryKind{Rules: []HistoryRule{
		{Category: "config", Field: HistoryFieldAction, Equals: []string{`set_pool`, `100%\`}, Like: []string{`disk\_%`}},
	}}
	cats, fields, patterns := k.sqlRules()
	if want := []string{`set\_pool`, `100\%\\`, `disk\_%`}; !slices.Equal(patterns, want) ||
		!slices.Equal(cats, []string{"config", "config", "config"}) ||
		!slices.Equal(fields, []string{"action", "action", "action"}) {
		t.Errorf("sqlRules = %v %v %v, want patterns %v", cats, fields, patterns, want)
	}
}

// The zero time is ListHistory's "no cutoff"; a guest named twice would be
// looked up by the first of its cutoffs only, so it is refused.
func TestHistoryKindListParams(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p, err := GuestHistory().ListParams([16]byte{1}, HistorySelection{
		Guests:  []HistoryGuest{{VMID: 100}, {VMID: 101, After: pgtype.Timestamptz{Time: at, Valid: true}, Inclusive: true}},
		Objects: []HistoryObject{{ResourceType: "vm_folder", Value: "f1"}, {ResourceType: "vm_folder_membership", DetailKey: "folder_id", Value: "f1"}},
	})
	if err != nil || !slices.Equal(p.Vmids, []int32{100, 101}) ||
		!slices.Equal(p.CutoffAfter, []time.Time{{}, at}) || !slices.Equal(p.CutoffInclusive, []bool{false, true}) ||
		!slices.Equal(p.ObjectKeys, []string{"", "folder_id"}) {
		t.Errorf("ListParams = %+v, %v", p, err)
	}
	if _, err := GuestHistory().ListParams([16]byte{1}, HistorySelection{Guests: []HistoryGuest{{VMID: 100}, {VMID: 100}}}); err == nil {
		t.Error("a guest named twice was accepted")
	}
}

// The generic selector names each object by the value of its path parameter.
func TestSelectObjects(t *testing.T) {
	t.Parallel()
	props := apischema.Properties{"rule": {Type: apischema.String}}
	if err := props.Compile(); err != nil {
		t.Fatal(err)
	}
	p, err := props.Validate(map[string]any{"rule": "rule01"})
	if err != nil {
		t.Fatal(err)
	}
	sel, err := SelectObjects("rule", HistoryObject{ResourceType: "ha_rule"},
		HistoryObject{ResourceType: "ha_resource", DetailKey: "rule"})(context.Background(), HistoryRequest{Params: p})
	want := []HistoryObject{{ResourceType: "ha_rule", Value: "rule01"}, {ResourceType: "ha_resource", DetailKey: "rule", Value: "rule01"}}
	if err != nil || len(sel.Guests) != 0 || !slices.Equal(sel.Objects, want) {
		t.Errorf("SelectObjects = %+v, %v; want objects %+v", sel, err, want)
	}
}
