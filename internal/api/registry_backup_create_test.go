package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

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
			// A bound far above a loopback read, so only the hung read expires.
			pve := &backupRunPVE{listBefore: tt.before, listAfter: tt.after, hangReads: tt.hang, readTimeout: 500 * time.Millisecond}
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
		})
	}
}
