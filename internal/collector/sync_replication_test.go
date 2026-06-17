package collector

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// replFakeClient implements the runtime interface syncReplication asserts on.
type replFakeClient struct {
	ProxmoxClient
	jobs []proxmox.ReplicationJob
	err  error
}

func (c *replFakeClient) GetReplicationJobs(context.Context) ([]proxmox.ReplicationJob, error) {
	return c.jobs, c.err
}

func TestSyncReplication(t *testing.T) {
	clusterID := uuid.New()
	mq := &mockQueries{}
	s := &Syncer{queries: mq, logger: testLogger()}

	client := &replFakeClient{jobs: []proxmox.ReplicationJob{
		{ID: "100-0", Guest: 100, Source: "pve1", Target: "pve2", FailCount: 3, Error: "connection refused"},
		{ID: "101-0", Guest: 101, Target: "pve3"},
	}}

	s.syncReplication(context.Background(), client, clusterID)

	if len(mq.replicationUpserts) != 2 {
		t.Fatalf("got %d upserts, want 2", len(mq.replicationUpserts))
	}
	got := mq.replicationUpserts[0]
	if got.ClusterID != clusterID || got.JobID != "100-0" || got.Guest != 100 ||
		got.FailCount != 3 || got.Error != "connection refused" || got.Node != "pve1" {
		t.Errorf("unexpected upsert: %+v", got)
	}
	if mq.deleteStaleReplicationCalls != 1 {
		t.Errorf("delete-stale called %d times, want 1", mq.deleteStaleReplicationCalls)
	}
}

func TestSyncReplicationUnsupportedClient(t *testing.T) {
	// A client that doesn't implement GetReplicationJobs is a safe no-op.
	mq := &mockQueries{}
	s := &Syncer{queries: mq, logger: testLogger()}
	s.syncReplication(context.Background(), &fakeProxmoxClient{}, uuid.New())
	if len(mq.replicationUpserts) != 0 || mq.deleteStaleReplicationCalls != 0 {
		t.Errorf("expected no-op for a client without GetReplicationJobs")
	}
}
