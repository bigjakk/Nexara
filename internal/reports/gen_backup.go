package reports

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/backupcoverage"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Backup compliance: every guest on the cluster against every provider, the
// runs that produced (or failed to produce) those backups, and where they
// live. Split into a fetch that reads the store and a pure build over
// backupInput so the report's every sentence can be tested without a
// database.

const (
	providerPBS   = "pbs"
	providerVeeam = "veeam"
	providerLocal = "local"
)

// backupRun is one job execution in the period, from either provider.
type backupRun struct {
	When     time.Time
	Job      string
	Provider string
	Detail   string // node for PVE, "Veeam backup" for Veeam
	Outcome  string // "ok", "warn", "fail", "running", "cancelled", "lost"
	Guests   string
	Message  string
}

// capacityItem is one place backups live.
type capacityItem struct {
	Name     string
	Detail   string
	Provider string
	Used     float64
	Total    float64
	History  []capSample
}

type capSample struct {
	T    time.Time
	Used float64
}

// orphanedObject is a Veeam backup object with no live guest.
type orphanedObject struct {
	Name     string
	Points   int32
	Bytes    int64
	LastSeen time.Time
}

// backupInput is everything the backup compliance build reads.
type backupInput struct {
	now, since   time.Time
	params       Params
	includeVeeam bool
	nodeCount    int
	entries      []backupcoverage.Entry
	nodeNames    map[uuid.UUID]string
	pbsServers   []string // "pbs01 (datastores store01, store02)"
	veeamServers []string // "vbr01 (Veeam 13.1)"
	runs         []backupRun
	capacity     []capacityItem
	orphans      []orphanedObject
	runsRead     bool // false when neither source could be read
}

func (g *Generator) generateBackupCompliance(ctx context.Context, gc genCtx, data *ReportData) error {
	in, err := g.fetchBackupInput(ctx, gc)
	if err != nil {
		return err
	}
	buildBackupCompliance(in, data)
	return nil
}

func (g *Generator) fetchBackupInput(ctx context.Context, gc genCtx) (backupInput, error) {
	in := backupInput{now: gc.now, since: gc.since, params: gc.params, nodeNames: map[uuid.UUID]string{}}
	in.includeVeeam = g.includeVeeam(ctx, gc)

	entries, err := backupcoverage.Compute(ctx, g.queries, backupcoverage.Scope{
		Cluster: backupcoverage.OneCluster(gc.cluster.ID),
		Veeam: func(id uuid.UUID) bool {
			return in.includeVeeam && id == gc.cluster.ID
		},
	}, backupcoverage.Options{
		StaleAfter: gc.params.StaleAfter(),
		Now:        gc.now,
		Logger:     g.logger,
		// Strict: a compliance document that quietly calls every Veeam-only
		// guest "no backup" because a table was unreadable would be a lie.
		// A failed run in the history is the honest outcome.
		VeeamStrict: true,
	})
	if err != nil {
		return in, fmt.Errorf("backup coverage: %w", err)
	}
	in.entries = entries

	nodes, err := g.queries.ListNodesByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return in, fmt.Errorf("list nodes: %w", err)
	}
	in.nodeCount = len(nodes)
	for _, n := range nodes {
		in.nodeNames[n.ID] = n.Name
	}

	// Which PBS datastores this cluster mounts, and which local pools hold
	// vzdump archives.
	pools, err := g.queries.ListStoragePoolsByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return in, fmt.Errorf("list storage pools: %w", err)
	}
	datastores := map[string]bool{}
	localPools := map[string]db.StoragePool{}
	for _, p := range pools {
		switch {
		case p.Type == "pbs":
			datastores[p.Storage] = true
		case strings.Contains(p.Content, "backup"):
			// Shared pools appear once per node; keep the fullest reading.
			if cur, ok := localPools[p.Storage]; !ok || p.Total > cur.Total {
				localPools[p.Storage] = p
			}
		}
	}
	for _, p := range localPools {
		detail := p.Type
		if p.Shared {
			detail += " · shared"
		} else if n, ok := in.nodeNames[p.NodeID]; ok {
			detail += " · " + n
		}
		if p.Total > 0 {
			in.capacity = append(in.capacity, capacityItem{Name: p.Storage, Detail: detail, Provider: providerLocal, Used: float64(p.Used), Total: float64(p.Total)})
		}
	}

	// PBS: which servers hold those datastores, and their capacity history.
	servers, err := g.queries.ListPBSServers(ctx)
	if err != nil {
		return in, fmt.Errorf("list PBS servers: %w", err)
	}
	for _, srv := range servers {
		latest, lErr := g.queries.GetLatestPBSDatastoreMetrics(ctx, srv.ID)
		if lErr != nil {
			// Skipping the server would let the report claim "no PBS datastore
			// mounted" on the strength of a database error.
			return in, fmt.Errorf("PBS datastore metrics for %s: %w", srv.Name, lErr)
		}
		var served []string
		for _, m := range latest {
			if !datastores[m.Datastore] {
				continue
			}
			served = append(served, m.Datastore)
			item := capacityItem{Name: m.Datastore, Detail: "PBS datastore · " + srv.Name, Provider: providerPBS, Used: float64(m.Used), Total: float64(m.Total)}
			hist, hErr := g.queries.GetPBSDatastoreMetricsHistory(ctx, db.GetPBSDatastoreMetricsHistoryParams{
				BucketSeconds: 3600,
				PbsServerID:   srv.ID,
				StartTime:     gc.since,
				EndTime:       gc.now,
			})
			if hErr == nil {
				for _, h := range hist {
					if h.Datastore == m.Datastore {
						item.History = append(item.History, capSample{T: h.Time, Used: float64(h.Used)})
					}
				}
			}
			in.capacity = append(in.capacity, item)
		}
		if len(served) > 0 {
			sort.Strings(served)
			in.pbsServers = append(in.pbsServers, fmt.Sprintf("%s (PBS, %s)", srv.Name, joinLabel("datastore", served)))
		}
	}

	// Veeam: the servers whose platforms map to this cluster, their
	// repositories, and the guests they hold backups for that no longer exist.
	if in.includeVeeam {
		platforms, pErr := g.queries.ListVeeamPlatformsForCluster(ctx, gc.cluster.ID)
		if pErr != nil {
			return in, fmt.Errorf("list Veeam platforms: %w", pErr)
		}
		seen := map[uuid.UUID]bool{}
		for _, p := range platforms {
			if seen[p.VeeamServerID] {
				continue
			}
			seen[p.VeeamServerID] = true
			label := p.ServerName + " (Veeam"
			if p.ServerVersion != "" {
				label += " " + shortVersion(p.ServerVersion)
			}
			in.veeamServers = append(in.veeamServers, label+")")

			repos, rErr := g.queries.ListVeeamRepositoriesByServer(ctx, p.VeeamServerID)
			if rErr != nil {
				g.logger.Warn("report: Veeam repositories unreadable", "veeam_server_id", p.VeeamServerID, "error", rErr)
			}
			for _, r := range repos {
				if r.CapacityBytes <= 0 {
					continue
				}
				item := capacityItem{Name: r.Name, Detail: "Veeam repository · " + r.RepoType + " · " + p.ServerName, Provider: providerVeeam, Used: float64(r.UsedBytes), Total: float64(r.CapacityBytes)}
				hist, hErr := g.queries.GetVeeamRepositoryMetrics(ctx, db.GetVeeamRepositoryMetricsParams{
					VeeamServerID:     p.VeeamServerID,
					RepositoryVeeamID: r.VeeamID,
					Bucket:            gc.since,
				})
				if hErr == nil {
					for _, h := range hist {
						item.History = append(item.History, capSample{T: h.Time, Used: float64(h.UsedBytes)})
					}
				}
				in.capacity = append(in.capacity, item)
			}

			orphans, oErr := g.queries.ListVeeamOrphanedObjects(ctx, p.VeeamServerID)
			if oErr != nil {
				g.logger.Warn("report: Veeam orphaned objects unreadable", "veeam_server_id", p.VeeamServerID, "error", oErr)
			}
			for _, o := range orphans {
				if !o.ClusterID.Valid || uuid.UUID(o.ClusterID.Bytes) != gc.cluster.ID {
					continue
				}
				in.orphans = append(in.orphans, orphanedObject{Name: o.Name, Points: o.RestorePointsCount, Bytes: o.RestorePointBytes, LastSeen: o.LastSeenAt})
			}
		}
	}

	// Runs: vzdump tasks and Veeam sessions that ended in the window.
	if gc.params.SectionEnabled("runs") {
		tasks, tErr := g.queries.ListTaskHistoryByTypeInWindow(ctx, db.ListTaskHistoryByTypeInWindowParams{
			ClusterID: gc.cluster.ID, TaskType: "vzdump", Since: gc.since, Until: gc.now, RowLimit: 5000,
		})
		if tErr != nil {
			return in, fmt.Errorf("list backup tasks: %w", tErr)
		}
		in.runsRead = true
		for _, t := range tasks {
			in.runs = append(in.runs, pveBackupRun(t, in.entries))
		}
		if in.includeVeeam {
			sessions, sErr := g.queries.ListVeeamSessionsForClusterInWindow(ctx, db.ListVeeamSessionsForClusterInWindowParams{
				ClusterID: gc.cluster.ID, Since: gc.since, Until: gc.now, RowLimit: 5000,
			})
			if sErr != nil {
				return in, fmt.Errorf("list Veeam sessions: %w", sErr)
			}
			for _, s := range sessions {
				in.runs = append(in.runs, veeamBackupRun(s))
			}
		}
		sort.Slice(in.runs, func(i, j int) bool { return in.runs[i].When.After(in.runs[j].When) })
	}

	return in, nil
}

func joinLabel(noun string, items []string) string {
	if len(items) == 1 {
		return noun + " " + items[0]
	}
	return noun + "s " + strings.Join(items, ", ")
}

// shortVersion trims a build number to its first two components: "13.1".
func shortVersion(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) > 2 {
		return strings.Join(parts[:2], ".")
	}
	return v
}

// pveBackupRun classifies one vzdump task by the same rule the Tasks page
// uses: an exit of "WARNINGS: N" is a success that warned, everything the
// collector marked failed is a failure, and a task the collector lost track
// of is neither.
func pveBackupRun(t db.TaskHistory, entries []backupcoverage.Entry) backupRun {
	when := t.StartedAt
	if t.FinishedAt.Valid {
		when = t.FinishedAt.Time
	}
	run := backupRun{When: when, Provider: providerPBS, Detail: "vzdump · " + t.Node, Message: t.ExitStatus}
	run.Job = t.Description
	if run.Job == "" {
		run.Job = "vzdump"
	}
	switch {
	case t.Status == "running":
		run.Outcome = "running"
	case t.ExitStatus == "vanished":
		run.Outcome = "lost"
		run.Message = "Nexara lost track of this task before it finished"
	case t.Status == "failed" || !proxmox.TaskSucceeded(t.ExitStatus):
		run.Outcome = "fail"
	case strings.HasPrefix(strings.ToUpper(strings.TrimSpace(t.ExitStatus)), "WARNINGS"):
		run.Outcome = "warn"
	default:
		run.Outcome = "ok"
	}
	if t.Vmid.Valid {
		run.Guests = fmt.Sprintf("#%d", t.Vmid.Int32)
		for _, e := range entries {
			if e.VMID == t.Vmid.Int32 && e.Name != "" {
				run.Guests = fmt.Sprintf("%s (%d)", e.Name, e.VMID)
				break
			}
		}
	}
	return run
}

// veeamBackupRun classifies one Veeam session. A run Nexara itself stopped is
// recorded by Veeam as Failed with nothing to distinguish it from a real
// failure, so nexara_stopped is the only signal and it wins.
func veeamBackupRun(s db.VeeamSession) backupRun {
	when := s.CreationTime
	if s.EndTime.Valid {
		when = s.EndTime.Time
	}
	run := backupRun{When: when, Job: s.Name, Provider: providerVeeam, Detail: "Veeam backup", Message: s.ResultMessage}
	switch {
	case s.NexaraStopped:
		run.Outcome = "cancelled"
		run.Message = "Stopped from Nexara"
	case s.State != "" && s.State != "Stopped":
		run.Outcome = "running"
	case s.Result == "Failed":
		run.Outcome = "fail"
	case s.Result == "Warning":
		run.Outcome = "warn"
	case s.Result == "Success":
		run.Outcome = "ok"
	default:
		// "None" or an empty result on a stopped session: Veeam recorded no
		// verdict, and inventing one either way would be a claim.
		run.Outcome = "unknown"
	}
	return run
}

// --- build ---

// rpoBucket is one bar of the newest-restore-point histogram.
type rpoBucket struct {
	label string
	upTo  time.Duration // exclusive upper bound; 0 means "never"
}

var rpoBuckets = []rpoBucket{
	{"< 6 h", 6 * time.Hour},
	{"6–12 h", 12 * time.Hour},
	{"12–24 h", 24 * time.Hour},
	{"1–2 d", 48 * time.Hour},
	{"2–7 d", 7 * 24 * time.Hour},
	{"> 7 d", 1<<62 - 1},
	{"never", 0},
}

func buildBackupCompliance(in backupInput, data *ReportData) {
	staleAfter := in.params.StaleAfter()
	staleLabel := formatThreshold(staleAfter)

	// --- counts ---
	var targets, recent, stale, none, excluded int
	var pbsOnly, veeamOnly, both, pbsOnlyStale, veeamOnlyStale, bothStale int
	var noneNames, staleNames, nameMatches, failedVeeam, malware []string
	var oldest time.Duration
	hist := make([]float64, len(rpoBuckets))
	for _, e := range in.entries {
		if e.Eligibility != backupcoverage.Eligible {
			excluded++
			continue
		}
		targets++
		age := in.now.Sub(time.Unix(e.Freshest, 0))
		switch e.CoverageStatus {
		case backupcoverage.StatusRecent:
			recent++
		case backupcoverage.StatusStale:
			stale++
			staleNames = append(staleNames, fmt.Sprintf("%s (%s)", guestLabel(e), formatAge(age)))
			if age > oldest {
				oldest = age
			}
		case backupcoverage.StatusNone:
			none++
			noneNames = append(noneNames, guestLabel(e))
		}
		switch e.Protection {
		case backupcoverage.ProtectionPBS:
			pbsOnly++
			if e.CoverageStatus == backupcoverage.StatusStale {
				pbsOnlyStale++
			}
		case backupcoverage.ProtectionVeeam:
			veeamOnly++
			if e.CoverageStatus == backupcoverage.StatusStale {
				veeamOnlyStale++
			}
		case backupcoverage.ProtectionBoth:
			both++
			if e.CoverageStatus == backupcoverage.StatusStale {
				bothStale++
			}
		}
		if e.Freshest == 0 {
			hist[len(hist)-1]++
		} else {
			for i, b := range rpoBuckets {
				if b.upTo > 0 && age < b.upTo {
					hist[i]++
					break
				}
			}
		}
		if e.Veeam != nil {
			if e.Veeam.MatchMethod == "name" {
				nameMatches = append(nameMatches, guestLabel(e))
			}
			if e.Veeam.LastRunFailed {
				failedVeeam = append(failedVeeam, guestLabel(e))
			}
			if e.Veeam.MalwareStatus == "Suspicious" || e.Veeam.MalwareStatus == "Infected" {
				malware = append(malware, fmt.Sprintf("%s (%s)", guestLabel(e), e.Veeam.MalwareStatus))
			}
		}
	}

	// --- runs ---
	var runsOK, runsWarn, runsFail, runsOther int
	failedJobs := map[string]bool{}
	for _, r := range in.runs {
		switch r.Outcome {
		case "ok":
			runsOK++
		case "warn":
			runsWarn++
		case "fail":
			runsFail++
			failedJobs[r.Job] = true
		default:
			runsOther++
		}
	}
	runsTotal := len(in.runs)

	// --- capacity ---
	var fullest *capacityItem
	meters := make([]Meter, 0, len(in.capacity))
	type projection struct {
		item *capacityItem
		days float64
	}
	var projections []projection
	sort.SliceStable(in.capacity, func(i, j int) bool {
		return in.capacity[i].Used/in.capacity[i].Total > in.capacity[j].Used/in.capacity[j].Total
	})
	for i := range in.capacity {
		c := &in.capacity[i]
		if c.Total <= 0 {
			continue
		}
		pct := c.Used / c.Total * 100
		if fullest == nil || pct > fullest.Used/fullest.Total*100 {
			fullest = c
		}
		m := Meter{Name: c.Name, Detail: c.Detail, Used: c.Used, Total: c.Total, Tone: capacityTone(pct),
			UsedText: fmt.Sprintf("%s of %s", formatBytes(c.Used), formatBytes(c.Total))}
		perDay, days, state := projectFull(c)
		switch state {
		case projectionNoHistory:
			m.Projection = "Not enough history for a trend"
		case projectionFlat:
			m.Projection = "No growth over the period"
		case projectionFull:
			m.Projection = "Already full"
			m.ProjectionWarn = true
		case projectionGrowing:
			if days > 3650 {
				m.Projection = fmt.Sprintf("+%s/day · full in over 10 years", formatBytes(perDay))
				break
			}
			when := in.now.Add(time.Duration(days*24) * time.Hour)
			m.Projection = fmt.Sprintf("+%s/day → full in ~%.0f d (%s)", formatBytes(perDay), days, when.Format("2006-01-02"))
			m.ProjectionWarn = days < 90
			projections = append(projections, projection{item: c, days: days})
		}
		meters = append(meters, m)
	}

	// --- masthead ---
	scope := fmt.Sprintf("%s · %s · %s", plural(in.nodeCount, "node", "nodes"), plural(len(in.entries), "guest", "guests"), plural(targets, "backup target", "backup targets"))
	data.addMeta("Scope", scope)
	providers := append(append([]string{}, in.pbsServers...), in.veeamServers...)
	switch {
	case len(providers) > 0:
		data.addMeta("Providers consulted", strings.Join(providers, " · "))
	case in.includeVeeam:
		data.addMeta("Providers consulted", "No PBS datastore mounted and no Veeam platform mapped to this cluster")
	default:
		data.addMeta("Providers consulted", "Proxmox Backup Server only")
	}
	data.addMeta("Freshness rule", fmt.Sprintf("Stale after %s without a restore point", staleLabel))
	data.Subtitle = "Every guest on the cluster, checked against Proxmox Backup Server"
	if in.includeVeeam {
		data.Subtitle += " and Veeam Backup & Replication"
	}
	data.Subtitle += "."

	// --- KPIs ---
	pct := 0.0
	anyPct := 0.0
	if targets > 0 {
		pct = float64(recent) / float64(targets) * 100
		anyPct = float64(recent+stale) / float64(targets) * 100
	}
	data.KPIs = append(data.KPIs, KPI{Label: "Current within " + staleLabel, Value: fmt.Sprintf("%.0f", pct), Unit: "%", Hero: true,
		Detail: fmt.Sprintf("%d of %d backup targets · %.0f %% have at least one backup", recent, targets, anyPct)})
	targetKPI := KPI{Label: "Backup targets", Value: fmt.Sprint(targets)}
	if excluded > 0 {
		targetKPI.Detail = fmt.Sprintf("+%d excluded (Veeam infrastructure)", excluded)
	}
	data.KPIs = append(data.KPIs, targetKPI,
		KPI{Label: "Protected", Value: fmt.Sprint(recent), Tone: ToneGood, Detail: "newest point < " + staleLabel},
		KPI{Label: "Stale", Value: fmt.Sprint(stale), Tone: toneIf(stale > 0, ToneWarn), Detail: staleDetail(oldest)},
		KPI{Label: "No backup", Value: fmt.Sprint(none), Tone: toneIf(none > 0, ToneCrit), Detail: "from any provider"},
	)
	if in.runsRead {
		data.KPIs = append(data.KPIs, KPI{Label: "Runs this period", Value: fmt.Sprint(runsTotal),
			Detail: fmt.Sprintf("%d ok · %d warned · %d failed", runsOK, runsWarn, runsFail), Tone: toneIf(runsFail > 0, ToneWarn)})
	}
	if fullest != nil {
		fpct := fullest.Used / fullest.Total * 100
		k := KPI{Label: "Fullest repository", Value: fmt.Sprintf("%.0f", fpct), Unit: "%", Detail: fullest.Name, Tone: capacityTone(fpct)}
		if k.Tone == ToneAccent {
			k.Tone = ""
		}
		data.KPIs = append(data.KPIs, k)
	}

	// --- findings ---
	if none > 0 {
		data.addFinding(SevCritical, fmt.Sprintf("%s no backup from any provider:", pluralHave(none, "backup target")), nameList(noneNames, 5)+".", "Guests table")
	}
	for _, p := range projections {
		pctFull := p.item.Used / p.item.Total * 100
		sev := ""
		switch {
		case p.days < 30 || pctFull >= 90:
			sev = SevCritical
		case p.days < 60 || pctFull >= 80:
			sev = SevSerious
		case p.days < 90 || pctFull >= 75:
			sev = SevWarning
		}
		if sev != "" {
			data.addFinding(sev, fmt.Sprintf("%s is %.0f %% full", p.item.Name, pctFull),
				fmt.Sprintf("and at the last %s' growth rate it is full in about %.0f days (around %s).", periodDays(in.since, in.now), p.days, in.now.Add(time.Duration(p.days*24)*time.Hour).Format("2006-01-02")), "Repository capacity")
		}
	}
	for i := range in.capacity {
		c := &in.capacity[i]
		if c.Total <= 0 {
			continue
		}
		pctFull := c.Used / c.Total * 100
		already := false
		for _, p := range projections {
			if p.item == c {
				already = true
			}
		}
		switch {
		case already:
		case pctFull >= 90:
			data.addFinding(SevCritical, fmt.Sprintf("%s is %.0f %% full", c.Name, pctFull), "with no growth trend to project from.", "Repository capacity")
		case pctFull >= 80:
			data.addFinding(SevSerious, fmt.Sprintf("%s is %.0f %% full.", c.Name, pctFull), "", "Repository capacity")
		case pctFull >= 75:
			data.addFinding(SevWarning, fmt.Sprintf("%s is %.0f %% full.", c.Name, pctFull), "", "Repository capacity")
		}
	}
	if stale > 0 {
		data.addFinding(SevSerious, fmt.Sprintf("%s stale (newest restore point older than %s):", pluralAre(stale, "guest"), staleLabel), nameList(staleNames, 5)+".", "Guests table")
	}
	if len(malware) > 0 {
		sev := SevSerious
		if strings.Contains(strings.Join(malware, " "), "Infected") {
			sev = SevCritical
		}
		data.addFinding(sev, fmt.Sprintf("Veeam flagged the newest restore point of %s:", plural(len(malware), "guest", "guests")), nameList(malware, 5)+".", "Veeam findings · malware detection")
	}
	if len(failedVeeam) > 0 {
		data.addFinding(SevSerious, fmt.Sprintf("Veeam's last run failed for %s:", plural(len(failedVeeam), "guest", "guests")), nameList(failedVeeam, 5)+".", "Backup runs")
	}
	if runsFail > 0 {
		jobs := make([]string, 0, len(failedJobs))
		for j := range failedJobs {
			jobs = append(jobs, j)
		}
		sort.Strings(jobs)
		data.addFinding(SevWarning, fmt.Sprintf("%d of %d backup runs failed this period", runsFail, runsTotal), "("+nameList(jobs, 4)+").", "Backup runs · failed and warned runs")
	}
	if len(nameMatches) > 0 {
		data.addFinding(SevWarning, fmt.Sprintf("%s linked to a Veeam backup by name only:", pluralAre(len(nameMatches), "guest")), nameList(nameMatches, 5)+". A rebuilt host reuses its name, so verify the mapping before relying on it.", "Guests table · name match")
	}
	if len(in.orphans) > 0 {
		var bytes int64
		names := make([]string, 0, len(in.orphans))
		for _, o := range in.orphans {
			bytes += o.Bytes
			names = append(names, o.Name)
		}
		data.addFinding(SevInfo, fmt.Sprintf("%s of Veeam restore points belong to %s that no longer exist on this cluster:", formatBytes(float64(bytes)), plural(len(in.orphans), "guest", "guests")), nameList(names, 5)+".", "Veeam findings · orphaned objects")
	}

	// --- coverage section ---
	coverageChart := &Chart{Kind: ChartStackedBar, Total: float64(targets), Legend: true,
		AriaLabel: fmt.Sprintf("Coverage: %d protected, %d stale, %d without backup", recent, stale, none),
		Series: []Series{
			{Name: "Protected", Tone: ToneGood, Values: []float64{float64(recent)}},
			{Name: "Stale", Tone: ToneWarn, Values: []float64{float64(stale)}},
			{Name: "No backup", Tone: ToneCrit, Values: []float64{float64(none)}},
		}}
	splitRows := [][]Cell{
		{chips(Chip{Text: "PBS", Kind: ChipPBS}, Chip{Text: "only", Kind: ""}), num(pbsOnly), num(pbsOnly - pbsOnlyStale), num(pbsOnlyStale)},
	}
	if in.includeVeeam {
		splitRows = append(splitRows,
			[]Cell{chips(Chip{Text: "Veeam", Kind: ChipVeeam}, Chip{Text: "only", Kind: ""}), num(veeamOnly), num(veeamOnly - veeamOnlyStale), num(veeamOnlyStale)},
			[]Cell{chips(Chip{Text: "PBS", Kind: ChipPBS}, Chip{Text: "Veeam", Kind: ChipVeeam}, Chip{Text: "both", Kind: ""}), num(both), num(both - bothStale), num(bothStale)},
		)
	}
	splitRows = append(splitRows, []Cell{text("No provider"), num(none), mute("—"), mute("—")})
	coverageSub := ""
	if excluded > 0 {
		coverageSub = fmt.Sprintf("%s excluded from the denominator (Veeam infrastructure).", plural(excluded, "guest", "guests"))
	}
	histLabels := make([]string, len(rpoBuckets))
	for i, b := range rpoBuckets {
		histLabels[i] = b.label
	}
	thresholdIndex := 2 // after the 12–24 h bucket, for the default 24 h rule
	aligned := false
	for i, b := range rpoBuckets {
		if b.upTo > 0 && b.upTo <= staleAfter {
			thresholdIndex = i
			aligned = b.upTo == staleAfter
		}
	}
	histSubtitle := fmt.Sprintf("Guests per age bucket. The line marks the %s stale rule.", staleLabel)
	if aligned {
		histSubtitle = fmt.Sprintf("Guests per age bucket. Everything right of the line is stale under the %s rule.", staleLabel)
	}
	data.addSection("Coverage",
		"Freshness is judged on the newest restore point from any provider, so a guest that only one provider protects still counts as protected.",
		Block{Kind: BlockChart, Title: fmt.Sprintf("%s by coverage state", plural(targets, "backup target", "backup targets")), Subtitle: coverageSub, Chart: coverageChart},
		Block{Kind: BlockChart, Title: "Age of the newest restore point", Subtitle: histSubtitle,
			Chart: &Chart{Kind: ChartHist, Categories: histLabels, Series: []Series{{Name: "Guests", Tone: ToneAccent, Values: hist}},
				Threshold: &Threshold{AfterIndex: thresholdIndex, Label: "stale after " + staleLabel}, Footnote: fmt.Sprintf("guests · %d backup targets", targets),
				AriaLabel: "Newest restore point age per guest"}},
		tableBlock("Protected by", "", &Table{Columns: []Column{col("Provider"), numCol("Guests"), numCol("Current"), numCol("Stale")}, Rows: splitRows}, ""),
	)

	// --- runs section ---
	if in.runsRead {
		buildRunsSection(in, data, runsOK, runsWarn, runsFail, runsOther)
	}

	// --- capacity section ---
	if len(meters) > 0 {
		data.addSection("Repository capacity", "Where the backups live, how full it is, and when it fills at the period's growth rate.",
			metersBlock("", "", meters, ""))
	}

	// --- guests table ---
	data.addSection("Guests", "Problems first. \"Newest backup\" is the freshest point from any provider; the PBS and Veeam columns count what each one holds.",
		wideTable("", buildGuestTable(in), "No guests on this cluster."))

	// --- veeam findings ---
	if in.includeVeeam && (len(malware) > 0 || len(in.orphans) > 0) {
		malwareRows := [][]Cell{}
		for _, e := range in.entries {
			if e.Veeam == nil || (e.Veeam.MalwareStatus != "Suspicious" && e.Veeam.MalwareStatus != "Infected") {
				continue
			}
			tone := ToneWarn
			if e.Veeam.MalwareStatus == "Infected" {
				tone = ToneCrit
			}
			point := ""
			if e.Veeam.LatestRestorePoint != nil {
				point = ts(*e.Veeam.LatestRestorePoint)
			}
			malwareRows = append(malwareRows, []Cell{textSub(e.Name, fmt.Sprint(e.VMID)), pill(e.Veeam.MalwareStatus, tone), mute(point)})
		}
		orphanRows := make([][]Cell, 0, len(in.orphans))
		for _, o := range in.orphans {
			orphanRows = append(orphanRows, []Cell{strong(o.Name), num(o.Points), num(formatBytes(float64(o.Bytes))), mute("No guest on this cluster carries its SMBIOS UUID; last seen " + o.LastSeen.Format("2006-01-02"))})
		}
		data.addSection("Veeam findings", "Malware verdicts ride on every restore point; only the newest point per guest is judged, so a superseded \"Suspicious\" does not linger.",
			tableBlock("Malware detection, newest point per guest", "", &Table{Columns: []Column{col("Guest"), col("Verdict"), col("Point")}, Rows: malwareRows}, "Every newest restore point is clean."),
			tableBlock("Orphaned backup objects", "", &Table{Columns: []Column{col("Backup object"), numCol("Points"), numCol("Size"), col("Why orphaned")}, Rows: orphanRows}, "No restore points are held for guests that no longer exist."),
		)
	}

	// --- notes ---
	method := []string{
		fmt.Sprintf("A guest is current when its newest restore point from any provider is under %s old (parameter stale_after_hours), stale otherwise, and has no backup when neither provider holds a point.", staleLabel),
		"PBS snapshots are matched through the datastores this cluster mounts, so a same-numbered guest on another cluster never counts.",
	}
	if in.includeVeeam {
		method = append(method,
			"Veeam objects are matched on the guest's SMBIOS UUID; a name-only match is shown with a dashed flag and never counted as deterministic.",
			"Veeam worker appliances and the backup server are not backup targets and are listed but excluded from every percentage.")
	} else {
		method = append(method, "Veeam data is included when the report's requester holds view:veeam on the cluster; this run consulted Proxmox Backup Server only.")
	}
	sources := []string{
		"Inventory as of the collector pass at " + data.GeneratedAt + ".",
		"Run outcomes: Proxmox task history (vzdump) and Veeam sessions that ended in the period; a run that ended with warnings counts as succeeded, as on the Tasks page.",
		fmt.Sprintf("Capacity projections are a least-squares line through the period's datastore and repository samples (%s).", periodDays(in.since, in.now)),
		"All times UTC.",
	}
	data.Notes = []NoteGroup{{Title: "How this report was computed", Items: method}, {Title: "Sources and times", Items: sources}}
}

func buildRunsSection(in backupInput, data *ReportData, ok, warn, fail, other int) {
	starts, labels, step := dayBuckets(in.since, in.now)
	okS := make([]float64, len(starts))
	warnS := make([]float64, len(starts))
	failS := make([]float64, len(starts))
	type jobStats struct {
		name, provider, detail string
		runs, ok, warn, fail   int
		last                   backupRun
	}
	jobs := map[string]*jobStats{}
	var jobOrder []string
	var problems [][]Cell
	for _, r := range in.runs {
		if i := bucketIndex(starts, step, r.When); i >= 0 {
			switch r.Outcome {
			case "ok":
				okS[i]++
			case "warn":
				warnS[i]++
			case "fail":
				failS[i]++
			}
		}
		key := r.Provider + ":" + r.Job
		j, seen := jobs[key]
		if !seen {
			j = &jobStats{name: r.Job, provider: r.Provider, detail: r.Detail, last: r}
			jobs[key] = j
			jobOrder = append(jobOrder, key)
		}
		j.runs++
		switch r.Outcome {
		case "ok":
			j.ok++
		case "warn":
			j.warn++
		case "fail":
			j.fail++
		}
		if r.When.After(j.last.When) {
			j.last = r
		}
		if r.Outcome == "fail" || r.Outcome == "warn" || r.Outcome == "lost" {
			problems = append(problems, []Cell{mute(ts(r.When)), strong(r.Job), outcomePill(r.Outcome), text(r.Guests), text(r.Message)})
		}
	}
	sort.SliceStable(jobOrder, func(a, b int) bool { return jobs[jobOrder[a]].name < jobs[jobOrder[b]].name })
	jobRows := make([][]Cell, 0, len(jobOrder))
	for _, key := range jobOrder {
		j := jobs[key]
		chip := Chip{Text: "PBS", Kind: ChipPBS}
		if j.provider == providerVeeam {
			chip = Chip{Text: "Veeam", Kind: ChipVeeam}
		}
		jobRows = append(jobRows, []Cell{
			{Text: j.name, Kind: CellStrong, Sub: j.detail},
			{Kind: CellChips, Chips: []Chip{chip}},
			num(j.runs), num(j.ok), num(j.warn), num(j.fail),
			outcomePillSub(j.last.Outcome, ts(j.last.When)),
		})
	}
	subtitle := fmt.Sprintf("%s · %d succeeded · %d warned · %d failed", plural(len(in.runs), "run", "runs"), ok, warn, fail)
	if other > 0 {
		subtitle += fmt.Sprintf(" · %d running, cancelled or lost", other)
	}
	why := "Proxmox vzdump tasks from task history"
	if in.includeVeeam {
		why += " and Veeam job sessions"
	}
	why += ", judged by the same success rule the Tasks page uses (a run that only warned still produced a backup)."
	blocks := []Block{
		chartBlock("Runs per period, by outcome", subtitle, &Chart{Kind: ChartColumns, Categories: labels, Legend: true, AriaLabel: "Backup runs per day by outcome",
			Series:   []Series{{Name: "Succeeded", Tone: ToneGood, Values: okS}, {Name: "Warnings", Tone: ToneWarn, Values: warnS}, {Name: "Failed", Tone: ToneCrit, Values: failS}},
			Footnote: "runs per day, UTC"}),
		tableBlock("Jobs", "Every job that ran against this cluster, with its last outcome.",
			&Table{Columns: []Column{nowrapCol("Job"), col("Provider"), numCol("Runs"), numCol("OK"), numCol("Warn"), numCol("Fail"), nowrapCol("Last run")}, Rows: jobRows}, "No backup runs ended in this period."),
	}
	if len(problems) > 0 {
		blocks = append(blocks, wideTable("Failed and warned runs", &Table{Columns: []Column{nowrapCol("When (UTC)"), col("Job"), col("Outcome"), col("Guest"), col("What Proxmox or Veeam said")}, Rows: problems}, ""))
	}
	data.addSection("Backup runs in the period", why, blocks...)
}

func outcomePill(outcome string) Cell {
	switch outcome {
	case "ok":
		return pill("Succeeded", ToneGood)
	case "warn":
		return pill("Warning", ToneWarn)
	case "fail":
		return pill("Failed", ToneCrit)
	case "running":
		return pill("Running", ToneNeutral)
	case "cancelled":
		return pill("Stopped", ToneNeutral)
	case "unknown":
		return pill("No result", ToneNeutral)
	}
	return pill("Lost track", ToneNeutral)
}

func outcomePillSub(outcome, sub string) Cell {
	c := outcomePill(outcome)
	c.Sub = sub
	return c
}

// buildGuestTable lists every guest, problems first, then not-a-target rows
// last so the Veeam appliances read as context rather than as alarms.
func buildGuestTable(in backupInput) *Table {
	order := map[string]int{backupcoverage.StatusNone: 0, backupcoverage.StatusStale: 1, backupcoverage.StatusRecent: 2, backupcoverage.StatusNotEligible: 3}
	rows := make([]backupcoverage.Entry, len(in.entries))
	copy(rows, in.entries)
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if order[a.CoverageStatus] != order[b.CoverageStatus] {
			return order[a.CoverageStatus] < order[b.CoverageStatus]
		}
		if a.Freshest != b.Freshest {
			// Oldest first within a status.
			return a.Freshest < b.Freshest
		}
		return a.VMID < b.VMID
	})
	cols := []Column{nowrapCol("VMID"), nowrapCol("Guest"), col("Type"), nowrapCol("Node"), nowrapCol("Coverage"), col("Protected by"), nowrapCol("Newest backup"), numCol("PBS")}
	if in.includeVeeam {
		cols = append(cols, numCol("Veeam"))
	}
	cols = append(cols, col("Notes"))
	out := make([][]Cell, 0, len(rows))
	for _, e := range rows {
		kind := "VM"
		if e.Type == "lxc" {
			kind = "CT"
		}
		var coverage Cell
		switch e.CoverageStatus {
		case backupcoverage.StatusRecent:
			coverage = pill("Protected", ToneGood)
		case backupcoverage.StatusStale:
			coverage = pill("Stale", ToneWarn)
		case backupcoverage.StatusNone:
			coverage = pill("No backup", ToneCrit)
		default:
			coverage = pill("Not a target", ToneNeutral)
		}
		var prov Cell
		switch e.Eligibility {
		case backupcoverage.VeeamWorker:
			prov = Cell{Kind: CellChips, Text: "Veeam worker appliance"}
		case backupcoverage.VeeamBackupServer:
			prov = Cell{Kind: CellChips, Text: "Veeam backup server"}
		default:
			var cs []Chip
			if e.BackupCount > 0 {
				cs = append(cs, Chip{Text: "PBS", Kind: ChipPBS})
			}
			if e.Veeam != nil && e.Veeam.Protected {
				cs = append(cs, Chip{Text: "Veeam", Kind: ChipVeeam})
				if e.Veeam.MatchMethod == "name" {
					cs = append(cs, Chip{Text: "name match", Kind: ChipFlag})
				}
			}
			if len(cs) == 0 {
				prov = mute("—")
			} else {
				prov = chips(cs...)
			}
		}
		var newest Cell
		switch {
		case e.Eligibility != backupcoverage.Eligible:
			newest = mute("n/a")
		case e.Freshest == 0:
			newest = mute("never")
		default:
			t := time.Unix(e.Freshest, 0).UTC()
			newest = textSub(formatAge(in.now.Sub(t))+" ago", ts(t))
		}
		pbsCount := mute("—")
		if e.BackupCount > 0 {
			pbsCount = num(e.BackupCount)
		}
		row := []Cell{idCell(e.VMID), strong(e.Name), chips(Chip{Text: kind, Kind: ChipType}), text(in.nodeNames[e.NodeID]), coverage, prov, newest, pbsCount}
		if in.includeVeeam {
			switch {
			case e.Veeam != nil && e.Veeam.RestorePointCount > 0:
				row = append(row, num(e.Veeam.RestorePointCount))
			case !e.VeeamCapable && e.Eligibility == backupcoverage.Eligible:
				row = append(row, mute("n/a"))
			default:
				row = append(row, mute("—"))
			}
		}
		row = append(row, mute(guestNote(e)))
		out = append(out, row)
	}
	return &Table{Columns: cols, Rows: out}
}

// guestNote is the Notes column: the reason a guest needs a look, in words.
func guestNote(e backupcoverage.Entry) string {
	var notes []string
	if e.Veeam != nil {
		if e.Veeam.MalwareStatus == "Suspicious" || e.Veeam.MalwareStatus == "Infected" {
			notes = append(notes, "Newest Veeam point flagged "+e.Veeam.MalwareStatus)
		}
		if e.Veeam.LastRunFailed {
			notes = append(notes, "Last Veeam run for this guest failed")
		}
		if e.Veeam.MatchMethod == "name" {
			notes = append(notes, "Veeam link is a name match; verify")
		}
		if e.Veeam.RestorePointCount == 0 && e.Veeam.MatchMethod != "none" {
			notes = append(notes, "Veeam knows this guest but holds no restore points")
		}
	}
	if e.Status == "stopped" && e.CoverageStatus == backupcoverage.StatusNone {
		notes = append(notes, "Stopped guest")
	}
	return strings.Join(notes, " · ")
}

func guestLabel(e backupcoverage.Entry) string {
	if e.Name == "" {
		return fmt.Sprintf("#%d", e.VMID)
	}
	return fmt.Sprintf("%s (%d)", e.Name, e.VMID)
}

func toneIf(cond bool, tone string) string {
	if cond {
		return tone
	}
	return ""
}

func staleDetail(oldest time.Duration) string {
	if oldest <= 0 {
		return "none overdue"
	}
	return "oldest " + formatAge(oldest)
}

func pluralHave(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s has", noun)
	}
	return fmt.Sprintf("%d %ss have", n, noun)
}

func pluralAre(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s is", noun)
	}
	return fmt.Sprintf("%d %ss are", n, noun)
}

func periodDays(since, until time.Time) string {
	d := until.Sub(since)
	if d >= 24*time.Hour {
		return fmt.Sprintf("%.0f days", d.Hours()/24)
	}
	return fmt.Sprintf("%.0f hours", d.Hours())
}

// Projection states, as projectFull reports them.
const (
	projectionNoHistory = "no-history"
	projectionFlat      = "flat"
	projectionFull      = "full"
	projectionGrowing   = "growing"
)

// projectFull fits a line through the used-bytes history and returns the
// growth per day and the days until the total is reached. Anything under
// 0.01 % of capacity a day is reported as flat rather than as a projection:
// a datastore gaining a gigabyte a day on ten terabytes must not be told it
// fills "in about 0 days".
func projectFull(c *capacityItem) (perDay, days float64, state string) {
	if len(c.History) < 2 {
		return 0, 0, projectionNoHistory
	}
	start := c.History[0].T
	xs := make([]float64, len(c.History))
	ys := make([]float64, len(c.History))
	for i, s := range c.History {
		xs[i] = s.T.Sub(start).Hours() / 24
		ys[i] = s.Used
	}
	// A history shorter than six hours is noise, not a trend.
	if xs[len(xs)-1] < 0.25 {
		return 0, 0, projectionNoHistory
	}
	slope, _, fit := LinearRegression(xs, ys)
	if !fit {
		return 0, 0, projectionNoHistory
	}
	if slope <= c.Total*0.0001 {
		return 0, 0, projectionFlat
	}
	free := c.Total - c.Used
	if free <= 0 {
		return slope, 0, projectionFull
	}
	return slope, free / slope, projectionGrowing
}
