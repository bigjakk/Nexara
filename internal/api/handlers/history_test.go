package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
			Rules:       []HistoryRule{{Category: "membership", Field: HistoryFieldAction, Equals: []string{"set_pool"}}},
			Select:      SelectObjects("pool", HistoryObject{ResourceType: "pool"}),
			ObjectTypes: []string{"pool"},
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
		{"a pattern ending in a lone backslash", func(k *HistoryKind) { k.Rules[0].Like = []string{`drs\`} }, "lone backslash"},
		{"a category the filter refuses", func(k *HistoryKind) { k.Rules[0].Category = "pools" }, "not one of its Categories"},
		{"a category nothing produces", func(k *HistoryKind) { k.Categories = append(k.Categories, "ha") }, "no rule produces"},
		{"a category twice", func(k *HistoryKind) { k.Categories = append(k.Categories, "other") }, "twice"},
		{"a default the filter refuses", func(k *HistoryKind) { k.Default = "misc" }, "Default"},
		{"a global kind with a guest", func(k *HistoryKind) { k.Global, k.GuestFromPath = true, "vmid" }, "global"},
		{"a global kind reading a cluster's type", func(k *HistoryKind) { k.Global = true }, "unique only within a cluster"},
		{"an observed type it does not read", func(k *HistoryKind) {
			k.ObservedTypes = []HistoryObservedType{{Type: "replication"}}
		}, "observed changes"},
		{"an observed type with no view", func(k *HistoryKind) {
			k.ObjectTypes, k.ObservedTypes = []string{"pool", "probe"}, []HistoryObservedType{{Type: "probe"}}
		}, "observed changes"},
		{"a whole-cluster kind naming objects", func(k *HistoryKind) { k.WholeCluster = true }, "whole cluster"},
		{"runs outside a whole-cluster read", func(k *HistoryKind) {
			k.Collapse = []HistoryCollapse{{ResourceType: "cluster", Action: "sync_failed", Gap: time.Minute}}
		}, "only a whole-cluster read"},
		{"a narrowed read capped and an unfiltered one not", func(k *HistoryKind) { k.FilteredRowCap = 100 },
			"caps one read and not the other"},
		{"a run with no gap", func(k *HistoryKind) {
			k.WholeCluster, k.ObjectTypes = true, nil
			k.Collapse = []HistoryCollapse{{ResourceType: "cluster", Action: "sync_failed"}}
		}, "needs a resource type, an action and a gap"},
	} {
		k := valid()
		tt.mutate(&k)
		if err := k.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: Validate() = %v, want an error naming %q", tt.name, err, tt.want)
		}
	}
	// A type the map lacks is shown under a view of the kind's own.
	k := valid()
	k.ObjectTypes, k.ObservedTypes = []string{"pool", "probe"}, []HistoryObservedType{{Type: "probe", View: "network"}}
	if err := k.Validate(); err != nil {
		t.Errorf("an observed type with its own view: %v", err)
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

// The zero time is ListHistory's "no cutoff". A guest named twice is one,
// unless its cutoffs differ: ListHistory would hold it to the first only.
func TestHistoryKindListParams(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k := GuestHistory()
	k.ObjectTypes = []string{"vm_folder", "vm_folder_membership", "pool", "pbs_server", "pve_acl", "vm", "container"}
	p, err := k.ListParams([16]byte{1}, HistorySelection{
		Guests:  []HistoryGuest{{VMID: 100}, {VMID: 101, After: pgtype.Timestamptz{Time: at, Valid: true}, Inclusive: true}},
		Objects: []HistoryObject{{ResourceType: "vm_folder", Value: "f1"}, {ResourceType: "vm_folder_membership", DetailKey: "folder_id", Value: "f1"}},
	})
	if err != nil || !slices.Equal(p.Vmids, []int32{100, 101}) || p.CutoffFloor.Valid ||
		!slices.Equal(p.CutoffAfter, []time.Time{{}, at}) || !slices.Equal(p.CutoffInclusive, []bool{false, true}) ||
		!slices.Equal(p.ObjectKeys, []string{"", "folder_id"}) {
		t.Errorf("ListParams = %+v, %v", p, err)
	}
	// Every arm reads from the earliest cutoff — none, above, when a guest has none.
	if p, _ := k.ListParams([16]byte{1}, HistorySelection{Guests: []HistoryGuest{
		{VMID: 100, After: pgtype.Timestamptz{Time: at.Add(time.Hour), Valid: true}},
		{VMID: 101, After: pgtype.Timestamptz{Time: at, Valid: true}}}}); !p.CutoffFloor.Valid || !p.CutoffFloor.Time.Equal(at) {
		t.Errorf("cutoff floor = %+v, want the earlier cutoff %v", p.CutoffFloor, at)
	}
	// The same guest twice is one; twice with different cutoffs is refused.
	if p, err := k.ListParams([16]byte{1}, HistorySelection{Guests: []HistoryGuest{{VMID: 100}, {VMID: 100}}}); err != nil ||
		!slices.Equal(p.Vmids, []int32{100}) {
		t.Errorf("the same guest twice: vmids %v, err %v; want one guest", p.Vmids, err)
	}
	// A selection ListHistory would read wrong — or fail on — is refused.
	for name, sel := range map[string]HistorySelection{
		"one guest with two cutoffs":    {Guests: []HistoryGuest{{VMID: 100}, {VMID: 100, After: pgtype.Timestamptz{Time: at, Valid: true}}}},
		"an object naming no value":     {Objects: []HistoryObject{{ResourceType: "pool"}}},
		"a resource id with no key":     {Objects: []HistoryObject{{ResourceType: "pbs_server", ResourceID: "s1", Value: "x"}}},
		"a list with no key":            {Objects: []HistoryObject{{ResourceType: "pve_acl", Value: "u@pve", InList: true}}},
		"a lone-backslash worker":       {Guests: []HistoryGuest{{VMID: 100}}, GuestWorkers: []string{`ha\`}},
		"a guest's object, not a guest": {Guests: []HistoryGuest{{VMID: 100}}, Objects: []HistoryObject{{ResourceType: "vm", Value: "101"}}},
		"a guest's object by row id":    {Objects: []HistoryObject{{ResourceType: "container", Value: "d0000000-0000-4000-8000-000000000001"}}},
		"an undeclared type":            {Objects: []HistoryObject{{ResourceType: "replication", Value: "job01"}}},
	} {
		if _, err := k.ListParams([16]byte{1}, sel); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// A global kind reads every cluster: never a guest, nor an object whose id
	// is unique only within one.
	global := HistoryKind{Name: "probe", Categories: []string{"other"}, Default: "other", Global: true,
		ObjectTypes: []string{"pbs_server"}}
	if p, err := global.ListParams([16]byte{1}, HistorySelection{Objects: []HistoryObject{{ResourceType: "pbs_server", Value: "s1"}}}); err != nil ||
		!p.Global || p.ClusterID != uuid.Nil {
		t.Errorf("global ListParams: global %v, cluster %v, err %v; want every cluster", p.Global, p.ClusterID, err)
	}
	for name, sel := range map[string]HistorySelection{
		"a guest":           {Guests: []HistoryGuest{{VMID: 100}}},
		"a node, by name":   {Objects: []HistoryObject{{ResourceType: "node", Value: "pve-01"}}},
		"a guest's workers": {GuestWorkers: []string{"ha%"}},
		"a key, no parent":  {Objects: []HistoryObject{{ResourceType: "pbs_server", DetailKey: "datastore", Value: "datastore01"}}},
	} {
		if _, err := global.ListParams([16]byte{1}, sel); err == nil {
			t.Errorf("a global kind selecting %s was accepted", name)
		}
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

// A kind's guests are kept only where the caller may open them; one the
// inventory no longer holds needs both guest permissions.
func TestViewableGuests(t *testing.T) {
	t.Parallel()
	guests := []HistoryGuest{{VMID: 100}, {VMID: 101}, {VMID: 102}}
	types := []db.ListGuestTypesRow{{Vmid: 100, Type: "qemu"}, {Vmid: 101, Type: "lxc"}}
	for _, tt := range []struct {
		viewable []string
		want     []int32
	}{
		{[]string{}, []int32{}},
		{[]string{"qemu"}, []int32{100}},
		{[]string{"lxc"}, []int32{101}},
		{[]string{"qemu", "lxc"}, []int32{100, 101, 102}},
	} {
		got := []int32{}
		for _, g := range viewableGuests(guests, types, tt.viewable) {
			got = append(got, g.VMID)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("viewable %v: kept %v, want %v", tt.viewable, got, tt.want)
		}
	}
}

// List drops them before the query: a caller with view:vm alone sees the
// folder's VM and not its container — not even that the folder holds one.
func TestHistoryListDropsGuestsTheCallerCannotOpen(t *testing.T) {
	t.Parallel()
	cluster := uuid.New()
	dbtx := &auditRowsDBTX{rows: []any{db.ListGuestTypesRow{Vmid: 100, Type: "qemu"}, db.ListGuestTypesRow{Vmid: 101, Type: "lxc"}}}
	kind := HistoryKind{Name: "folder", Categories: []string{"other"}, Default: "other",
		ObjectTypes: []string{"vm", "container"},
		Select: func(context.Context, HistoryRequest) (HistorySelection, error) {
			return HistorySelection{Guests: []HistoryGuest{{VMID: 100}, {VMID: 101}},
				Objects: []HistoryObject{{ResourceType: "vm", Value: "100"}, {ResourceType: "container", Value: "101"}}}, nil
		}}
	props := historyTestProps("cluster_id", apischema.StdOption("cluster-id"))
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("rbac_engine", clusterGrantEngine{action: "view", resource: "vm", cluster: cluster})
		return c.Next()
	})
	app.Get("/clusters/:cluster_id/probe", withRequestParams(t, props, []string{"cluster_id"},
		NewHistoryHandler(dbtx, db.New(dbtx)).Route(kind).List))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/clusters/"+cluster.String()+"/probe", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	for _, call := range dbtx.calls {
		if !strings.Contains(call.sql, "name: ListHistory ") {
			continue
		}
		// …and the container's own object goes with it, rather than the
		// History failing over it.
		saw := 0
		for _, arg := range call.args {
			// The guests are the one []int32 that is not empty: the runs' gaps
			// are one too, and this kind collapses nothing.
			if vmids, ok := arg.([]int32); ok && len(vmids) > 0 {
				saw++
				if !slices.Equal(vmids, []int32{100}) {
					t.Errorf("ListHistory was asked for guests %v, want only the VM [100]", vmids)
				}
			}
			if values, ok := arg.([]string); ok && slices.Contains(values, "101") {
				t.Errorf("ListHistory was asked for the dropped container's object: %v", values)
			}
		}
		if saw != 1 {
			t.Fatalf("ListHistory ran with %d guest lists among %d arguments, want one", saw, len(call.args))
		}
		return
	}
	t.Fatalf("ListHistory never ran: %d queries", len(dbtx.calls))
}

// A global kind's scope is global, as its gate is: it names no cluster, and
// asks no permission of one.
func TestHistoryListGlobalKindAsksNoCluster(t *testing.T) {
	t.Parallel()
	engine := &recordingEngine{actions: map[string]bool{"view": true}}
	dbtx := &auditRowsDBTX{}
	kind := HistoryKind{Name: "probe", Categories: []string{"other"}, Default: "other", Global: true,
		Select: SelectObjects("id", HistoryObject{ResourceType: "pbs_server"}), ObjectTypes: []string{"pbs_server"}}
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("rbac_engine", engine)
		return c.Next()
	})
	app.Get("/probes/:id/history", withRequestParams(t, historyTestProps("id", apischema.Property{Type: apischema.String}),
		[]string{"id"}, NewHistoryHandler(dbtx, db.New(dbtx)).Route(kind).List))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/probes/"+uuid.NewString()+"/history", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(dbtx.calls) != 1 || len(engine.checks) != 0 || dbtx.clusterLookups != 0 {
		t.Errorf("status %d, %d queries, cluster checks %v, %d cluster lookups; want 200, ListHistory, none, none",
			resp.StatusCode, len(dbtx.calls), engine.checks, dbtx.clusterLookups)
	}
}

// historyTestProps mirrors the History route's parameters: the path's own
// one, and the filters every History takes.
func historyTestProps(name string, path apischema.Property) apischema.Properties {
	return apischema.Properties{
		name:         path,
		"limit":      {Type: apischema.Integer, Optional: true, Default: 50},
		"offset":     {Type: apischema.Integer, Optional: true, Default: 0},
		"start_time": {Type: apischema.String, Optional: true},
		"category":   {Type: apischema.String, Optional: true},
		"source":     {Type: apischema.String, Optional: true},
		// Optional with no default: omitted, it is "".
		"exclude_category": {Type: apischema.String, Optional: true},
		"all_time":         {Type: apischema.Boolean, Optional: true, Default: false},
	}
}

// A guest-from-path kind skips the guest-permission filter for the path's
// guest alone: a selection of any other guest is refused before the query.
func TestHistoryListRefusesAGuestBesideThePath(t *testing.T) {
	t.Parallel()
	dbtx := &auditRowsDBTX{}
	kind := GuestHistory()
	kind.Select = func(context.Context, HistoryRequest) (HistorySelection, error) {
		return HistorySelection{Guests: []HistoryGuest{{VMID: 101}}}, nil
	}
	cluster := uuid.New()
	props := historyTestProps("cluster_id", apischema.StdOption("cluster-id"))
	props["vmid"] = apischema.Property{Type: apischema.Integer}
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("rbac_engine", clusterGrantEngine{action: "view", resource: "vm", cluster: cluster})
		return c.Next()
	})
	app.Get("/clusters/:cluster_id/guests/:vmid/history", withRequestParams(t, props, []string{"cluster_id", "vmid"},
		NewHistoryHandler(dbtx, db.New(dbtx)).Route(kind).List))
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/clusters/"+cluster.String()+"/guests/100/history", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError || len(dbtx.calls) != 0 {
		t.Errorf("status %d after %d queries, want 500 before any", resp.StatusCode, len(dbtx.calls))
	}
}

// A cluster's read is capped, 2,000 rows a source unfiltered and 20,000
// narrowed, for a caller without a guest type too (the arms apply it), and no
// page past the cap is served; a node's and a folder's at 20,000, all time
// too. With no start_time each reads the last week, unless it asks for
// all_time. ListHistory alone is planned for its own arguments (HistoryDBTX):
// the folder's own read before it is not.
func TestHistoryListBoundsACappedRead(t *testing.T) {
	t.Parallel()
	// Where each bound sits among the query's arguments.
	probe := &auditRowsDBTX{}
	since := pgtype.Timestamptz{Time: time.Unix(1_000_000, 0), Valid: true}
	if _, err := db.New(HistoryDBTX(probe)).ListHistory(context.Background(), db.ListHistoryParams{
		RowCap: 11, RowOffset: 22, RowLimit: 33, Since: since}); err != nil {
		t.Fatal(err)
	}
	at := func(v any) int { return slices.Index(probe.calls[0].args, v) }
	atCap, atOffset, atLimit, atSince := at(int32(11)), at(int32(22)), at(int32(33)), at(since)
	cluster := uuid.New()
	every := func() permissionEngine { return &recordingEngine{actions: map[string]bool{"view": true}} }
	week := time.Now().Add(-7 * 24 * time.Hour)
	type route struct {
		kind       HistoryKind
		path, name string // the route's own path parameter, after cluster_id
	}
	clusterRoute := route{ClusterHistory(), "/history", ""}
	nodeRoute := route{NodeHistory(), "/nodes/:node_name/history", "node_name"}
	folderRoute := route{FolderHistory(), "/vm-folders/:folder_id/history", "folder_id"}
	for _, tt := range []struct {
		name, query        string
		route              route
		engine             permissionEngine
		cap, offset, limit int32
		window             bool // the last week; else no bound
	}{
		{"unfiltered", "", clusterRoute, every(), 2_000, 0, 50, true},
		{"a category", "?category=sync", clusterRoute, every(), 20_000, 0, 50, true},
		{"categories left out", "?exclude_category=other", clusterRoute, every(), 20_000, 0, 50, true},
		{"a source", "?source=nexara", clusterRoute, every(), 20_000, 0, 50, true},
		{"a caller without view:container", "", clusterRoute,
			clusterGrantEngine{action: "view", resource: "vm", cluster: cluster}, 2_000, 0, 50, true},
		{"the last page within the cap", "?offset=1980", clusterRoute, every(), 2_000, 1_980, 20, true},
		{"a page past the cap", "?offset=2050", clusterRoute, every(), 2_000, 2_000, 0, true},
		{"all time", "?all_time=true", clusterRoute, every(), 2_000, 0, 50, false},
		{"a node, all time", "?all_time=true", nodeRoute, every(), 20_000, 0, 50, false},
		{"a node, a page past the cap", "?offset=20050", nodeRoute, every(), 20_000, 20_000, 0, true},
		{"a folder", "", folderRoute, every(), 20_000, 0, 50, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dbtx := &auditRowsDBTX{}
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.New())
				c.Locals("rbac_engine", tt.engine)
				return c.Next()
			})
			props, names, path := historyTestProps("cluster_id", apischema.StdOption("cluster-id")),
				[]string{"cluster_id"}, "/clusters/"+cluster.String()+tt.route.path
			if n := tt.route.name; n != "" {
				// A node named pve-01; the folder of the guests in none, which
				// reads no folder.
				props[n], names = apischema.Property{Type: apischema.String}, append(names, n)
				path = strings.Replace(path, ":"+n, map[string]string{"node_name": "pve-01", "folder_id": UnfiledFolderSegment}[n], 1)
				props["include_guests"] = apischema.Property{Type: apischema.Boolean, Optional: true, Default: false}
			}
			app.Get("/clusters/:cluster_id"+tt.route.path, withRequestParams(t, props, names,
				NewHistoryHandler(dbtx, db.New(dbtx)).Route(tt.route.kind).List))
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, path+tt.query, nil))
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if n := len(dbtx.calls); n == 0 || !strings.Contains(dbtx.calls[n-1].sql, "name: ListHistory ") {
				t.Fatalf("status %d after %d queries, want ListHistory last", resp.StatusCode, n)
			}
			args := dbtx.calls[len(dbtx.calls)-1].args
			for i, call := range dbtx.calls {
				var mode pgx.QueryExecMode
				if len(call.args) > 0 {
					mode, _ = call.args[0].(pgx.QueryExecMode)
				}
				if (mode == pgx.QueryExecModeCacheDescribe) != (i == len(dbtx.calls)-1) {
					t.Errorf("query %d of %d ran with exec mode %v; want CacheDescribe for ListHistory alone", i+1,
						len(dbtx.calls), call.args[0])
				}
			}
			if args[atCap] != tt.cap || args[atOffset] != tt.offset || args[atLimit] != tt.limit {
				t.Errorf("cap %v, offset %v, limit %v; want %d, %d, %d", args[atCap], args[atOffset], args[atLimit],
					tt.cap, tt.offset, tt.limit)
			}
			got, _ := args[atSince].(pgtype.Timestamptz)
			if inWeek := got.Valid && got.Time.Sub(week).Abs() < time.Minute; inWeek != tt.window || !tt.window && got.Valid {
				t.Errorf("since %v, want the last week: %v", got, tt.window)
			}
		})
	}
}

// A capped read says how far back it is whole, an empty page of one too —
// whose one row carries no entry, and is no item — and a selection that
// follows only some of its guests says how many of how many.
func TestHistoryListSaysHowFarBackItSearched(t *testing.T) {
	t.Parallel()
	at := pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true}
	entry := db.ListHistoryRow{EntryID: "audit:1", Kind: "audit", ResourceType: "node", Details: json.RawMessage(`{}`),
		Total: 7, SearchedFrom: at}
	following := ClusterHistory()
	following.WholeCluster, following.Collapse = false, nil
	following.Select = func(context.Context, HistoryRequest) (HistorySelection, error) {
		return HistorySelection{GuestsTotal: 600}, nil
	}
	for _, tt := range []struct {
		name       string
		kind       HistoryKind
		rows       []any
		wantItems  int
		wantTotal  int64
		wantGuests string
	}{
		{"a page", ClusterHistory(), []any{entry}, 1, 7, ""},
		{"an empty page", ClusterHistory(), []any{db.ListHistoryRow{Details: json.RawMessage(`{}`), SearchedFrom: at}},
			0, 0, ""},
		{"some of its guests", following, []any{entry}, 1, 7, `{"followed":0,"total":600}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dbtx := &auditRowsDBTX{rows: tt.rows}
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.New())
				c.Locals("rbac_engine", &recordingEngine{actions: map[string]bool{"view": true}})
				return c.Next()
			})
			app.Get("/clusters/:cluster_id/history", withRequestParams(t,
				historyTestProps("cluster_id", apischema.StdOption("cluster-id")), []string{"cluster_id"},
				NewHistoryHandler(dbtx, db.New(dbtx)).Route(tt.kind).List))
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/clusters/"+uuid.NewString()+"/history", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			var body struct {
				Items        []historyEntry  `json:"items"`
				Total        int64           `json:"total"`
				SearchedFrom string          `json:"searched_from"`
				Guests       json.RawMessage `json:"guests"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("status %d: %v", resp.StatusCode, err)
			}
			if guests := string(body.Guests); len(body.Items) != tt.wantItems || body.Total != tt.wantTotal ||
				body.SearchedFrom != "2026-01-02T03:04:05Z" || guests != tt.wantGuests {
				t.Errorf("%d items, total %d, searched from %q, guests %s; want %d, %d, 2026-01-02T03:04:05Z, %s",
					len(body.Items), body.Total, body.SearchedFrom, guests, tt.wantItems, tt.wantTotal, tt.wantGuests)
			}
		})
	}
}

// A kind with a guest toggle reads guest rows only when the request sets it;
// any other kind reads them always.
func TestHistoryListPassesTheGuestToggle(t *testing.T) {
	t.Parallel()
	// Where IncludeGuests sits among the query's arguments: the one true in
	// an otherwise zero call.
	probe := &auditRowsDBTX{}
	if _, err := db.New(HistoryDBTX(probe)).ListHistory(context.Background(), db.ListHistoryParams{IncludeGuests: true}); err != nil {
		t.Fatal(err)
	}
	at := slices.Index(probe.calls[0].args, any(true))
	cluster := uuid.New()
	for _, tt := range []struct {
		name, toggle, query string
		want                bool
	}{
		{"a toggle left unset", "include_guests", "", false},
		{"a toggle set", "include_guests", "?include_guests=true", true},
		{"no toggle", "", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dbtx := &auditRowsDBTX{}
			kind := HistoryKind{Name: "probe", Categories: []string{"other"}, Default: "other", GuestToggle: tt.toggle,
				Select: SelectObjects("cluster_id", HistoryObject{ResourceType: "node"}), ObjectTypes: []string{"node"}}
			props := historyTestProps("cluster_id", apischema.StdOption("cluster-id"))
			props["include_guests"] = apischema.Property{Type: apischema.Boolean, Optional: true, Default: false}
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.New())
				c.Locals("rbac_engine", &recordingEngine{actions: map[string]bool{"view": true}})
				return c.Next()
			})
			app.Get("/clusters/:cluster_id/probe", withRequestParams(t, props, []string{"cluster_id"},
				NewHistoryHandler(dbtx, db.New(dbtx)).Route(kind).List))
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/clusters/"+cluster.String()+"/probe"+tt.query, nil))
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if len(dbtx.calls) != 1 || dbtx.calls[0].args[at] != tt.want {
				t.Errorf("status %d, %d queries; include_guests = %v, want %v", resp.StatusCode, len(dbtx.calls),
					dbtx.calls[0].args[at], tt.want)
			}
		})
	}
}
