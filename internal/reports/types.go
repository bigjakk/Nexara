package reports

import "time"

// ReportType identifies the kind of report.
type ReportType string

const (
	TypeResourceUtilization ReportType = "resource_utilization"
	TypeCapacityForecast    ReportType = "capacity_forecast"
	TypeBackupCompliance    ReportType = "backup_compliance"
	TypePatchStatus         ReportType = "patch_status"
	TypeUptimeSummary       ReportType = "uptime_summary"
	TypeVMResourceUsage     ReportType = "vm_resource_usage"
)

// ValidReportType returns true if the report type is supported.
func ValidReportType(t string) bool {
	switch ReportType(t) {
	case TypeResourceUtilization, TypeCapacityForecast, TypeBackupCompliance,
		TypePatchStatus, TypeUptimeSummary, TypeVMResourceUsage:
		return true
	}
	return false
}

// ReportData is the top-level structure stored as JSONB.
type ReportData struct {
	Title       string          `json:"title"`
	ClusterName string          `json:"cluster_name"`
	ClusterID   string          `json:"cluster_id"`
	ReportType  string          `json:"report_type"`
	GeneratedAt string          `json:"generated_at"`
	TimeRange   TimeRange       `json:"time_range"`
	Sections    []ReportSection `json:"sections"`
}

// TimeRange describes the reporting period.
type TimeRange struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Hours     int    `json:"hours"`
}

// ReportSection is a named section with rows of data.
type ReportSection struct {
	Title   string              `json:"title"`
	Headers []string            `json:"headers"`
	Rows    []map[string]string `json:"rows"`
}

// --- Resource Utilization types ---

// NodeUtilization holds daily resource metrics for a node.
type NodeUtilization struct {
	NodeName  string  `json:"node_name"`
	Day       string  `json:"day"`
	CPUAvg    float64 `json:"cpu_avg"`
	CPUMax    float64 `json:"cpu_max"`
	MemAvg    float64 `json:"mem_avg"`
	MemMax    float64 `json:"mem_max"`
	DiskRead  float64 `json:"disk_read"`
	DiskWrite float64 `json:"disk_write"`
	NetIn     float64 `json:"net_in"`
	NetOut    float64 `json:"net_out"`
}

// --- Capacity Forecast types ---

// ForecastResult holds a metric's linear regression forecast.
type ForecastResult struct {
	Metric         string   `json:"metric"`
	NodeName       string   `json:"node_name"`
	CurrentValue   float64  `json:"current_value"`
	TrendPerDay    float64  `json:"trend_per_day"`
	DaysToExhaust  *float64 `json:"days_to_exhaust,omitempty"`
	ExhaustionDate *string  `json:"exhaustion_date,omitempty"`
}

// --- Backup Compliance types ---

// BackupComplianceEntry describes a VM's backup status.
type BackupComplianceEntry struct {
	VMName       string `json:"vm_name"`
	VMID         int    `json:"vmid"`
	NodeName     string `json:"node_name"`
	HasBackup    bool   `json:"has_backup"`
	LastBackup   string `json:"last_backup,omitempty"`
	BackupAge    string `json:"backup_age,omitempty"`
	IsStale      bool   `json:"is_stale"`
}

// BackupComplianceSummary is the overall backup coverage info.
type BackupComplianceSummary struct {
	TotalVMs    int     `json:"total_vms"`
	BackedUp    int     `json:"backed_up"`
	Missing     int     `json:"missing"`
	Stale       int     `json:"stale"`
	CoveragePercent float64 `json:"coverage_percent"`
}

// --- Patch Status types ---

// PatchStatusNode describes per-node vulnerability counts.
type PatchStatusNode struct {
	NodeName     string `json:"node_name"`
	TotalVulns   int    `json:"total_vulns"`
	Critical     int    `json:"critical"`
	High         int    `json:"high"`
	Medium       int    `json:"medium"`
	Low          int    `json:"low"`
	PostureScore float64 `json:"posture_score"`
}

// --- Uptime types ---

// UptimeEntry holds node uptime info.
type UptimeEntry struct {
	NodeName   string        `json:"node_name"`
	Status     string        `json:"status"`
	Uptime     time.Duration `json:"-"`
	UptimeStr  string        `json:"uptime"`
	UptimePct  float64       `json:"uptime_pct"`
}
