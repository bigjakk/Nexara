package collector

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// mockCopyFromer records CopyFrom calls for testing.
type mockCopyFromer struct {
	calls []copyFromCall
	err   error // if set, CopyFrom returns this error
}

type copyFromCall struct {
	tableName   pgx.Identifier
	columnNames []string
	rowCount    int
	rows        [][]any
}

func (m *mockCopyFromer) CopyFrom(_ context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	if m.err != nil {
		return 0, m.err
	}

	var rows [][]any
	for rowSrc.Next() {
		vals, err := rowSrc.Values()
		if err != nil {
			return 0, err
		}
		row := make([]any, len(vals))
		copy(row, vals)
		rows = append(rows, row)
	}

	call := copyFromCall{
		tableName:   tableName,
		columnNames: columnNames,
		rowCount:    len(rows),
		rows:        rows,
	}
	m.calls = append(m.calls, call)
	return int64(len(rows)), nil
}

func TestBatchInsertNodeMetrics(t *testing.T) {
	tests := []struct {
		name      string
		snapshots []nodeMetricSnapshot
		wantCount int64
		wantErr   bool
	}{
		{
			name:      "empty snapshots",
			snapshots: nil,
			wantCount: 0,
		},
		{
			name: "single node",
			snapshots: []nodeMetricSnapshot{
				{NodeID: uuid.New(), CPUUsage: 0.5, MemUsed: 1024, MemTotal: 2048, DiskRead: 100, DiskWrite: 200, NetIn: 300, NetOut: 400},
			},
			wantCount: 1,
		},
		{
			name: "multiple nodes",
			snapshots: []nodeMetricSnapshot{
				{NodeID: uuid.New(), CPUUsage: 0.25, MemUsed: 512, MemTotal: 1024},
				{NodeID: uuid.New(), CPUUsage: 0.75, MemUsed: 768, MemTotal: 1024},
				{NodeID: uuid.New(), CPUUsage: 0.10, MemUsed: 256, MemTotal: 2048},
			},
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copier := &mockCopyFromer{}
			ts := time.Now()

			count, err := batchInsertNodeMetrics(context.Background(), copier, ts, tt.snapshots)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}

			if tt.wantCount > 0 {
				if len(copier.calls) != 1 {
					t.Fatalf("expected 1 CopyFrom call, got %d", len(copier.calls))
				}
				call := copier.calls[0]
				if call.tableName[0] != "node_metrics" {
					t.Errorf("table = %v, want [node_metrics]", call.tableName)
				}
				if len(call.columnNames) != 9 {
					t.Errorf("columns = %d, want 9", len(call.columnNames))
				}
			}
		})
	}
}

func TestBatchInsertVMMetrics(t *testing.T) {
	tests := []struct {
		name      string
		snapshots []vmMetricSnapshot
		wantCount int64
	}{
		{
			name:      "empty snapshots",
			snapshots: nil,
			wantCount: 0,
		},
		{
			name: "single VM",
			snapshots: []vmMetricSnapshot{
				{VMID: uuid.New(), CPUUsage: 0.8, MemUsed: 4096, MemTotal: 8192, DiskRead: 500, DiskWrite: 600, NetIn: 700, NetOut: 800},
			},
			wantCount: 1,
		},
		{
			name: "multiple VMs",
			snapshots: []vmMetricSnapshot{
				{VMID: uuid.New(), CPUUsage: 0.1},
				{VMID: uuid.New(), CPUUsage: 0.2},
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copier := &mockCopyFromer{}
			ts := time.Now()

			count, err := batchInsertVMMetrics(context.Background(), copier, ts, tt.snapshots)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}

			if tt.wantCount > 0 {
				call := copier.calls[0]
				if call.tableName[0] != "vm_metrics" {
					t.Errorf("table = %v, want [vm_metrics]", call.tableName)
				}
			}
		})
	}
}

func TestBatchInsertNodeMetrics_CopyError(t *testing.T) {
	copier := &mockCopyFromer{err: fmt.Errorf("connection lost")}
	snapshots := []nodeMetricSnapshot{
		{NodeID: uuid.New(), CPUUsage: 0.5},
	}

	_, err := batchInsertNodeMetrics(context.Background(), copier, time.Now(), snapshots)
	if err == nil {
		t.Fatal("expected error from CopyFrom failure")
	}
}

func TestBatchInsertVMMetrics_CopyError(t *testing.T) {
	copier := &mockCopyFromer{err: fmt.Errorf("connection lost")}
	snapshots := []vmMetricSnapshot{
		{VMID: uuid.New(), CPUUsage: 0.5},
	}

	_, err := batchInsertVMMetrics(context.Background(), copier, time.Now(), snapshots)
	if err == nil {
		t.Fatal("expected error from CopyFrom failure")
	}
}

func TestBatchInsertNodeMetrics_RowValues(t *testing.T) {
	copier := &mockCopyFromer{}
	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	nodeID := uuid.New()

	snapshots := []nodeMetricSnapshot{
		{NodeID: nodeID, CPUUsage: 0.42, MemUsed: 1000, MemTotal: 2000, DiskRead: 100, DiskWrite: 200, NetIn: 300, NetOut: 400},
	}

	count, err := batchInsertNodeMetrics(context.Background(), copier, ts, snapshots)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	row := copier.calls[0].rows[0]
	if row[0] != ts {
		t.Errorf("row[0] (time) = %v, want %v", row[0], ts)
	}
	if row[1] != nodeID {
		t.Errorf("row[1] (node_id) = %v, want %v", row[1], nodeID)
	}
	if row[2] != 0.42 {
		t.Errorf("row[2] (cpu_usage) = %v, want 0.42", row[2])
	}
	if row[3] != int64(1000) {
		t.Errorf("row[3] (mem_used) = %v, want 1000", row[3])
	}
}
