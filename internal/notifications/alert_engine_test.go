package notifications

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/proxdash/proxdash/internal/db/generated"
)

func TestCompareValue(t *testing.T) {
	tests := []struct {
		name      string
		value     float64
		operator  string
		threshold float64
		want      bool
	}{
		{"gt_true", 95.0, ">", 90.0, true},
		{"gt_false", 90.0, ">", 90.0, false},
		{"gt_below", 85.0, ">", 90.0, false},
		{"gte_equal", 90.0, ">=", 90.0, true},
		{"gte_above", 91.0, ">=", 90.0, true},
		{"gte_below", 89.0, ">=", 90.0, false},
		{"lt_true", 5.0, "<", 10.0, true},
		{"lt_false", 10.0, "<", 10.0, false},
		{"lte_equal", 10.0, "<=", 10.0, true},
		{"lte_above", 11.0, "<=", 10.0, false},
		{"eq_true", 50.0, "==", 50.0, true},
		{"eq_false", 50.1, "==", 50.0, false},
		{"neq_true", 50.1, "!=", 50.0, true},
		{"neq_false", 50.0, "!=", 50.0, false},
		{"zero_gt", 0.0, ">", 0.0, false},
		{"negative", -5.0, "<", 0.0, true},
		{"invalid_op", 50.0, "~", 50.0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareValue(tt.value, tt.operator, tt.threshold)
			if got != tt.want {
				t.Errorf("compareValue(%v, %q, %v) = %v, want %v",
					tt.value, tt.operator, tt.threshold, got, tt.want)
			}
		})
	}
}

func TestExtractNodeMetric(t *testing.T) {
	row := db.GetNodeRecentMetricsRow{
		CpuUsage:  75.5,
		MemUsed:   4 * 1024 * 1024 * 1024,  // 4 GB
		MemTotal:  8 * 1024 * 1024 * 1024,  // 8 GB
		DiskRead:  1000,
		DiskWrite: 2000,
		NetIn:     3000,
		NetOut:    4000,
	}

	tests := []struct {
		metric string
		want   float64
	}{
		{"cpu_usage", 75.5},
		{"mem_percent", 50.0},
		{"disk_read", 1000},
		{"disk_write", 2000},
		{"net_in", 3000},
		{"net_out", 4000},
		{"unknown", 0},
	}

	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			got := extractNodeMetric(row, tt.metric)
			if got != tt.want {
				t.Errorf("extractNodeMetric(%q) = %v, want %v", tt.metric, got, tt.want)
			}
		})
	}
}

func TestExtractNodeMetric_ZeroMemTotal(t *testing.T) {
	row := db.GetNodeRecentMetricsRow{MemUsed: 1000, MemTotal: 0}
	got := extractNodeMetric(row, "mem_percent")
	if got != 0 {
		t.Errorf("mem_percent with zero total = %v, want 0", got)
	}
}

func TestExtractVMMetric(t *testing.T) {
	row := db.GetVMRecentMetricsRow{
		CpuUsage:  30.0,
		MemUsed:   2 * 1024 * 1024 * 1024,
		MemTotal:  4 * 1024 * 1024 * 1024,
		DiskRead:  500,
		DiskWrite: 600,
		NetIn:     700,
		NetOut:    800,
	}

	tests := []struct {
		metric string
		want   float64
	}{
		{"cpu_usage", 30.0},
		{"mem_percent", 50.0},
		{"disk_read", 500},
		{"disk_write", 600},
		{"net_in", 700},
		{"net_out", 800},
	}

	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			got := extractVMMetric(row, tt.metric)
			if got != tt.want {
				t.Errorf("extractVMMetric(%q) = %v, want %v", tt.metric, got, tt.want)
			}
		})
	}
}

func TestIsInMaintenanceWindow(t *testing.T) {
	clusterID := uuid.New()
	nodeID := uuid.New()
	otherNodeID := uuid.New()

	clusterPg := pgtype.UUID{Bytes: clusterID, Valid: true}
	nodePg := pgtype.UUID{Bytes: nodeID, Valid: true}
	otherNodePg := pgtype.UUID{Bytes: otherNodeID, Valid: true}

	e := &Engine{}

	tests := []struct {
		name      string
		clusterID pgtype.UUID
		nodeID    pgtype.UUID
		windows   []db.MaintenanceWindow
		want      bool
	}{
		{
			"no_windows",
			clusterPg, nodePg,
			nil,
			false,
		},
		{
			"cluster_wide_window",
			clusterPg, nodePg,
			[]db.MaintenanceWindow{{ClusterID: clusterID, NodeID: pgtype.UUID{}}},
			true,
		},
		{
			"matching_node_window",
			clusterPg, nodePg,
			[]db.MaintenanceWindow{{ClusterID: clusterID, NodeID: nodePg}},
			true,
		},
		{
			"different_node_window",
			clusterPg, nodePg,
			[]db.MaintenanceWindow{{ClusterID: clusterID, NodeID: otherNodePg}},
			false,
		},
		{
			"different_cluster",
			pgtype.UUID{Bytes: uuid.New(), Valid: true}, nodePg,
			[]db.MaintenanceWindow{{ClusterID: clusterID, NodeID: pgtype.UUID{}}},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.isInMaintenanceWindow(tt.clusterID, tt.nodeID, tt.windows)
			if got != tt.want {
				t.Errorf("isInMaintenanceWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidMetric(t *testing.T) {
	for _, m := range []string{"cpu_usage", "mem_percent", "disk_read", "disk_write", "net_in", "net_out"} {
		if !ValidMetric(m) {
			t.Errorf("ValidMetric(%q) = false, want true", m)
		}
	}
	if ValidMetric("bogus") {
		t.Error("ValidMetric(\"bogus\") = true, want false")
	}
}

func TestParseEscalationChain(t *testing.T) {
	chain := parseEscalationChain([]byte(`[{"channel_id":"a0000000-0000-0000-0000-000000000001","delay_minutes":5}]`))
	if len(chain) != 1 {
		t.Fatalf("expected 1 step, got %d", len(chain))
	}
	if chain[0].DelayMinutes != 5 {
		t.Errorf("delay_minutes = %d, want 5", chain[0].DelayMinutes)
	}

	// Empty/invalid
	if chain := parseEscalationChain(nil); chain != nil {
		t.Error("expected nil for nil input")
	}
	if chain := parseEscalationChain([]byte("[]")); len(chain) != 0 {
		t.Error("expected empty for empty array")
	}
}

func TestFormatThreshold(t *testing.T) {
	if got := formatThreshold(90); got != "90" {
		t.Errorf("formatThreshold(90) = %q, want %q", got, "90")
	}
	if got := formatThreshold(90.5); got != "90.50" {
		t.Errorf("formatThreshold(90.5) = %q, want %q", got, "90.50")
	}
}
