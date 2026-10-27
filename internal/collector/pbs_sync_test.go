package collector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// pbsFakeClient answers the reads syncPBSServer makes: one job of each type,
// or the listing's error.
type pbsFakeClient struct {
	PBSProxmoxClient
	syncErr, verifyErr error
}

func (c *pbsFakeClient) GetDatastoreStatus(context.Context) ([]proxmox.PBSDatastoreStatus, error) {
	return nil, nil
}

func (c *pbsFakeClient) GetDatastores(context.Context) ([]proxmox.PBSDatastore, error) {
	return nil, nil
}

func (c *pbsFakeClient) GetSyncJobs(context.Context) ([]proxmox.PBSSyncJob, error) {
	return []proxmox.PBSSyncJob{{ID: "s-01", Store: "datastore01"}}, c.syncErr
}

func (c *pbsFakeClient) GetVerifyJobs(context.Context) ([]proxmox.PBSVerifyJob, error) {
	return []proxmox.PBSVerifyJob{{ID: "v-01", Store: "datastore01"}}, c.verifyErr
}

// A PBS's jobs are pruned only after a listing that was written whole: a
// listing that failed refreshed no row, so pruning then deleted every job of a
// PBS unreachable for longer than the grace, and a job that failed to write
// would be pruned while the PBS still has it.
func TestSyncPBSServerPrunesJobsOnlyAfterAWholeListing(t *testing.T) {
	t.Parallel()
	failed := errors.New("no answer")
	secret, err := crypto.Encrypt("secret-token-value", testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name                   string
		syncErr, verifyErr     error
		upsertErr              error
		wantSyncs, wantVerifys int
		wantMarks              string // the job types marked read whole (migrations/000130)
	}{
		{name: "both listed and written", wantSyncs: 1, wantVerifys: 1, wantMarks: "sync verify"},
		{name: "both listings failed", syncErr: failed, verifyErr: failed},
		{name: "a sync job not written", upsertErr: failed, wantVerifys: 1, wantMarks: "verify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mq := &mockQueries{pbsSyncJobUpsertErr: tc.upsertErr}
			s := &Syncer{
				queries:       mq,
				encryptionKey: testEncryptionKey,
				pbsClientFactory: func(string, string, string, string) (PBSProxmoxClient, error) {
					return &pbsFakeClient{syncErr: tc.syncErr, verifyErr: tc.verifyErr}, nil
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
