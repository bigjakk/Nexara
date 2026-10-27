package rolling

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

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
	setup := func(t *testing.T, refuse ...string) (*Orchestrator, *proxmox.Client, *memberDB) {
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
		d := &memberDB{t: t, audits: true}
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
