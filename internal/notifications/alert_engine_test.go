package notifications

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
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

// CpuUsage is stored as PVE's 0–1 ratio; a rule's threshold is a percentage.
func TestExtractNodeMetric(t *testing.T) {
	row := db.GetNodeRecentMetricsRow{
		CpuUsage:  0.755,
		MemUsed:   4 * 1024 * 1024 * 1024, // 4 GB
		MemTotal:  8 * 1024 * 1024 * 1024, // 8 GB
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
		CpuUsage:  0.25,
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
		{"cpu_usage", 25.0},
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

// A VM rule is judged on percent CPU over the readings in its window, and a
// memory rule never on the host's view of the VM: that sits near 100 %
// whatever the guest uses. A reading with no recorded source keeps its say.
// A counter rule is judged on rates; a running guest's zero is unreported.
func TestVMRuleOutcome(t *testing.T) {
	t.Parallel()
	now := time.Now()
	// A 100 MiB allocation, so usedPct MiB is exactly usedPct %.
	reading := func(ago time.Duration, cpu float64, usedPct int64, guest *bool) db.GetVMRecentMetricsRow {
		r := db.GetVMRecentMetricsRow{Bucket: now.Add(-ago), CpuUsage: cpu, MemUsed: usedPct << 20, MemTotal: 100 << 20}
		if guest != nil {
			r.MemGuest = pgtype.Bool{Bool: *guest, Valid: true}
		}
		return r
	}
	own, host := true, false
	cpu := func(ago time.Duration, ratio float64) db.GetVMRecentMetricsRow { return reading(ago, ratio, 50, &host) }
	disk := func(agoSeconds int, read int64, cpu float64, mem int64) db.GetVMRecentMetricsRow {
		return db.GetVMRecentMetricsRow{Bucket: now.Add(-time.Duration(agoSeconds) * time.Second), DiskRead: read, CpuUsage: cpu, MemUsed: mem}
	}
	carried := func(r db.GetVMRecentMetricsRow) db.GetVMRecentMetricsRow { r.DiskUnreported = true; return r }
	tests := []struct {
		name       string
		metric     string
		duration   int32
		rows       []db.GetVMRecentMetricsRow
		met, ok    bool
		wantLatest float64
	}{
		{"cpu ratio judged as percent, host-view VM", "cpu_usage", 60, []db.GetVMRecentMetricsRow{cpu(10*time.Second, 0.95)}, true, true, 95},
		{"zero duration judges the latest alone", "cpu_usage", 0, []db.GetVMRecentMetricsRow{cpu(10*time.Second, 0.95), cpu(50*time.Second, 0.10)}, true, true, 95},
		{"every reading in the window must meet it", "cpu_usage", 60, []db.GetVMRecentMetricsRow{cpu(10*time.Second, 0.95), cpu(30*time.Second, 0.50)}, false, true, 95},
		{"a reading before the window is not judged", "cpu_usage", 60, []db.GetVMRecentMetricsRow{cpu(10*time.Second, 0.95), cpu(90*time.Second, 0.10)}, true, true, 95},
		{"nothing in the window", "cpu_usage", 60, []db.GetVMRecentMetricsRow{cpu(90*time.Second, 0.95)}, false, false, 0},
		{"guest's own memory", "mem_percent", 60, []db.GetVMRecentMetricsRow{reading(10*time.Second, 0.1, 96, &own), reading(30*time.Second, 0.1, 96, &own)}, true, true, 96},
		{"host memory only: unmet, not unjudged", "mem_percent", 60, []db.GetVMRecentMetricsRow{reading(10*time.Second, 0.1, 101, &host)}, false, true, 0},
		{"a boot's host reading is skipped", "mem_percent", 60, []db.GetVMRecentMetricsRow{reading(10*time.Second, 0.1, 101, &host), reading(30*time.Second, 0.1, 96, &own)}, true, true, 96},
		{"a reading with no source is judged on its figure", "mem_percent", 60, []db.GetVMRecentMetricsRow{reading(10*time.Second, 0.1, 96, nil)}, true, true, 96},
		{"a lapse in the guest's own figure holds the alert", "mem_percent", 0, []db.GetVMRecentMetricsRow{reading(10*time.Second, 0.1, 101, &host), reading(40*time.Second, 0.1, 96, &own)}, false, false, 0},
		{"no readings", "mem_percent", 60, nil, false, false, 0},
		{"a disk counter is judged as a rate", "disk_read", 0, []db.GetVMRecentMetricsRow{disk(10, 3000, 0.1, 1<<30), disk(30, 1000, 0.1, 1<<30)}, true, true, 100},
		{"a zero counter on a guest showing memory or CPU is no baseline", "disk_read", 0, []db.GetVMRecentMetricsRow{disk(10, 5100, 0, 1<<30), disk(30, 0, 0, 1<<30), disk(50, 0, 0.1, 0)}, false, false, 0},
		{"a stopped guest's zeros are readings", "disk_read", 0, []db.GetVMRecentMetricsRow{disk(10, 0, 0, 0), disk(30, 0, 0, 0)}, false, true, 0},
		{"a carried sample is no reading", "disk_read", 0, []db.GetVMRecentMetricsRow{carried(disk(10, 5000, 0.1, 1<<30)), disk(30, 5000, 0.1, 1<<30), disk(50, 3000, 0.1, 1<<30)}, false, false, 0},
		{"its network counters still are", "net_in", 0, []db.GetVMRecentMetricsRow{{Bucket: now.Add(-10 * time.Second), NetIn: 3000, CpuUsage: 0.1, MemUsed: 1 << 30, DiskUnreported: true}, {Bucket: now.Add(-30 * time.Second), NetIn: 1000, CpuUsage: 0.1, MemUsed: 1 << 30}}, true, true, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rule := db.AlertRule{Metric: tt.metric, Operator: ">", Threshold: 90, DurationSeconds: tt.duration}
			met, latest, ok := vmRuleOutcome(rule, tt.rows, now)
			if met != tt.met || ok != tt.ok || latest != tt.wantLatest {
				t.Errorf("vmRuleOutcome = (%v, %v, %v), want (%v, %v, %v)", met, latest, ok, tt.met, tt.wantLatest, tt.ok)
			}
		})
	}
}

// A counter metric is judged on the rates between its readings, derived from
// the whole fetch so a reading before the window still serves as a baseline.
// A dip is measured from the level it fell from, an unreported reading is no
// baseline, and the rule holds while the newest reading has no rate.
func TestCounterOutcome(t *testing.T) {
	t.Parallel()
	now := time.Now()
	type sample struct {
		ago        time.Duration
		bytes      float64
		unreported bool
	}
	s := func(agoSeconds int, bytes float64) sample {
		return sample{ago: time.Duration(agoSeconds) * time.Second, bytes: bytes}
	}
	u := func(agoSeconds int) sample {
		return sample{ago: time.Duration(agoSeconds) * time.Second, unreported: true}
	}
	tests := []struct {
		name       string
		duration   int32
		readings   []sample // newest first, as fetched
		met, ok    bool
		wantLatest float64
	}{
		{"duration 0 judges the newest rate alone", 0, []sample{s(10, 3000), s(30, 1000), s(50, 1000)}, true, true, 100},
		{"the window's first rate is measured from the reading before it", 30, []sample{s(10, 2000), s(40, 500), s(70, 0)}, true, true, 50},
		{"a fall holds until the counter is back at its level", 60, []sample{s(10, 1100), s(30, 100), s(50, 9000), s(70, 7000)}, false, false, 0},
		{"a dip is measured from the level it fell from", 0, []sample{s(10, 5100), s(30, 4000), s(50, 5000)}, false, true, 2.5},
		{"an unreported reading is no baseline", 0, []sample{s(10, 5100), u(30)}, false, false, 0},
		{"an unreported newest reading holds", 0, []sample{u(10), s(30, 3000), s(50, 1000)}, false, false, 0},
		{"readings at one instant have no rate between them", 0, []sample{s(10, 3000), s(10, 3000), s(30, 1000)}, true, true, 100},
		{"no readings", 0, nil, false, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rule := db.AlertRule{Metric: "net_in", Operator: ">", Threshold: 40, DurationSeconds: tt.duration}
			met, latest, ok := counterOutcome(rule, tt.readings, now,
				func(r sample) time.Time { return now.Add(-r.ago) },
				func(r sample) (float64, bool) { return r.bytes, !r.unreported })
			if met != tt.met || ok != tt.ok || latest != tt.wantLatest {
				t.Errorf("counterOutcome = (%v, %v, %v), want (%v, %v, %v)", met, latest, ok, tt.met, tt.wantLatest, tt.ok)
			}
		})
	}
}

// A node rule judges a level metric on its readings and a counter metric on
// the rate between them, not on the counter. A disk rule holds while the
// newest sample is one the collector marks disk_unreported; a network rule
// does not, its sums being real.
func TestNodeRuleOutcome(t *testing.T) {
	t.Parallel()
	now := time.Now()
	rows := []db.GetNodeRecentMetricsRow{
		{Bucket: now.Add(-10 * time.Second), CpuUsage: 0.95, NetIn: 3000},
		{Bucket: now.Add(-30 * time.Second), CpuUsage: 0.95, NetIn: 1000},
	}
	carried := append([]db.GetNodeRecentMetricsRow{{Bucket: now.Add(-5 * time.Second), CpuUsage: 0.95, NetIn: 3500, DiskUnreported: true}}, rows...)
	for _, tt := range []struct {
		name       string
		metric     string
		rows       []db.GetNodeRecentMetricsRow
		met, ok    bool
		wantLatest float64
	}{
		{"cpu as percent", "cpu_usage", rows, true, true, 95},
		{"net_in as a rate", "net_in", rows, true, true, 100},
		{"a carried disk sample holds a disk rule", "disk_write", carried, false, false, 0},
		{"but not a network rule", "net_in", carried, true, true, 100},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rule := db.AlertRule{Metric: tt.metric, Operator: ">", Threshold: 90, DurationSeconds: 60}
			met, latest, ok := nodeRuleOutcome(rule, tt.rows, now)
			if met != tt.met || ok != tt.ok || latest != tt.wantLatest {
				t.Errorf("nodeRuleOutcome = (%v, %v, %v), want (%v, %v, %v)", met, latest, ok, tt.met, tt.wantLatest, tt.ok)
			}
		})
	}
}

// A counter metric's fetch reaches past its window for the reading the
// window's oldest rate is measured from; a level metric's does not.
func TestMetricLookback(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		metric string
		want   time.Duration
	}{
		{"cpu_usage", 5 * time.Minute},
		{"net_in", 15 * time.Minute},
	} {
		if got := metricLookback(db.AlertRule{Metric: tt.metric, DurationSeconds: 300}); got != tt.want {
			t.Errorf("metricLookback(%s, 300s) = %v, want %v", tt.metric, got, tt.want)
		}
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
	for _, m := range []string{
		"cpu_usage", "mem_percent", "disk_read", "disk_write", "net_in", "net_out",
		"snapshot_age_days", "veeam_rpo_hours", "veeam_malware_status", "veeam_repo_used_percent",
		// veeam_job_failed became implementable with job control, and not
		// before: a job cancelled through the API records as result "Failed"
		// with isCanceled false and an empty log, so the rule would have fired
		// every time an operator stopped a job from Nexara. What made it
		// honest is veeam_sessions.nexara_stopped, which the control handlers
		// set and GetClusterVeeamJobFailureStats excludes on.
		"veeam_job_failed",
		// The PVE task metrics needed no new collection — task_history already
		// carried the terminal status. What was missing was a trigger on it.
		"pve_backup_failed", "pve_task_failed",
	} {
		if !ValidMetric(m) {
			t.Errorf("ValidMetric(%q) = false, want true", m)
		}
	}
	if ValidMetric("bogus") {
		t.Error("ValidMetric(\"bogus\") = true, want false")
	}
}

// The engine errors out on an unsupported metric/scope pairing once per tick,
// forever, and the rule silently never fires — so the API rejects the pairing
// at create time, from this table.
func TestMetricScopes(t *testing.T) {
	tests := []struct {
		metric string
		want   []string
	}{
		{"snapshot_age_days", []string{"cluster", "vm"}},
		{"veeam_rpo_hours", []string{"cluster", "vm"}},
		{"veeam_malware_status", []string{"cluster", "vm"}},
		// One repository holds every cluster's backups, so there is no
		// cluster to attribute its fullness to.
		{"veeam_repo_used_percent", []string{"global"}},
		// A Veeam job protects many guests at once, so there is no single vm
		// its failure belongs to — and raising it against every guest in the
		// job would be N alarms for one event.
		{"veeam_job_failed", []string{"cluster"}},
		// task_history stores a node NAME while a rule stores a node UUID, so
		// node scope would need the name resolved and the dedupe dimension
		// threaded through. Cluster only until someone wants that stream.
		{"pve_backup_failed", []string{"cluster"}},
		{"pve_task_failed", []string{"cluster"}},
		// nil means "the ordinary node/vm/cluster set", which the caller
		// distinguishes from a restricted list.
		{"cpu_usage", nil},
	}
	for _, tt := range tests {
		got := MetricScopes(tt.metric)
		if len(got) != len(tt.want) {
			t.Errorf("MetricScopes(%q) = %v, want %v", tt.metric, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("MetricScopes(%q) = %v, want %v", tt.metric, got, tt.want)
				break
			}
		}
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
