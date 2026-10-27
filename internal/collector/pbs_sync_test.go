package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// pbsFakeClient answers the reads syncPBSServer makes: two datastores of one
// snapshot each and one job of each type, or the listing's error.
type pbsFakeClient struct {
	PBSProxmoxClient
	datastoresErr, syncErr, verifyErr error
	snapshotsErrFor                   string // the datastore whose snapshots cannot be listed
}

func (c *pbsFakeClient) GetDatastoreStatus(context.Context) ([]proxmox.PBSDatastoreStatus, error) {
	return nil, nil
}

func (c *pbsFakeClient) GetDatastores(context.Context) ([]proxmox.PBSDatastore, error) {
	return []proxmox.PBSDatastore{{Name: "datastore01"}, {Name: "datastore02"}}, c.datastoresErr
}

func (c *pbsFakeClient) GetSnapshots(_ context.Context, store string) ([]proxmox.PBSSnapshot, error) {
	if store == c.snapshotsErrFor {
		return nil, errors.New("no answer")
	}
	return []proxmox.PBSSnapshot{{BackupType: "vm", BackupID: "100", BackupTime: 1}}, nil
}

func (c *pbsFakeClient) GetSyncJobs(context.Context) ([]proxmox.PBSSyncJob, error) {
	return []proxmox.PBSSyncJob{{ID: "s-01", Store: "datastore01"}}, c.syncErr
}

func (c *pbsFakeClient) GetVerifyJobs(context.Context) ([]proxmox.PBSVerifyJob, error) {
	return []proxmox.PBSVerifyJob{{ID: "v-01", Store: "datastore01"}}, c.verifyErr
}

// A PBS's jobs and snapshots are pruned only after a listing that succeeded,
// and only of what it lacked: a listing that failed refreshed no row, so
// pruning then deleted every row of a PBS unreachable for longer than the
// grace, and a row a listing showed but failed to write would be pruned while
// the PBS still has it. A datastore that did not answer is spared on its own;
// the jobs are marked read whole only when every one was written.
func TestSyncPBSServerPrunesOnlyAfterAWholeListing(t *testing.T) {
	t.Parallel()
	failed := errors.New("no answer")
	secret, err := crypto.Encrypt("secret-token-value", testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name                              string
		datastoresErr, syncErr, verifyErr error
		snapshotsErrFor, snapshotNotFor   string
		upsertErr                         error
		wantSyncs, wantVerifys            int
		wantMarks                         string // the job types marked read whole (migrations/000130)
		wantSnapshotPrunes                string // what each snapshot prune spared
		wantSyncKept                      string // the sync jobs the prune kept
	}{
		{name: "everything listed and written", wantSyncs: 1, wantVerifys: 1, wantMarks: "sync verify",
			wantSnapshotPrunes: "[unlistable [] kept []]", wantSyncKept: "[]"},
		{name: "every listing failed", datastoresErr: failed, syncErr: failed, verifyErr: failed,
			wantSnapshotPrunes: "[]", wantSyncKept: "[]"},
		{name: "a sync job and a snapshot not written", upsertErr: failed, snapshotNotFor: "datastore02",
			wantSyncs: 1, wantVerifys: 1, wantMarks: "verify", wantSyncKept: "[s-01]",
			wantSnapshotPrunes: `[unlistable [] kept [{"datastore":"datastore02","backup_type":"vm","backup_id":"100","backup_time":1}]]`},
		{name: "a datastore that did not answer", snapshotsErrFor: "datastore01", wantSyncs: 1, wantVerifys: 1,
			wantMarks: "sync verify", wantSnapshotPrunes: "[unlistable [datastore01] kept []]", wantSyncKept: "[]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mq := &mockQueries{pbsSyncJobUpsertErr: tc.upsertErr, pbsSnapshotUpsertErrFor: tc.snapshotNotFor}
			s := &Syncer{
				queries:       mq,
				encryptionKey: testEncryptionKey,
				pbsClientFactory: func(string, string, string, string) (PBSProxmoxClient, error) {
					return &pbsFakeClient{datastoresErr: tc.datastoresErr, syncErr: tc.syncErr, verifyErr: tc.verifyErr,
						snapshotsErrFor: tc.snapshotsErrFor}, nil
				},
				logger: testLogger(),
			}
			server := db.PbsServer{ID: uuid.New(), ApiUrl: "https://192.0.2.20:8007", TokenSecretEncrypted: secret}
			if _, err := s.syncPBSServer(context.Background(), server); err != nil {
				t.Fatalf("sync: %v", err)
			}
			if mq.pbsSyncJobPrunes != tc.wantSyncs || mq.pbsVerifyJobPrunes != tc.wantVerifys {
				t.Fatalf("prunes: sync %d verify %d, want %d and %d",
					mq.pbsSyncJobPrunes, mq.pbsVerifyJobPrunes, tc.wantSyncs, tc.wantVerifys)
			}
			var marks []string
			for _, m := range mq.pbsJobBaselines {
				marks = append(marks, m.JobType)
			}
			if strings.Join(marks, " ") != tc.wantMarks {
				t.Fatalf("marked %v, want %q", marks, tc.wantMarks)
			}
			if got := fmt.Sprint(mq.pbsSnapshotPrunes); got != tc.wantSnapshotPrunes {
				t.Fatalf("snapshot prunes sparing %s, want %s", got, tc.wantSnapshotPrunes)
			}
			if got := fmt.Sprint(mq.pbsSyncJobKept); got != tc.wantSyncKept {
				t.Fatalf("sync job prune kept %s, want %s", got, tc.wantSyncKept)
			}
		})
	}
}

// The PBS pass decides the global kinds' settled changes once it has run.
func TestSyncAllPBSClassifiesGlobalChanges(t *testing.T) {
	t.Parallel()
	mq := &mockQueries{}
	(&Syncer{queries: mq, logger: testLogger()}).SyncAllPBS(context.Background())
	if mq.classifyGlobalObs != 1 {
		t.Fatalf("classified %d times, want once", mq.classifyGlobalObs)
	}
}
