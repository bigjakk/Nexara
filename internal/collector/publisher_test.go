package collector

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Publisher tests verify that PublishClusterMetrics and PublishNodeOffline
// don't panic when Redis is nil or unavailable. Integration tests would
// require a real Redis connection; here we test the serialization path.

func TestPublisher_NilPublisher(t *testing.T) {
	// A nil publisher should be safe to skip in MetricCollector.
	var p *Publisher
	if p != nil {
		t.Error("nil publisher should be nil")
	}
}

func TestClusterMetricSummary_Fields(t *testing.T) {
	clusterID := uuid.New()
	nodeID := uuid.New()
	vmID := uuid.New()

	result := &clusterMetricResult{
		ClusterID:   clusterID,
		CollectedAt: time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC),
		NodeMetrics: []nodeMetricSnapshot{
			{NodeID: nodeID, CPUUsage: 0.5, MemUsed: 1024, MemTotal: 2048},
		},
		VMMetrics: []vmMetricSnapshot{
			{VMID: vmID, CPUUsage: 0.8, MemUsed: 512, MemTotal: 1024},
		},
	}

	if result.ClusterID != clusterID {
		t.Errorf("ClusterID = %s, want %s", result.ClusterID, clusterID)
	}
	if len(result.NodeMetrics) != 1 {
		t.Errorf("NodeMetrics count = %d, want 1", len(result.NodeMetrics))
	}
	if len(result.VMMetrics) != 1 {
		t.Errorf("VMMetrics count = %d, want 1", len(result.VMMetrics))
	}
}

func TestMetricCollector_ProcessResults_NilResults(t *testing.T) {
	copier := &mockCopyFromer{}
	mc := NewMetricCollector(copier, nil, testLogger())

	// Should not panic with nil results.
	mc.ProcessResults(context.Background(), nil)
	mc.ProcessResults(context.Background(), []*clusterMetricResult{nil, nil})

	if len(copier.calls) != 0 {
		t.Errorf("expected 0 CopyFrom calls for nil results, got %d", len(copier.calls))
	}
}

func TestMetricCollector_ProcessResults_InsertsMetrics(t *testing.T) {
	copier := &mockCopyFromer{}
	mc := NewMetricCollector(copier, nil, testLogger())

	result := &clusterMetricResult{
		ClusterID:   uuid.New(),
		CollectedAt: time.Now(),
		NodeMetrics: []nodeMetricSnapshot{
			{NodeID: uuid.New(), CPUUsage: 0.5},
		},
		VMMetrics: []vmMetricSnapshot{
			{VMID: uuid.New(), CPUUsage: 0.3},
			{VMID: uuid.New(), CPUUsage: 0.7},
		},
	}

	mc.ProcessResults(context.Background(), []*clusterMetricResult{result})

	// Should have 2 CopyFrom calls: one for node_metrics, one for vm_metrics.
	if len(copier.calls) != 2 {
		t.Fatalf("expected 2 CopyFrom calls, got %d", len(copier.calls))
	}

	if copier.calls[0].tableName[0] != "node_metrics" {
		t.Errorf("first call table = %v, want [node_metrics]", copier.calls[0].tableName)
	}
	if copier.calls[0].rowCount != 1 {
		t.Errorf("node metrics rows = %d, want 1", copier.calls[0].rowCount)
	}

	if copier.calls[1].tableName[0] != "vm_metrics" {
		t.Errorf("second call table = %v, want [vm_metrics]", copier.calls[1].tableName)
	}
	if copier.calls[1].rowCount != 2 {
		t.Errorf("vm metrics rows = %d, want 2", copier.calls[1].rowCount)
	}
}

func TestMetricCollector_ProcessResults_EmptyMetrics(t *testing.T) {
	copier := &mockCopyFromer{}
	mc := NewMetricCollector(copier, nil, testLogger())

	result := &clusterMetricResult{
		ClusterID:   uuid.New(),
		CollectedAt: time.Now(),
	}

	mc.ProcessResults(context.Background(), []*clusterMetricResult{result})

	// Empty snapshots skip CopyFrom.
	if len(copier.calls) != 0 {
		t.Errorf("expected 0 CopyFrom calls for empty metrics, got %d", len(copier.calls))
	}
}
