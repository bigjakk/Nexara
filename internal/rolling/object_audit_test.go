package rolling

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// auditDB is a db.DBTX that records the audit rows the engine writes, accepts the
// two CRS markers, and fails the test for anything else.
type auditDB struct {
	t    *testing.T
	mu   sync.Mutex
	rows []auditRow
}

type auditRow struct {
	resourceType, resourceID, action string
	details                          map[string]any
}

func (d *auditDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch name := queryName(sql); name {
	case "InsertAuditLog":
		row := auditRow{details: map[string]any{}}
		row.resourceType, _ = args[2].(string)
		row.resourceID, _ = args[3].(string)
		row.action, _ = args[4].(string)
		raw, _ := args[5].(json.RawMessage)
		_ = json.Unmarshal(raw, &row.details)
		d.mu.Lock()
		d.rows = append(d.rows, row)
		d.mu.Unlock()
	case "SetJobNativeCRSPaused", "ClearJobNativeCRSPaused":
	default:
		d.t.Errorf("unexpected write %s", name)
		return pgconn.CommandTag{}, errors.New("unexpected")
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (d *auditDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.t.Errorf("unexpected query %s", queryName(sql))
	return nil, errors.New("unexpected")
}

func (d *auditDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	d.t.Errorf("unexpected query row %s", queryName(sql))
	return rowErr{errors.New("unexpected")}
}

// rowsOf returns what the engine wrote for resourceType.
func (d *auditDB) rowsOf(resourceType string) []auditRow {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []auditRow
	for _, r := range d.rows {
		if r.resourceType == resourceType {
			out = append(out, r)
		}
	}
	return out
}

// TestRollingUpdateFilesTheObjectsItChanges: the update disables HA rules and
// pauses the CRS option, and restores both. Each is filed under the object's own
// key (ha_rule/<name>, cluster_options/<cluster>) beside the job's rolling_update
// row, as the handlers file the same change; a change Proxmox refused is not.
func TestRollingUpdateFilesTheObjectsItChanges(t *testing.T) {
	t.Parallel()
	clusterID, jobID := uuid.New(), uuid.New()
	job := db.RollingUpdateJob{ID: jobID, ClusterID: clusterID, SavedCrsConfig: "ha=static,ha-auto-rebalance=1"}

	// setup stands up the engine on a stub Proxmox that refuses the rules named in
	// refuse (and the options write, if "options" is), lists the cluster options
	// with auto-rebalance on, and says which rules exist.
	setup := func(t *testing.T, refuse ...string) (*Orchestrator, *proxmox.Client, *auditDB) {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api2/json/cluster/ha/rules/"):
				for _, name := range refuse {
					if strings.HasSuffix(r.URL.Path, "/"+name) {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = w.Write([]byte(`{"data":null,"message":"refused\n"}`))
						return
					}
				}
				_, _ = w.Write([]byte(`{"data":null}`))
			case r.URL.Path == "/api2/json/cluster/ha/rules":
				_, _ = w.Write([]byte(`{"data":[{"rule":"refused-rule","type":"node-affinity","disable":1}]}`))
			case r.Method == http.MethodGet && r.URL.Path == "/api2/json/cluster/options":
				_, _ = w.Write([]byte(`{"data":{"crs":"ha=static,ha-auto-rebalance=1"}}`))
			case r.Method == http.MethodPut && r.URL.Path == "/api2/json/cluster/options":
				if slices.Contains(refuse, "options") {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"data":null,"message":"refused\n"}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":null}`))
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		client, err := proxmox.NewClient(proxmox.ClientConfig{BaseURL: srv.URL, TokenID: "user@pam!test", TokenSecret: "secret"})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		d := &auditDB{t: t}
		o := NewOrchestrator(context.Background(), db.New(d), "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
		return o, client, d
	}
	rules := []DisabledHARule{{Rule: "ok-rule", Type: "resource-affinity"}, {Rule: "refused-rule", Type: "node-affinity"}}
	ctx := context.Background()

	t.Run("a rule disabled before a drain", func(t *testing.T) {
		t.Parallel()
		o, client, d := setup(t, "refused-rule")
		o.disableHARules(ctx, client, job, "pve-01", rules)
		got := d.rowsOf("ha_rule")
		if len(got) != 1 || got[0].resourceID != "ok-rule" || got[0].action != "updated" ||
			got[0].details["disable"] != float64(1) || got[0].details["job_id"] != jobID.String() ||
			got[0].details["by"] != "rolling_update" || got[0].details["node"] != "pve-01" {
			t.Errorf("ha_rule rows = %+v, want one update with disable=1 for ok-rule only", got)
		}
		if n := len(d.rowsOf("rolling_update")); n != 1 {
			t.Errorf("%d rolling_update rows, want the job's one", n)
		}
	})

	t.Run("rules restored, one refused and kept", func(t *testing.T) {
		t.Parallel()
		o, client, d := setup(t, "refused-rule")
		ok := o.releaseRuleRecord(ctx, client, job, rules, "", true, func([]DisabledHARule) bool { return true })
		got := d.rowsOf("ha_rule")
		if ok || len(got) != 1 || got[0].resourceID != "ok-rule" || got[0].details["disable"] != float64(0) {
			t.Errorf("released = %v, ha_rule rows = %+v, want one update with disable=0 for ok-rule only", ok, got)
		}
	})

	t.Run("CRS paused and restored", func(t *testing.T) {
		t.Parallel()
		o, client, d := setup(t)
		o.pauseNativeCRSIfActive(ctx, client, job)
		if restored := o.restoreNativeCRS(ctx, client, job, true); !restored {
			t.Fatal("restoreNativeCRS = false")
		}
		got := d.rowsOf("cluster_options")
		if len(got) != 2 {
			t.Fatalf("%d cluster_options rows, want a pause and a restore", len(got))
		}
		for _, row := range got {
			settings, _ := row.details["settings"].([]any)
			if row.resourceID != clusterID.String() || row.action != "updated" || len(settings) != 1 || settings[0] != "crs" ||
				row.details["job_id"] != jobID.String() {
				t.Errorf("cluster_options row = %+v, want an update of crs for the cluster, by this job", row)
			}
		}
	})

	t.Run("a CRS restore Proxmox refuses files nothing", func(t *testing.T) {
		t.Parallel()
		o, client, d := setup(t, "options")
		if restored := o.restoreNativeCRS(ctx, client, job, false); restored {
			t.Error("restoreNativeCRS = true for a refused write")
		}
		if n := len(d.rowsOf("cluster_options")); n != 0 {
			t.Errorf("%d cluster_options rows for a write that did not happen", n)
		}
	})
}
