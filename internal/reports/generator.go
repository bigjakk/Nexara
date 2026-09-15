package reports

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// PermissionChecker is the slice of the RBAC engine the generator needs: it
// asks whether the requesting user may see Veeam data on the cluster.
// *auth.RBACEngine satisfies it.
type PermissionChecker interface {
	HasPermission(ctx context.Context, userID uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error)
}

// Generator builds report data from database queries.
type Generator struct {
	queries db.Querier
	perms   PermissionChecker
	logger  *slog.Logger
	now     func() time.Time
}

// NewGenerator creates a report generator. perms may be nil, in which case no
// report ever includes Veeam data — the fail-closed answer for a deployment
// without an RBAC engine.
func NewGenerator(queries db.Querier, perms PermissionChecker, logger *slog.Logger) *Generator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Generator{queries: queries, perms: perms, logger: logger, now: time.Now}
}

// Request describes one report to produce.
type Request struct {
	Type           string
	ClusterID      uuid.UUID
	TimeRangeHours int
	Params         Params
	// RequestedBy is the user whose grants decide what the report may read:
	// the on-demand requester, or the schedule's creator for a scheduled run.
	// A stored run is then readable by anyone holding view:report on the
	// cluster, which is the same posture audit details take for Viewers — a
	// report is a summary product, shared under view:report by design.
	RequestedBy uuid.UUID
}

// genCtx carries what every generator needs about the run.
type genCtx struct {
	cluster db.Cluster
	now     time.Time
	since   time.Time
	hours   int
	params  Params
	user    uuid.UUID
}

// Generate produces a report of the requested type for a cluster.
func (g *Generator) Generate(ctx context.Context, req Request) (*ReportData, error) {
	if !ValidReportType(req.Type) {
		return nil, fmt.Errorf("unsupported report type: %s", req.Type)
	}
	cluster, err := g.queries.GetCluster(ctx, req.ClusterID)
	if err != nil {
		return nil, fmt.Errorf("get cluster: %w", err)
	}
	hours := req.TimeRangeHours
	if hours <= 0 {
		hours = 168
	}
	params := req.Params.WithDefaults()
	now := g.now().UTC()
	gc := genCtx{
		cluster: cluster,
		now:     now,
		since:   now.Add(-time.Duration(hours) * time.Hour),
		hours:   hours,
		params:  params,
		user:    req.RequestedBy,
	}

	rt := ReportType(req.Type)
	data := g.newReport(rt, gc)

	switch rt {
	case TypeBackupCompliance:
		err = g.generateBackupCompliance(ctx, gc, data)
	case TypeResourceUtilization:
		err = g.generateResourceUtilization(ctx, gc, data)
	case TypeCapacityForecast:
		err = g.generateCapacityForecast(ctx, gc, data)
	case TypeVMResourceUsage:
		err = g.generateVMResourceUsage(ctx, gc, data)
	case TypeSnapshotInventory:
		err = g.generateSnapshotInventory(ctx, gc, data)
	case TypePatchStatus:
		err = g.generatePatchStatus(ctx, gc, data)
	case TypeUptimeSummary:
		err = g.generateUptimeSummary(ctx, gc, data)
	case TypeClusterDigest:
		err = g.generateClusterDigest(ctx, gc, data)
	default:
		err = fmt.Errorf("unsupported report type: %s", req.Type)
	}
	if err != nil {
		return nil, err
	}
	sortFindings(data.Findings)
	return data, nil
}

// newReport fills the fields every report shares.
func (g *Generator) newReport(rt ReportType, gc genCtx) *ReportData {
	period := periodLabel(gc.hours, gc.now)
	return &ReportData{
		Schema:      SchemaVersion,
		Title:       fmt.Sprintf("%s · %s · %s", rt.Name(), gc.cluster.Name, period),
		Kicker:      rt.Name(),
		Heading:     fmt.Sprintf("%s · %s", gc.cluster.Name, period),
		ClusterName: gc.cluster.Name,
		ClusterID:   gc.cluster.ID.String(),
		ReportType:  string(rt),
		GeneratedAt: gc.now.Format("2006-01-02 15:04") + " UTC",
		TimeRange: TimeRange{
			StartTime: gc.since.Format(time.RFC3339Nano),
			EndTime:   gc.now.Format(time.RFC3339Nano),
			Hours:     gc.hours,
		},
		Meta: []MetaItem{{
			Label: "Period",
			Value: fmt.Sprintf("%s → %s UTC (%d h)", gc.since.Format("2006-01-02 15:04"), gc.now.Format("2006-01-02 15:04"), gc.hours),
		}},
		Sections: []Section{},
	}
}

// includeVeeam decides whether the requesting user may see Veeam data on the
// cluster. No engine, or no user, means no: the report never says more than
// its reader could learn from the coverage page.
func (g *Generator) includeVeeam(ctx context.Context, gc genCtx) bool {
	if g.perms == nil || gc.user == uuid.Nil {
		return false
	}
	ok, err := g.perms.HasPermission(ctx, gc.user, "view", "veeam", "cluster", gc.cluster.ID)
	if err != nil {
		g.logger.Warn("report: veeam permission check failed, omitting Veeam data",
			"cluster_id", gc.cluster.ID, "user_id", gc.user, "error", err)
		return false
	}
	return ok
}

// periodLabel is the human period: "7 days to 2026-09-14", or "36 h to
// 2026-09-14 06:00" when the range is not whole days.
func periodLabel(hours int, end time.Time) string {
	if hours >= 24 && hours%24 == 0 {
		days := hours / 24
		if days == 1 {
			return "24 hours to " + end.Format("2006-01-02")
		}
		return fmt.Sprintf("%d days to %s", days, end.Format("2006-01-02"))
	}
	return fmt.Sprintf("%d h to %s", hours, end.Format("2006-01-02 15:04"))
}

// --- Findings ---

var severityRank = map[string]int{SevCritical: 0, SevSerious: 1, SevWarning: 2, SevInfo: 3}

// sortFindings orders findings most severe first, keeping insertion order
// within a severity so a generator's own ordering survives.
func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		return severityRank[fs[i].Severity] < severityRank[fs[j].Severity]
	})
}

func (d *ReportData) addFinding(sev, lead, text, where string) {
	d.Findings = append(d.Findings, Finding{Severity: sev, Lead: lead, Text: text, Where: where})
}

func (d *ReportData) addSection(title, why string, blocks ...Block) {
	d.Sections = append(d.Sections, Section{Title: title, Why: why, Blocks: blocks})
}

func (d *ReportData) addMeta(label, value string) {
	d.Meta = append(d.Meta, MetaItem{Label: label, Value: value})
}

// --- Cell and block builders ---

func col(name string) Column       { return Column{Name: name} }
func numCol(name string) Column    { return Column{Name: name, Numeric: true} }
func nowrapCol(name string) Column { return Column{Name: name, NoWrap: true} }
func text(s string) Cell           { return Cell{Text: s} }
func textSub(s, sub string) Cell   { return Cell{Text: s, Sub: sub} }
func strong(s string) Cell         { return Cell{Text: s, Kind: CellStrong} }
func idCell(v any) Cell            { return Cell{Text: fmt.Sprint(v), Kind: CellID} }
func mute(s string) Cell           { return Cell{Text: s, Kind: CellMute} }
func num(v any) Cell               { return Cell{Text: fmt.Sprint(v)} }
func pill(s, tone string) Cell     { return Cell{Text: s, Kind: CellPill, Tone: tone} }
func chips(cs ...Chip) Cell        { return Cell{Kind: CellChips, Chips: cs} }

func tableBlock(title, subtitle string, table *Table, empty string) Block {
	return Block{Kind: BlockTable, Title: title, Subtitle: subtitle, Table: table, Empty: empty}
}

func wideTable(title string, table *Table, empty string) Block {
	b := tableBlock(title, "", table, empty)
	b.Wide = true
	return b
}

func chartBlock(title, subtitle string, chart *Chart) Block {
	return Block{Kind: BlockChart, Title: title, Subtitle: subtitle, Chart: chart}
}

func metersBlock(title, subtitle string, meters []Meter, empty string) Block {
	return Block{Kind: BlockMeters, Title: title, Subtitle: subtitle, Meters: meters, Empty: empty, Wide: true}
}

// nameList joins up to limit names and says how many more there are.
func nameList(names []string, limit int) string {
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:limit], ", "), len(names)-limit)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// --- Formatting ---

func formatBytesRate(b float64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB/s", b/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB/s", b/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB/s", b/float64(1<<10))
	default:
		return fmt.Sprintf("%.0f B/s", b)
	}
}

func formatBytes(b float64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.1f TB", b/float64(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", b/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", b/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", b/float64(1<<10))
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
}

// formatAge writes an age the way the report tables show it: "3 h",
// "2 d 4 h", "6 d". Never "-0d": a clock skew reads as zero.
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	if h < 24 {
		if h == 0 {
			return fmt.Sprintf("%d min", int(d.Minutes()))
		}
		return fmt.Sprintf("%d h", h)
	}
	days, rem := h/24, h%24
	if rem == 0 {
		return fmt.Sprintf("%d d", days)
	}
	return fmt.Sprintf("%d d %d h", days, rem)
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }

// formatThreshold writes a rule's threshold the way an operator set it: in
// hours below two days ("24 h", "36 h"), in days from there on.
func formatThreshold(d time.Duration) string {
	if d < 48*time.Hour {
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return formatAge(d)
}

// capacityTone colours a fill percentage: critical from 90 %, warning from 75 %.
func capacityTone(pct float64) string {
	switch {
	case pct >= 90:
		return ToneCrit
	case pct >= 75:
		return ToneWarn
	}
	return ToneAccent
}

// dayBuckets splits a window into UTC days (or seven-day chunks counted from
// the window's first day, when the window is longer than five weeks) and
// returns the bucket starts and their labels.
func dayBuckets(since, until time.Time) (starts []time.Time, labels []string, step time.Duration) {
	step = 24 * time.Hour
	if until.Sub(since) > 35*24*time.Hour {
		step = 7 * 24 * time.Hour
	}
	start := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.UTC)
	for t := start; t.Before(until); t = t.Add(step) {
		starts = append(starts, t)
		if step > 24*time.Hour {
			labels = append(labels, "wk "+t.Format("Jan 2"))
		} else {
			labels = append(labels, t.Format("Jan 2"))
		}
	}
	return starts, labels, step
}

func bucketIndex(starts []time.Time, step time.Duration, t time.Time) int {
	if len(starts) == 0 || t.Before(starts[0]) {
		return -1
	}
	i := int(t.Sub(starts[0]) / step)
	if i >= len(starts) {
		return -1
	}
	return i
}
