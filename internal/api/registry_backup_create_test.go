package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// POST /cluster/backup answers null, not the id it gave the job, so the audit
// row of a create learns the id from what the job list gained. Two new jobs, a
// list that gained nothing, or one that cannot be read in time: no id is
// recorded rather than a guess, and the create itself still succeeds.
func TestCreateBackupJobAuditsTheIDProxmoxGaveIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, before, after, wantID string
		hang                        map[int]bool
	}{
		{"the one job the list gained", `[{"id":"job-a"}]`, `[{"id":"job-a"},{"id":"job-b"}]`, "job-b", nil},
		{"two jobs the list gained", `[]`, `[{"id":"job-b"},{"id":"job-c"}]`, "backup-job", nil},
		{"no job the list gained", `[{"id":"job-a"}]`, `[{"id":"job-a"}]`, "backup-job", nil},
		{"a list unreadable before", ``, `[{"id":"job-b"}]`, "backup-job", nil},
		{"a list unreadable after", `[]`, ``, "backup-job", nil},
		{"a list that hangs before", `[]`, `[{"id":"job-b"}]`, "backup-job", map[int]bool{0: true}},
		{"a list that hangs after", `[]`, `[{"id":"job-b"}]`, "backup-job", map[int]bool{1: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pve := &backupRunPVE{listBefore: tt.before, listAfter: tt.after, hangReads: tt.hang}
			if tt.hang != nil {
				// Only a hung read needs a short bound; far above a loopback read.
				pve.readTimeout = 500 * time.Millisecond
			}
			app, dbtx := newBackupJobApp(t, pve, fiber.MethodPost, clusterScope+"/backup-jobs",
				(*handlers.BackupHandler).CreateBackupJob)

			req := httptest.NewRequest(http.MethodPost, backupRoute(clusterScope+"/backup-jobs"),
				strings.NewReader(`{"schedule":"sat 02:00","storage":"store01","all":1}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-User", "yes")
			resp, err := app.Test(req, fiber.TestConfig{Timeout: 30 * time.Second, FailOnTimeout: true})
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			if pve.creates != 1 {
				t.Fatalf("the create reached Proxmox %d times, want exactly once", pve.creates)
			}
			rows := dbtx.decodedAudits(t)
			if len(rows) != 1 {
				t.Fatalf("the create wrote %d audit rows, want exactly 1", len(rows))
			}
			got := rows[0]
			if !got.cluster.Valid || uuid.UUID(got.cluster.Bytes) != uuid.MustParse(testClusterID) ||
				got.resourceType != "backup" || got.resourceID != tt.wantID || got.action != "backup_job_created" {
				t.Errorf("row is (%v, %q, %q, %q), want (%s, backup, %s, backup_job_created)",
					got.cluster, got.resourceType, got.resourceID, got.action, testClusterID, tt.wantID)
			}
			if got.details["storage"] != "store01" {
				t.Errorf("details = %s, want the job's storage among them", got.raw)
			}
			// The job is named in details too, where its id was found.
			if known := tt.wantID != "backup-job"; known && got.details["job_id"] != tt.wantID {
				t.Errorf("details = %s, want job_id %s", got.raw, tt.wantID)
			}
		})
	}
}

// The rows of a job's change and removal name it in details as well as in the
// resource id, which is also the shape of a guest's VMID and a node's name: the
// job's History tells its rows from theirs by that and the action.
func TestBackupJobChangeRowsNameTheJob(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		method, body, action string
		handler              func(*handlers.BackupHandler, fiber.Ctx, *apischema.Params) error
	}{
		{fiber.MethodPut, `{"enabled":0}`, "backup_job_updated", (*handlers.BackupHandler).UpdateBackupJob},
		{fiber.MethodDelete, ``, "backup_job_deleted", (*handlers.BackupHandler).DeleteBackupJob},
	} {
		t.Run(tt.action, func(t *testing.T) {
			t.Parallel()
			pve := &backupRunPVE{handle: func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api2/json/cluster/backup/"+backupRunJobID {
					return false
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":null}`))
				return true
			}}
			path := clusterScope + "/backup-jobs/:job_id"
			app, dbtx := newBackupAppAs(t, pve, grantsOf("manage:backup", "delete:backup"), tt.method, path, tt.handler)

			req := httptest.NewRequest(tt.method, backupRoute(path), strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-User", "yes")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			rows := dbtx.decodedAudits(t)
			if len(rows) != 1 || rows[0].action != tt.action || rows[0].resourceID != backupRunJobID ||
				rows[0].details["job_id"] != backupRunJobID {
				t.Errorf("audit rows are %+v, want one %s row for %s naming it as job_id", rows, tt.action, backupRunJobID)
			}
		})
	}
}
