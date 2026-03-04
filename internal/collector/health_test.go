package collector

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// mockMetricQueries tracks MarkNodeOffline/Online calls.
type mockMetricQueries struct {
	offlineCalls []uuid.UUID
	onlineCalls  []uuid.UUID
	err          error
}

func (m *mockMetricQueries) MarkNodeOffline(_ context.Context, id uuid.UUID) error {
	m.offlineCalls = append(m.offlineCalls, id)
	return m.err
}

func (m *mockMetricQueries) MarkNodeOnline(_ context.Context, id uuid.UUID) error {
	m.onlineCalls = append(m.onlineCalls, id)
	return m.err
}

func TestHealthMonitor_RecordFailure_ReachesThreshold(t *testing.T) {
	mq := &mockMetricQueries{}
	h := NewHealthMonitor(mq, nil, testLogger())
	ctx := context.Background()

	clusterID := uuid.New()
	nodeID := uuid.New()

	// First two failures should not trigger offline.
	h.RecordFailure(ctx, clusterID, nodeID, "pve1")
	h.RecordFailure(ctx, clusterID, nodeID, "pve1")

	if len(mq.offlineCalls) != 0 {
		t.Errorf("expected 0 offline calls after 2 failures, got %d", len(mq.offlineCalls))
	}

	// Third failure triggers offline.
	h.RecordFailure(ctx, clusterID, nodeID, "pve1")

	if len(mq.offlineCalls) != 1 {
		t.Fatalf("expected 1 offline call after 3 failures, got %d", len(mq.offlineCalls))
	}
	if mq.offlineCalls[0] != nodeID {
		t.Errorf("offline call node_id = %s, want %s", mq.offlineCalls[0], nodeID)
	}
}

func TestHealthMonitor_RecordFailure_OnlyTriggersOnce(t *testing.T) {
	mq := &mockMetricQueries{}
	h := NewHealthMonitor(mq, nil, testLogger())
	ctx := context.Background()

	clusterID := uuid.New()
	nodeID := uuid.New()

	// Reach threshold.
	for i := 0; i < 5; i++ {
		h.RecordFailure(ctx, clusterID, nodeID, "pve1")
	}

	// Should only trigger once at the threshold, not on subsequent failures.
	if len(mq.offlineCalls) != 1 {
		t.Errorf("expected 1 offline call after 5 failures, got %d", len(mq.offlineCalls))
	}
}

func TestHealthMonitor_RecordSuccess_ResetsCounter(t *testing.T) {
	mq := &mockMetricQueries{}
	h := NewHealthMonitor(mq, nil, testLogger())
	ctx := context.Background()

	clusterID := uuid.New()
	nodeID := uuid.New()

	// Two failures, then success.
	h.RecordFailure(ctx, clusterID, nodeID, "pve1")
	h.RecordFailure(ctx, clusterID, nodeID, "pve1")
	h.RecordSuccess(ctx, nodeID)

	// Three more failures needed to reach threshold again.
	h.RecordFailure(ctx, clusterID, nodeID, "pve1")
	h.RecordFailure(ctx, clusterID, nodeID, "pve1")
	if len(mq.offlineCalls) != 0 {
		t.Errorf("expected 0 offline calls, got %d", len(mq.offlineCalls))
	}

	h.RecordFailure(ctx, clusterID, nodeID, "pve1")
	if len(mq.offlineCalls) != 1 {
		t.Errorf("expected 1 offline call, got %d", len(mq.offlineCalls))
	}
}

func TestHealthMonitor_RecordSuccess_MarksOnlineAfterThreshold(t *testing.T) {
	mq := &mockMetricQueries{}
	h := NewHealthMonitor(mq, nil, testLogger())
	ctx := context.Background()

	clusterID := uuid.New()
	nodeID := uuid.New()

	// Reach threshold.
	for i := 0; i < 3; i++ {
		h.RecordFailure(ctx, clusterID, nodeID, "pve1")
	}

	if len(mq.offlineCalls) != 1 {
		t.Fatalf("expected 1 offline call, got %d", len(mq.offlineCalls))
	}

	// Now success should mark online.
	h.RecordSuccess(ctx, nodeID)

	if len(mq.onlineCalls) != 1 {
		t.Fatalf("expected 1 online call after recovery, got %d", len(mq.onlineCalls))
	}
	if mq.onlineCalls[0] != nodeID {
		t.Errorf("online call node_id = %s, want %s", mq.onlineCalls[0], nodeID)
	}
}

func TestHealthMonitor_RecordSuccess_NoOnlineCallBelowThreshold(t *testing.T) {
	mq := &mockMetricQueries{}
	h := NewHealthMonitor(mq, nil, testLogger())
	ctx := context.Background()

	nodeID := uuid.New()

	// Success without prior threshold — no online call.
	h.RecordSuccess(ctx, nodeID)

	if len(mq.onlineCalls) != 0 {
		t.Errorf("expected 0 online calls, got %d", len(mq.onlineCalls))
	}
}

func TestHealthMonitor_MultipleNodes(t *testing.T) {
	mq := &mockMetricQueries{}
	h := NewHealthMonitor(mq, nil, testLogger())
	ctx := context.Background()

	clusterID := uuid.New()
	node1 := uuid.New()
	node2 := uuid.New()

	// Node1 reaches threshold, node2 does not.
	for i := 0; i < 3; i++ {
		h.RecordFailure(ctx, clusterID, node1, "pve1")
	}
	h.RecordFailure(ctx, clusterID, node2, "pve2")
	h.RecordFailure(ctx, clusterID, node2, "pve2")

	if len(mq.offlineCalls) != 1 {
		t.Fatalf("expected 1 offline call, got %d", len(mq.offlineCalls))
	}
	if mq.offlineCalls[0] != node1 {
		t.Errorf("offline call for %s, want %s", mq.offlineCalls[0], node1)
	}
}
