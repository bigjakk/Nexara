package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// mountPBSAuditRoutes mounts the PBS datastore actions whose audit rows
// TestPBSDatastoreActionsAreAuditedUnderTheirServer reads, with the parameters
// registry_backup.go declares for them, mirrored for the reason
// pbsTaskUPIDMirror gives.
func mountPBSAuditRoutes(t *testing.T, app *fiber.App, h *BackupHandler) {
	t.Helper()
	pbsID := apischema.StdOption("pbs-id")
	name := apischema.Property{Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(64)}
	snapshot := apischema.Properties{
		"pbs_id":      pbsID,
		"store":       name,
		"backup_type": {Type: apischema.String},
		"backup_id":   {Type: apischema.String},
		"backup_time": {Type: apischema.Integer},
		"protected":   {Type: apischema.Boolean},
	}

	const base = "/api/v1/pbs-servers/:pbs_id"
	app.Post(base+"/datastores/:store/gc", withRequestParams(t,
		apischema.Properties{"pbs_id": pbsID, "store": name}, []string{"pbs_id", "store"}, h.TriggerGC))
	app.Post(base+"/sync-jobs/:job_id/run", withRequestParams(t,
		apischema.Properties{"pbs_id": pbsID, "job_id": name}, []string{"pbs_id", "job_id"}, h.RunSyncJob))
	app.Post(base+"/verify-jobs/:job_id/run", withRequestParams(t,
		apischema.Properties{"pbs_id": pbsID, "job_id": name}, []string{"pbs_id", "job_id"}, h.RunVerifyJob))
	app.Put(base+"/datastores/:store/snapshots/protect", withRequestParams(t,
		snapshot, []string{"pbs_id", "store"}, h.ProtectSnapshot))
}

// A datastore action is audited under its SERVER: pbs_server, the server's id,
// the datastore in details. A datastore's name is unique only within a server,
// so the "backup" row filed under the name named no server a History could find.
// The row is labelled (resource_name) by datastore, snapshot or job, and the PBS
// task a trigger started is pbs_upid, not the upid the feed opens as a PVE task.
// A job's datastore is the one the collector last synced for it, if any.
func TestPBSDatastoreActionsAreAuditedUnderTheirServer(t *testing.T) {
	t.Parallel()

	const protectBody = `{"backup_type":"vm","backup_id":"101","backup_time":1700000000,"protected":true}`
	tests := []struct {
		name, method, path, body string
		action                   string
		details                  map[string]any
	}{
		{
			name: "garbage collection", method: http.MethodPost, path: "/datastores/datastore01/gc",
			action:  "gc_triggered",
			details: map[string]any{"datastore": "datastore01", "resource_name": "datastore01", "pbs_upid": pbsUPIDSample},
		},
		{
			name: "sync job", method: http.MethodPost, path: "/sync-jobs/sync01/run",
			action:  "sync_job_triggered",
			details: map[string]any{"datastore": "datastore01", "job_id": "sync01", "resource_name": "sync01", "pbs_upid": pbsUPIDSample},
		},
		{
			name: "sync job the collector has not synced yet", method: http.MethodPost, path: "/sync-jobs/sync02/run",
			action:  "sync_job_triggered",
			details: map[string]any{"job_id": "sync02", "resource_name": "sync02", "pbs_upid": pbsUPIDSample},
		},
		{
			name: "verify job", method: http.MethodPost, path: "/verify-jobs/verify01/run",
			action:  "verify_job_triggered",
			details: map[string]any{"datastore": "datastore02", "job_id": "verify01", "resource_name": "verify01", "pbs_upid": pbsUPIDSample},
		},
		{
			name: "snapshot protect", method: http.MethodPut, path: "/datastores/datastore01/snapshots/protect",
			body: protectBody, action: "snapshot_protected",
			details: map[string]any{
				"datastore": "datastore01", "resource_name": "datastore01/vm/101", "backup_type": "vm",
				"backup_id": "101", "backup_time": float64(1700000000), "protected": true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newPBSUPIDHarness(t)
			h.dbtx.jobs = map[string][]any{
				"ListPBSSyncJobsByServer":   {db.PbsSyncJob{PbsServerID: h.pbsID, JobID: "sync01", Store: "datastore01"}},
				"ListPBSVerifyJobsByServer": {db.PbsVerifyJob{PbsServerID: h.pbsID, JobID: "verify01", Store: "datastore02"}},
			}

			req := httptest.NewRequest(tt.method, "/api/v1/pbs-servers/"+h.pbsID.String()+tt.path, strings.NewReader(tt.body))
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := h.app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 — the action never reached its audit", resp.StatusCode)
			}

			audits := h.dbtx.auditInserts(t)
			if len(audits) != 1 {
				t.Fatalf("the action wrote %d audit rows, want exactly 1", len(audits))
			}
			got := audits[0]
			if got.ResourceType != "pbs_server" || got.ResourceID != h.pbsID.String() || got.Action != tt.action {
				t.Errorf("row is (%q, %q, %q), want (pbs_server, %s, %s)",
					got.ResourceType, got.ResourceID, got.Action, h.pbsID, tt.action)
			}
			var details map[string]any
			if err := json.Unmarshal(got.Details, &details); err != nil {
				t.Fatalf("details are not a JSON object: %v (%s)", err, got.Details)
			}
			if !reflect.DeepEqual(details, tt.details) {
				t.Errorf("details = %s\nwant %v", got.Details, tt.details)
			}
		})
	}
}
