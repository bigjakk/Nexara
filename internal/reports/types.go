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
	TypeSnapshotInventory   ReportType = "snapshot_inventory"
	TypeClusterDigest       ReportType = "cluster_digest"
)

// AllTypes lists every report type the generator implements, in catalogue
// order. The report_schedules CHECK constraint (migrations/000103) and the
// frontend catalogue must agree with it.
var AllTypes = []ReportType{
	TypeClusterDigest,
	TypeBackupCompliance,
	TypeResourceUtilization,
	TypeVMResourceUsage,
	TypeCapacityForecast,
	TypeSnapshotInventory,
	TypePatchStatus,
	TypeUptimeSummary,
}

// ValidReportType returns true if the report type is supported.
func ValidReportType(t string) bool {
	for _, rt := range AllTypes {
		if string(rt) == t {
			return true
		}
	}
	return false
}

// Name is the human name of a report type, as the masthead and the email
// subject spell it.
func (t ReportType) Name() string {
	switch t {
	case TypeResourceUtilization:
		return "Resource utilisation"
	case TypeCapacityForecast:
		return "Capacity forecast"
	case TypeBackupCompliance:
		return "Backup compliance"
	case TypePatchStatus:
		return "Patch status"
	case TypeUptimeSummary:
		return "Uptime summary"
	case TypeVMResourceUsage:
		return "VM resource usage"
	case TypeSnapshotInventory:
		return "Snapshot inventory"
	case TypeClusterDigest:
		return "Cluster digest"
	}
	return string(t)
}

// SchemaVersion is stamped on every ReportData so a reader of report_runs.
// report_data can tell the typed shape (2) from the headers-and-rows shape
// the first renderer stored (no schema field at all).
const SchemaVersion = 2

// Tones colour a value by what it means. They are the only colours a report
// carries: series identity never rides on hue (the PBS and Veeam accents are
// indistinguishable under deuteranopia), so provider identity is text on a
// chip and charts encode state.
const (
	ToneGood    = "good"
	ToneWarn    = "warn"
	ToneCrit    = "crit"
	ToneNeutral = "neutral"
	ToneAccent  = "accent"
)

// Finding severities, in ranking order.
const (
	SevCritical = "critical"
	SevSerious  = "serious"
	SevWarning  = "warning"
	SevInfo     = "info"
)

// ReportData is the top-level structure stored as JSONB and rendered to HTML,
// CSV and the email digest.
type ReportData struct {
	Schema int `json:"schema"`
	// Title is the full name — "Backup compliance · cluster01 · 7 days to
	// 2026-09-14" — used for the document title and the email subject.
	Title string `json:"title"`
	// Kicker is the report's name alone, shown above the heading.
	Kicker string `json:"kicker"`
	// Heading is what the masthead leads with: the cluster and the period.
	Heading string `json:"heading"`
	// Subtitle is one sentence on what the report covers.
	Subtitle    string    `json:"subtitle,omitempty"`
	ClusterName string    `json:"cluster_name"`
	ClusterID   string    `json:"cluster_id"`
	ReportType  string    `json:"report_type"`
	GeneratedAt string    `json:"generated_at"`
	TimeRange   TimeRange `json:"time_range"`
	// Meta is the fact strip under the masthead: period, scope, sources, rules.
	Meta []MetaItem `json:"meta,omitempty"`
	// KPIs is the headline strip. At most one entry is the hero.
	KPIs []KPI `json:"kpis,omitempty"`
	// Findings are the ranked, plain-language conclusions, most severe first.
	Findings []Finding `json:"findings,omitempty"`
	Sections []Section `json:"sections"`
	// Notes is the method footer: how the numbers were computed.
	Notes []NoteGroup `json:"notes,omitempty"`
}

// TimeRange describes the reporting period.
type TimeRange struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Hours     int    `json:"hours"`
}

// MetaItem is one labelled fact in the masthead strip.
type MetaItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// KPI is one headline tile.
type KPI struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Unit is rendered small beside the value ("%", "TB").
	Unit   string `json:"unit,omitempty"`
	Detail string `json:"detail,omitempty"`
	Tone   string `json:"tone,omitempty"`
	Hero   bool   `json:"hero,omitempty"`
}

// Finding is one ranked conclusion. Lead is the bold clause; Text continues
// the sentence; Where names the section or object to act on.
type Finding struct {
	Severity string `json:"severity"`
	Lead     string `json:"lead"`
	Text     string `json:"text,omitempty"`
	Where    string `json:"where,omitempty"`
}

// Section is a titled group of blocks laid out in a two-column grid; a block
// marked Wide takes the full width.
type Section struct {
	Title  string  `json:"title"`
	Why    string  `json:"why,omitempty"`
	Blocks []Block `json:"blocks"`
}

// Block kinds.
const (
	BlockTable  = "table"
	BlockChart  = "chart"
	BlockMeters = "meters"
	BlockText   = "text"
)

// Block is one unit of content inside a section.
type Block struct {
	Kind     string  `json:"kind"`
	Title    string  `json:"title,omitempty"`
	Subtitle string  `json:"subtitle,omitempty"`
	Wide     bool    `json:"wide,omitempty"`
	Table    *Table  `json:"table,omitempty"`
	Chart    *Chart  `json:"chart,omitempty"`
	Meters   []Meter `json:"meters,omitempty"`
	Text     string  `json:"text,omitempty"`
	// Empty is shown in place of a table or chart with no rows.
	Empty string `json:"empty,omitempty"`
}

// Table is a typed grid. Rows align with Columns by position.
type Table struct {
	Columns []Column `json:"columns"`
	Rows    [][]Cell `json:"rows"`
}

// Column describes one table column.
type Column struct {
	Name string `json:"name"`
	// Numeric right-aligns the column with tabular figures.
	Numeric bool `json:"numeric,omitempty"`
	NoWrap  bool `json:"nowrap,omitempty"`
}

// Cell kinds.
const (
	CellText   = ""
	CellID     = "id"
	CellStrong = "strong"
	CellMute   = "mute"
	CellPill   = "pill"
	CellChips  = "chips"
)

// Cell is one table value. Sub is a muted second line (a timestamp under an
// age); Tone applies to pills; Chips applies to chip cells.
type Cell struct {
	Text  string `json:"text"`
	Sub   string `json:"sub,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Tone  string `json:"tone,omitempty"`
	Chips []Chip `json:"chips,omitempty"`
}

// Chip kinds.
const (
	ChipPBS   = "pbs"
	ChipVeeam = "veeam"
	ChipFlag  = "flag"
	ChipType  = "type"
)

// Chip is a small labelled marker inside a cell: a provider, a guest type, or
// a dashed caution flag.
type Chip struct {
	Text string `json:"text"`
	Kind string `json:"kind"`
}

// Chart kinds.
const (
	// ChartStackedBar is one horizontal 100 % bar: each series contributes its
	// first value.
	ChartStackedBar = "stacked-bar"
	// ChartColumns is stacked columns over categories, one segment per series.
	ChartColumns = "columns"
	// ChartHist is single-series columns with a value label on every cap and
	// an optional threshold line.
	ChartHist = "hist"
	// ChartBars is horizontal bars, one per category, single series, labelled
	// at the bar end.
	ChartBars = "bars"
	// ChartLines is one or more lines over ordered categories.
	ChartLines = "lines"
)

// Value formats for chart labels.
const (
	FormatNumber  = ""
	FormatPercent = "percent"
	FormatBytes   = "bytes"
	FormatRate    = "rate"
)

// Chart is a data graphic the renderer draws as inline SVG.
type Chart struct {
	Kind       string     `json:"kind"`
	Categories []string   `json:"categories,omitempty"`
	Series     []Series   `json:"series"`
	Threshold  *Threshold `json:"threshold,omitempty"`
	// Format decides how values are written in labels and tooltips.
	Format string `json:"format,omitempty"`
	// Footnote is drawn small under the axis ("guests · 22 backup targets").
	Footnote string `json:"footnote,omitempty"`
	// Total is the stacked-bar denominator; zero means the sum of the series.
	Total float64 `json:"total,omitempty"`
	// Max pins the value axis; zero means the data's peak.
	Max float64 `json:"max,omitempty"`
	// Legend draws a series legend beneath the chart; a single series needs none.
	Legend bool `json:"legend,omitempty"`
	// AriaLabel describes the chart for screen readers.
	AriaLabel string `json:"aria_label,omitempty"`
}

// Series is one named run of values, coloured by Tone.
type Series struct {
	Name   string    `json:"name"`
	Tone   string    `json:"tone,omitempty"`
	Values []float64 `json:"values"`
}

// Threshold marks a boundary. For ChartHist it is a vertical line after the
// category at AfterIndex; for ChartLines and ChartBars it is a horizontal or
// vertical line at Value.
type Threshold struct {
	AfterIndex int     `json:"after_index,omitempty"`
	Value      float64 `json:"value,omitempty"`
	Label      string  `json:"label"`
}

// Meter is one capacity gauge.
type Meter struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	// Used and Total are in the same unit; UsedText is how they are written.
	Used     float64 `json:"used"`
	Total    float64 `json:"total"`
	UsedText string  `json:"used_text"`
	// Tone colours the fill: accent, warn or crit by how full it is.
	Tone       string `json:"tone,omitempty"`
	Projection string `json:"projection,omitempty"`
	// ProjectionWarn emphasises a projection that needs action.
	ProjectionWarn bool `json:"projection_warn,omitempty"`
}

// NoteGroup is one titled list in the method footer.
type NoteGroup struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
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

// --- Uptime types ---

// UptimeEntry holds node uptime info.
type UptimeEntry struct {
	NodeName  string        `json:"node_name"`
	Status    string        `json:"status"`
	Uptime    time.Duration `json:"-"`
	UptimeStr string        `json:"uptime"`
	UptimePct float64       `json:"uptime_pct"`
}
