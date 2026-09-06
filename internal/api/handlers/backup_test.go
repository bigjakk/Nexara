package handlers

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

func TestBackupJobRequestSelectionKeys(t *testing.T) {
	tests := []struct {
		name string
		req  backupJobRequest
		want []string
	}{
		{"all guests", backupJobRequest{All: intPtr(1)}, []string{"all"}},
		{"vmid list", backupJobRequest{VMID: "100,101"}, []string{"vmid"}},
		{"pool", backupJobRequest{Pool: "prod"}, []string{"pool"}},
		{
			// vzdump's --exclude implies --all, so the two travel together.
			"exclusion list implies all",
			backupJobRequest{Exclude: "101"},
			[]string{"all", "exclude"},
		},
		{"all=0 is not a selection", backupJobRequest{All: intPtr(0)}, nil},
		{"nothing named", backupJobRequest{Schedule: "02:00"}, nil},
		{
			// Precedence has to match how vzdump resolves a config carrying
			// more than one selection, or the UI shows a job PVE won't run.
			"all wins over pool and vmid",
			backupJobRequest{All: intPtr(1), Pool: "prod", VMID: "100"},
			[]string{"all"},
		},
		{
			"pool wins over vmid",
			backupJobRequest{Pool: "prod", VMID: "100"},
			[]string{"pool"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.req.selectionKeys()
			if tt.want == nil {
				if got != nil {
					t.Fatalf("selectionKeys() = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("selectionKeys() = %v, want %v", got, tt.want)
			}
			for _, k := range tt.want {
				if !got[k] {
					t.Errorf("selectionKeys() missing %q (got %v)", k, got)
				}
			}
		})
	}
}

func TestBackupJobRequestClearedProperties(t *testing.T) {
	tests := []struct {
		name string
		req  backupJobRequest
		want []string
	}{
		{
			"switching to a vmid list clears the other selections",
			backupJobRequest{VMID: "100"},
			[]string{"all", "exclude", "pool"},
		},
		{
			"switching to all clears the lists",
			backupJobRequest{All: intPtr(1)},
			[]string{"vmid", "exclude", "pool"},
		},
		{
			"an exclusion job keeps all and exclude",
			backupJobRequest{Exclude: "101"},
			[]string{"vmid", "pool"},
		},
		{
			// A request naming no selection must not clear one, or a client
			// PUTting a single field would wipe the job's guest list.
			"a request with no selection clears nothing",
			backupJobRequest{Schedule: "02:00"},
			nil,
		},
		{
			// "All nodes" and "no comment" are choices the dialog can make, and
			// PVE only unsets what the delete list names.
			"an explicitly emptied node and comment are cleared",
			backupJobRequest{Node: strPtr(""), Comment: strPtr("")},
			[]string{"node", "comment"},
		},
		{
			"an omitted node and comment are left alone",
			backupJobRequest{Enabled: intPtr(0)},
			nil,
		},
		{
			"a populated node and comment are not cleared",
			backupJobRequest{Node: strPtr("pve1"), Comment: strPtr("nightly")},
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.req.clearedProperties()
			if len(got) != len(tt.want) {
				t.Fatalf("clearedProperties() = %v, want %v", got, tt.want)
			}
			for _, k := range tt.want {
				if !slices.Contains(got, k) {
					t.Errorf("clearedProperties() missing %q (got %v)", k, got)
				}
			}
		})
	}
}

func TestBackupJobRequestToParams(t *testing.T) {
	t.Run("an exclusion list is sent with all=1", func(t *testing.T) {
		params := backupJobRequest{Exclude: "101"}.toParams()
		if params.All == nil || *params.All != 1 {
			t.Errorf("All = %v, want 1", params.All)
		}
		if params.Exclude != "101" {
			t.Errorf("Exclude = %q, want %q", params.Exclude, "101")
		}
	})

	t.Run("only the winning selection is sent", func(t *testing.T) {
		// Otherwise the same property could be set and deleted in one request.
		req := backupJobRequest{All: intPtr(1), VMID: "100", Pool: "prod"}
		params := req.toParams()
		if params.VMID != "" || params.Pool != "" {
			t.Errorf("VMID = %q, Pool = %q, want both empty", params.VMID, params.Pool)
		}
		for _, cleared := range req.clearedProperties() {
			if cleared == "all" {
				t.Error("clearedProperties() deletes the selection toParams() sets")
			}
		}
	})

	t.Run("node and comment pass through both states", func(t *testing.T) {
		params := backupJobRequest{Node: strPtr("pve1"), Comment: strPtr("nightly")}.toParams()
		if params.Node != "pve1" || params.Comment != "nightly" {
			t.Errorf("Node = %q, Comment = %q", params.Node, params.Comment)
		}
		empty := backupJobRequest{}.toParams()
		if empty.Node != "" || empty.Comment != "" {
			t.Errorf("Node = %q, Comment = %q, want both empty", empty.Node, empty.Comment)
		}
	})
}

func TestBackupJobRequestAuditDetails(t *testing.T) {
	decode := func(t *testing.T, req backupJobRequest) map[string]any {
		t.Helper()
		var got map[string]any
		if err := json.Unmarshal(req.auditDetails(), &got); err != nil {
			t.Fatalf("auditDetails() is not valid JSON: %v", err)
		}
		return got
	}

	t.Run("describes a full job", func(t *testing.T) {
		got := decode(t, backupJobRequest{
			Enabled:  intPtr(1),
			Schedule: "mon,fri 02:00",
			Storage:  "pbs",
			Node:     strPtr("pve1"),
			Mode:     "snapshot",
			VMID:     "100,101",
		})
		want := map[string]any{
			"enabled":   true,
			"schedule":  "mon,fri 02:00",
			"storage":   "pbs",
			"node":      "pve1",
			"mode":      "snapshot",
			"selection": "vmids 100,101",
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("details[%q] = %v, want %v", k, got[k], v)
			}
		}
	})

	t.Run("summarises each selection mode", func(t *testing.T) {
		for _, tt := range []struct {
			req  backupJobRequest
			want string
		}{
			{backupJobRequest{All: intPtr(1)}, "all guests"},
			{backupJobRequest{Pool: "prod"}, "pool prod"},
			{backupJobRequest{Exclude: "101"}, "all guests except 101"},
		} {
			if got := decode(t, tt.req)["selection"]; got != tt.want {
				t.Errorf("selection = %v, want %q", got, tt.want)
			}
		}
	})

	t.Run("a partial update records only what it changed", func(t *testing.T) {
		// A row claiming the job was left enabled and scoped to all guests
		// would misreport a request that touched neither.
		got := decode(t, backupJobRequest{Enabled: intPtr(0)})
		if enabled, ok := got["enabled"]; !ok || enabled != false {
			t.Errorf("details[enabled] = %v, want false", got["enabled"])
		}
		for _, absent := range []string{"selection", "schedule", "storage", "node", "mode"} {
			if _, ok := got[absent]; ok {
				t.Errorf("details contains %q for a request that did not set it", absent)
			}
		}
	})
}

func TestFilterPruneJobsByStore(t *testing.T) {
	jobs := []proxmox.PBSPruneJob{
		{ID: "a", Store: "Test-Backup-Datastore"},
		{ID: "b", Store: "PBS-Test-Datastore"},
		{ID: "c", Store: "Test-Backup-Datastore"},
	}

	tests := []struct {
		name    string
		store   string
		wantIDs []string
	}{
		{"empty store is no filter", "", []string{"a", "b", "c"}},
		{"narrows to one datastore", "Test-Backup-Datastore", []string{"a", "c"}},
		{"unknown store yields nothing", "nope", []string{}},
		// Datastore names are case-sensitive in PBS; a near-miss must not match.
		{"case-sensitive", "test-backup-datastore", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterPruneJobsByStore(jobs, tt.store)
			if got == nil {
				t.Fatal("returned nil, want a non-nil slice")
			}
			var ids []string
			for _, j := range got {
				ids = append(ids, j.ID)
			}
			if strings.Join(ids, ",") != strings.Join(tt.wantIDs, ",") {
				t.Errorf("ids = %v, want %v", ids, tt.wantIDs)
			}
		})
	}
}

// The bug this guards: GetDatastoreMetrics used to hand the raw hypertable
// straight to the client. At the 10s collection interval that docker-compose
// ships, a 7-day window is tens of thousands of rows per datastore — a 6.4MB
// response that froze the browser for ~10s while it parsed and laid the points
// out, taking the rest of the SPA with it. Bucketing is what keeps the
// response bounded, so the invariant worth pinning is the point count, not the
// individual constants.
//
// This ranges over the timeframe table rather than restating it, so a
// timeframe added later is held to the same bound without a new test row.
func TestDatastoreMetricsWindowBounded(t *testing.T) {
	// A few hundred points is already finer than the pixels available to draw
	// them; well past that and we are shipping data no one can see.
	const maxPointsPerDatastore = 256

	for timeframe := range datastoreMetricsTimeframes {
		t.Run(timeframe, func(t *testing.T) {
			window, bucketSeconds := datastoreMetricsWindow(timeframe)

			if bucketSeconds <= 0 {
				// time_bucket() rejects a zero or negative width outright, so
				// this would be a 500 on every metrics request rather than a
				// silent fallback to raw rows.
				t.Fatalf("bucketSeconds = %d, must be positive", bucketSeconds)
			}
			if window <= 0 {
				t.Fatalf("window = %v, must be positive", window)
			}

			points := int64(window/time.Second) / int64(bucketSeconds)
			if points > maxPointsPerDatastore {
				t.Errorf("timeframe %q yields %d points per datastore "+
					"(window %v / bucket %ds), want <= %d",
					timeframe, points, window, bucketSeconds,
					maxPointsPerDatastore)
			}
		})
	}
}

// An unrecognised ?timeframe= must still resolve to a bounded window — the
// query is driven by whatever the client sends.
func TestDatastoreMetricsWindowFallback(t *testing.T) {
	fallback, ok := datastoreMetricsTimeframes[datastoreMetricsDefaultTimeframe]
	if !ok {
		t.Fatalf("default timeframe %q is not in the table",
			datastoreMetricsDefaultTimeframe)
	}

	for _, timeframe := range []string{"", "not-a-timeframe", "1H", "30d"} {
		t.Run("falls back: "+timeframe, func(t *testing.T) {
			window, bucketSeconds := datastoreMetricsWindow(timeframe)
			if window != fallback.window || bucketSeconds != fallback.bucketSeconds {
				t.Errorf("datastoreMetricsWindow(%q) = (%v, %d), want (%v, %d)",
					timeframe, window, bucketSeconds,
					fallback.window, fallback.bucketSeconds)
			}
		})
	}
}
