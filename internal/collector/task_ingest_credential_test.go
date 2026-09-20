package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The third door onto the same credential.
//
// A cross-cluster migration hands Proxmox the target cluster's decrypted API
// token in the `target-endpoint` property string, and PVE may echo a rejected
// parameter back. Two writers persist that text: the collector's reconciler,
// which ListRunningTaskHistoryByCluster now withholds the row from, and
// ingestTask — which writes PVE's status into task_history.exit_status AND into
// audit_log.details at INSERT, both Viewer-readable, and which 'qmigrate' does
// not escape via skipTaskTypes.
//
// The reconciler's anti-join cannot cover this one. It filters a SELECT over
// rows that already exist; ingestTask fires precisely when the row does NOT yet
// exist — in the few statements between startAndPollMigration setting
// migration_jobs.upid and its task_history/audit_log inserts landing. Both
// guards rest on that same ordering, which is what makes the second one
// possible: migration_jobs.upid is already set throughout the window.
//
// Driven through syncTasks rather than by handing ingestTask a pre-built `seen`
// map, because seenTaskUPIDs is the protected function — a test that builds the
// map itself would still pass with the lookup deleted.
func TestSyncTasks_WithholdsCrossClusterMigrationTasks(t *testing.T) {
	cluster := db.Cluster{ID: uuid.New()}
	taskStart := time.Now().Add(-time.Minute).Unix()

	const (
		crossUPID = "UPID:pve-01:00002001:00000001:67000001:qmigrate:100:nexara@pve!api:"
		plainUPID = "UPID:pve-01:00002002:00000002:67000002:qmigrate:101:nexara@pve!api:"
		// What PVE's worker would say if it echoed the endpoint it was handed.
		// The fixture is the whole point: if this string stops containing the
		// secret, every "the secret is absent" assertion below goes vacuous,
		// so the test checks that too.
		secret     = "s3cr3t-target-token-value"
		dieMessage = "migration aborted: target endpoint apitoken=PVEAPIToken=" +
			"nexara@pve!api=" + secret + " refused"
	)

	newClient := func() *ingestSyncFakeClient {
		return &ingestSyncFakeClient{
			nodes: []proxmox.NodeListEntry{{Node: "pve-01", Status: "online"}},
			tasks: map[string][]proxmox.NodeTask{
				"pve-01": {
					{
						UPID: crossUPID, Type: "qmigrate", ID: "100",
						Status: dieMessage, StartTime: taskStart, EndTime: taskStart + 3,
					},
					{
						UPID: plainUPID, Type: "qmigrate", ID: "101",
						Status: "OK", StartTime: taskStart, EndTime: taskStart + 3,
					},
				},
			},
		}
	}

	if !strings.Contains(dieMessage, secret) {
		t.Fatal("the fixture die-message does not contain the secret — nothing would be tested")
	}

	t.Run("the migration's task is not recorded, the ordinary one is", func(t *testing.T) {
		q := newMockQueries()
		// What the DB would say during the window: the job row already names
		// this UPID, nothing has recorded the task yet.
		q.crossClusterUPIDs = map[string]bool{crossUPID: true}
		s := &Syncer{queries: q, logger: testLogger()}

		s.syncTasks(context.Background(), newClient(), cluster)

		for _, call := range q.externalTaskCalls {
			if call.Upid == crossUPID {
				t.Errorf("the cross-cluster migration's task was written to task_history "+
					"with exit_status %q — PVE's text reaches a view:task column here "+
					"and only internal/migration can scrub it", call.ExitStatus)
			}
			if strings.Contains(call.ExitStatus, secret) {
				t.Errorf("the target cluster's token secret reached task_history.exit_status: %q",
					call.ExitStatus)
			}
		}
		for _, call := range q.auditWithSourceCalls {
			if strings.Contains(string(call.Details), secret) {
				t.Errorf("the target cluster's token secret reached audit_log.details "+
					"(view:audit, a default Viewer grant): %s", call.Details)
			}
		}

		// The other half: the guard must withhold the migration and nothing
		// else. Without this the test would pass against a collector that had
		// simply stopped ingesting.
		var sawPlain bool
		for _, call := range q.externalTaskCalls {
			if call.Upid == plainUPID {
				sawPlain = true
			}
		}
		if !sawPlain {
			t.Error("the ordinary qmigrate was withheld too — the guard is over-broad " +
				"and external task ingest has stopped working")
		}
	})

	t.Run("a failed lookup ingests nothing rather than ingesting unguarded", func(t *testing.T) {
		// Fail closed. The two sibling lookups already hold the watermark back
		// on error so tasks are retried; this one has the stronger reason —
		// carrying on would drop the credential guard for the whole tick.
		q := newMockQueries()
		q.crossClusterUPIDErr = errors.New("db unavailable")
		s := &Syncer{queries: q, logger: testLogger()}

		s.syncTasks(context.Background(), newClient(), cluster)

		if len(q.externalTaskCalls) != 0 {
			t.Errorf("ingested %d tasks while the cross-cluster lookup was failing; "+
				"with the guard unavailable the tick must ingest nothing",
				len(q.externalTaskCalls))
		}
		if len(q.upsertTaskSyncCalls) != 0 {
			t.Error("the watermark advanced past tasks that were never ingested — " +
				"they would be skipped forever by the since-filter")
		}
	})
}
