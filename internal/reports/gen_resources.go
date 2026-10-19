package reports

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// --- Resource utilisation ---

type nodeUsage struct {
	name, status                       string
	days                               int
	cpuAvg, cpuMax, memPct, memMaxPct  float64
	memUsed, memTotal                  float64
	diskRead, diskWrite, netIn, netOut float64
	dailyCPU, dailyMem                 []float64
	dailyKeys                          []string
}

func (g *Generator) generateResourceUtilization(ctx context.Context, gc genCtx, data *ReportData) error {
	usage, err := g.fetchNodeUsage(ctx, gc)
	if err != nil {
		return err
	}
	buildResourceUtilization(gc, usage, data)
	return nil
}

// dailyUsage is one day of a node's or guest's hourly rollups, CPU in percent.
// The rollups hold PVE's 0–1 ratio (the metrics API scales it the same way);
// converting here, where the report layer loads them, keeps every consumer —
// the tables, findings, trend, forecast and digest — on one scale.
type dailyUsage struct {
	day                           time.Time
	cpuPct, cpuMaxPct             float64
	memUsed, memUsedMax, memTotal float64
}

func newDailyUsage(day time.Time, cpu, cpuMax, memUsed, memUsedMax, memTotal float64) dailyUsage {
	return dailyUsage{day: day, cpuPct: cpu * 100, cpuMaxPct: cpuMax * 100, memUsed: memUsed, memUsedMax: memUsedMax, memTotal: memTotal}
}

func (d dailyUsage) memPct() float64 {
	if d.memTotal > 0 {
		return d.memUsed / d.memTotal * 100
	}
	return 0
}

func (g *Generator) nodeDailyUsage(ctx context.Context, nodeID uuid.UUID, since time.Time) ([]dailyUsage, error) {
	rows, err := g.queries.GetNodeMetricsDailyAvg(ctx, db.GetNodeMetricsDailyAvgParams{NodeID: nodeID, Bucket: since})
	if err != nil {
		return nil, err
	}
	out := make([]dailyUsage, len(rows))
	for i, r := range rows {
		out[i] = newDailyUsage(r.Day, r.Cpu, r.CpuMax, r.MemUsed, r.MemUsedMax, r.MemTotal)
	}
	return out, nil
}

func (g *Generator) vmDailyUsage(ctx context.Context, vmID uuid.UUID, since time.Time) ([]dailyUsage, error) {
	rows, err := g.queries.GetVMMetricsDailyAvg(ctx, db.GetVMMetricsDailyAvgParams{VmID: vmID, Bucket: since})
	if err != nil {
		return nil, err
	}
	out := make([]dailyUsage, len(rows))
	for i, r := range rows {
		out[i] = newDailyUsage(r.Day, r.Cpu, r.CpuMax, r.MemUsed, r.MemUsedMax, r.MemTotal)
	}
	return out, nil
}

// fetchNodeUsage reads every node's daily averages and IO rates for the
// period. Shared with the cluster digest.
func (g *Generator) fetchNodeUsage(ctx context.Context, gc genCtx) ([]nodeUsage, error) {
	nodes, err := g.queries.ListNodesByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	usage := make([]nodeUsage, 0, len(nodes))
	for _, node := range nodes {
		u := nodeUsage{name: node.Name, status: node.Status}
		// A query error is a failed report, never a node that "reported no
		// metrics": that sentence is a finding, and it must be true.
		metrics, mErr := g.nodeDailyUsage(ctx, node.ID, gc.since)
		if mErr != nil {
			return nil, fmt.Errorf("daily metrics for %s: %w", node.Name, mErr)
		}
		ioRates, ioErr := g.queries.GetNodeIODailyRate(ctx, db.GetNodeIODailyRateParams{NodeID: node.ID, Time: gc.since})
		if ioErr != nil {
			return nil, fmt.Errorf("IO rates for %s: %w", node.Name, ioErr)
		}
		var cpuSum, memUsedSum, memUsedMax, memTotalSum float64
		for _, m := range metrics {
			cpuSum += m.cpuPct
			u.cpuMax = maxf(u.cpuMax, m.cpuMaxPct)
			memUsedSum += m.memUsed
			memUsedMax = maxf(memUsedMax, m.memUsedMax)
			memTotalSum += m.memTotal
			u.dailyKeys = append(u.dailyKeys, m.day.Format("2006-01-02"))
			u.dailyCPU = append(u.dailyCPU, m.cpuPct)
			u.dailyMem = append(u.dailyMem, m.memPct())
		}
		if n := float64(len(metrics)); n > 0 {
			u.days = len(metrics)
			u.cpuAvg = cpuSum / n
			u.memUsed = memUsedSum / n
			u.memTotal = memTotalSum / n
			if u.memTotal > 0 {
				u.memPct = u.memUsed / u.memTotal * 100
				u.memMaxPct = memUsedMax / u.memTotal * 100
			}
		}
		if n := float64(len(ioRates)); n > 0 {
			for _, io := range ioRates {
				u.diskRead += io.DiskReadRate / n
				u.diskWrite += io.DiskWriteRate / n
				u.netIn += io.NetInRate / n
				u.netOut += io.NetOutRate / n
			}
		}
		usage = append(usage, u)
	}
	return usage, nil
}

func buildResourceUtilization(gc genCtx, usage []nodeUsage, data *ReportData) {
	data.Subtitle = "How much of the cluster's CPU, memory, disk and network the period used, per node and per day."
	data.addMeta("Scope", plural(len(usage), "node", "nodes"))

	// Cluster figures are the mean over nodes that reported.
	var reported, online int
	var cpuSum, cpuPeak, memSum, memPeak float64
	for _, u := range usage {
		if u.status == "online" {
			online++
		}
		if u.days == 0 {
			continue
		}
		reported++
		cpuSum += u.cpuAvg
		cpuPeak = maxf(cpuPeak, u.cpuMax)
		memSum += u.memPct
		memPeak = maxf(memPeak, u.memMaxPct)
	}
	cpuAvg, memAvg := 0.0, 0.0
	if reported > 0 {
		cpuAvg = cpuSum / float64(reported)
		memAvg = memSum / float64(reported)
	}
	data.KPIs = []KPI{
		{Label: "Cluster CPU, period average", Value: fmt.Sprintf("%.1f", cpuAvg), Unit: "%", Hero: true, Detail: fmt.Sprintf("peak %.1f %% on any node", cpuPeak), Tone: usageTone(cpuAvg)},
		{Label: "Memory, period average", Value: fmt.Sprintf("%.1f", memAvg), Unit: "%", Detail: fmt.Sprintf("peak %.1f %%", memPeak), Tone: usageTone(memAvg)},
		{Label: "Nodes online", Value: fmt.Sprintf("%d/%d", online, len(usage)), Tone: toneIf(online < len(usage), ToneCrit)},
		{Label: "Nodes with metrics", Value: fmt.Sprint(reported), Detail: fmt.Sprintf("of %d, over %s", len(usage), periodDays(gc.since, gc.now))},
	}

	// Findings: sustained pressure, a node carrying far more than the rest,
	// and nodes with no data at all.
	for _, u := range usage {
		if u.status != "online" {
			data.addFinding(SevCritical, fmt.Sprintf("%s is %s.", u.name, u.status), "", "Nodes")
		}
		if u.days == 0 {
			data.addFinding(SevInfo, fmt.Sprintf("%s reported no metrics for the period.", u.name), "", "Nodes")
			continue
		}
		if u.memPct >= 90 {
			data.addFinding(SevSerious, fmt.Sprintf("%s averaged %.0f %% memory", u.name, u.memPct), fmt.Sprintf("over the period (peak %.0f %%); it has no headroom for a failover.", u.memMaxPct), "Nodes")
		} else if u.memPct >= 80 {
			data.addFinding(SevWarning, fmt.Sprintf("%s averaged %.0f %% memory", u.name, u.memPct), fmt.Sprintf("over the period (peak %.0f %%).", u.memMaxPct), "Nodes")
		}
		if u.cpuAvg >= 80 {
			data.addFinding(SevSerious, fmt.Sprintf("%s averaged %.0f %% CPU", u.name, u.cpuAvg), fmt.Sprintf("over the period (peak %.0f %%).", u.cpuMax), "Nodes")
		} else if u.cpuAvg >= 60 {
			data.addFinding(SevWarning, fmt.Sprintf("%s averaged %.0f %% CPU", u.name, u.cpuAvg), fmt.Sprintf("over the period (peak %.0f %%).", u.cpuMax), "Nodes")
		}
		if reported > 1 && cpuAvg > 5 && u.cpuAvg > 2*cpuAvg {
			data.addFinding(SevInfo, fmt.Sprintf("%s carries twice the cluster's average CPU load", u.name), fmt.Sprintf("(%.0f %% against %.0f %%); worth a look at placement.", u.cpuAvg, cpuAvg), "Nodes")
		}
	}

	// Per-node table.
	rows := make([][]Cell, 0, len(usage))
	for _, u := range usage {
		if u.days == 0 {
			rows = append(rows, []Cell{strong(u.name), statusPill(u.status), mute("no data"), mute(""), mute(""), mute(""), mute(""), mute(""), mute(""), mute(""), mute(""), mute("")})
			continue
		}
		rows = append(rows, []Cell{strong(u.name), statusPill(u.status),
			num(fmt.Sprintf("%.1f", u.cpuAvg)), num(fmt.Sprintf("%.1f", u.cpuMax)),
			num(fmt.Sprintf("%.1f", u.memPct)), num(fmt.Sprintf("%.1f", u.memMaxPct)),
			num(formatBytes(u.memUsed)), num(formatBytes(u.memTotal)),
			num(formatBytesRate(u.diskRead)), num(formatBytesRate(u.diskWrite)), num(formatBytesRate(u.netIn)), num(formatBytesRate(u.netOut))})
	}
	nodeTable := &Table{Columns: []Column{nowrapCol("Node"), col("Status"), numCol("CPU avg %"), numCol("CPU peak %"), numCol("Mem avg %"), numCol("Mem peak %"), numCol("Mem used"), numCol("Mem total"), numCol("Disk read/s"), numCol("Disk write/s"), numCol("Net in/s"), numCol("Net out/s")}, Rows: rows}

	// Cluster daily trend: mean across nodes per day, as lines and as a table.
	dayIdx := map[string]int{}
	var days []string
	for _, u := range usage {
		for _, k := range u.dailyKeys {
			if _, ok := dayIdx[k]; !ok {
				dayIdx[k] = len(days)
				days = append(days, k)
			}
		}
	}
	sort.Strings(days)
	for i, d := range days {
		dayIdx[d] = i
	}
	cpuByDay := make([]float64, len(days))
	memByDay := make([]float64, len(days))
	countByDay := make([]float64, len(days))
	for _, u := range usage {
		for i, k := range u.dailyKeys {
			d := dayIdx[k]
			cpuByDay[d] += u.dailyCPU[i]
			memByDay[d] += u.dailyMem[i]
			countByDay[d]++
		}
	}
	trendRows := make([][]Cell, 0, len(days))
	labels := make([]string, len(days))
	for i, d := range days {
		if countByDay[i] > 0 {
			cpuByDay[i] /= countByDay[i]
			memByDay[i] /= countByDay[i]
		}
		if t, err := time.Parse("2006-01-02", d); err == nil {
			labels[i] = t.Format("Jan 2")
		} else {
			labels[i] = d
		}
		trendRows = append(trendRows, []Cell{text(d), num(fmt.Sprintf("%.1f", cpuByDay[i])), num(fmt.Sprintf("%.1f", memByDay[i])), num(int(countByDay[i]))})
	}
	blocks := []Block{}
	if len(days) >= 2 {
		blocks = append(blocks, chartBlock("Cluster daily trend", "Mean of the nodes that reported each day.", &Chart{Kind: ChartLines, Categories: labels, Format: FormatPercent, Legend: true, Max: 100,
			Series: []Series{{Name: "CPU %", Tone: ToneAccent, Values: cpuByDay}, {Name: "Memory %", Tone: ToneWarn, Values: memByDay}}, AriaLabel: "Cluster CPU and memory percent per day"}))
	}
	blocks = append(blocks, tableBlock("Daily figures", "", &Table{Columns: []Column{col("Day"), numCol("CPU avg %"), numCol("Mem avg %"), numCol("Nodes")}, Rows: trendRows}, "No daily metrics in the period."))
	data.addSection("Cluster trend", "", blocks...)
	data.addSection("Nodes", "Period averages and peaks per node. Peaks are the highest hourly maximum in the period.", wideTable("", nodeTable, "No nodes in this cluster."))
	data.Notes = []NoteGroup{{Title: "How this report was computed", Items: []string{
		"Averages are over the hourly metric rollups (node_metrics_1h) within the period; peaks are the largest hourly maximum.",
		"Disk and network rates are derived from counter deltas between samples, averaged per day and then over the period.",
		"Memory percentages use each day's average total, so a node whose memory was resized mid-period is averaged rather than skewed.",
	}}}
}

func statusPill(status string) Cell {
	switch status {
	case "online", "running":
		return pill(status, ToneGood)
	case "offline", "stopped", "unknown":
		return pill(status, ToneCrit)
	}
	return pill(status, ToneNeutral)
}

func usageTone(pct float64) string {
	switch {
	case pct >= 90:
		return ToneCrit
	case pct >= 75:
		return ToneWarn
	}
	return ""
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// --- Capacity forecast ---

func (g *Generator) generateCapacityForecast(ctx context.Context, gc genCtx, data *ReportData) error {
	nodes, err := g.queries.ListNodesByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	type nodeSeries struct {
		name    string
		times   []time.Time
		cpu     []float64
		mem     []float64
		samples int
	}
	series := make([]nodeSeries, 0, len(nodes))
	for _, node := range nodes {
		metrics, mErr := g.nodeDailyUsage(ctx, node.ID, gc.since)
		if mErr != nil {
			return fmt.Errorf("daily metrics for %s: %w", node.Name, mErr)
		}
		ns := nodeSeries{name: node.Name, samples: len(metrics)}
		for _, m := range metrics {
			ns.times = append(ns.times, m.day)
			ns.cpu = append(ns.cpu, m.cpuPct)
			ns.mem = append(ns.mem, m.memPct())
		}
		series = append(series, ns)
	}
	pools, err := g.queries.ListStoragePoolsByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return fmt.Errorf("list storage pools: %w", err)
	}
	nodeNames := map[uuid.UUID]string{}
	for _, n := range nodes {
		nodeNames[n.ID] = n.Name
	}

	data.Subtitle = "Where the period's trend takes CPU and memory per node, and how full each storage pool is today."
	data.addMeta("Scope", fmt.Sprintf("%s · %s", plural(len(nodes), "node", "nodes"), plural(len(pools), "storage pool reading", "storage pool readings")))

	rows := [][]Cell{}
	var soonest struct {
		node, metric string
		days         float64
	}
	soonest.days = -1
	forecastRow := func(name, metric string, times []time.Time, values []float64) {
		if len(values) < 2 {
			rows = append(rows, []Cell{strong(name), text(metric), mute("—"), mute("not enough history"), mute(""), mute("")})
			return
		}
		current := values[len(values)-1]
		start := times[0]
		xs := make([]float64, len(times))
		for i, t := range times {
			xs[i] = t.Sub(start).Hours() / 24
		}
		slope, _, ok := LinearRegression(xs, values)
		row := []Cell{strong(name), text(metric), num(fmt.Sprintf("%.1f%%", current)), mute("—"), mute("—"), mute("—")}
		if ok {
			row[3] = num(fmt.Sprintf("%+.2f%%", slope))
			days, date := ForecastMetric(times, values, 100)
			if days != nil && date != nil {
				row[4] = num(fmt.Sprintf("%.0f", *days))
				row[5] = text(date.Format("2006-01-02"))
				if soonest.days < 0 || *days < soonest.days {
					soonest.node, soonest.metric, soonest.days = name, metric, *days
				}
				if *days < 30 {
					data.addFinding(SevCritical, fmt.Sprintf("%s %s reaches 100 %% in about %.0f days", name, metric, *days), fmt.Sprintf("(%s) at the period's trend of %+.2f %% a day.", date.Format("2006-01-02"), slope), "Node forecast")
				} else if *days < 90 {
					data.addFinding(SevWarning, fmt.Sprintf("%s %s reaches 100 %% in about %.0f days", name, metric, *days), fmt.Sprintf("(%s) at the period's trend.", date.Format("2006-01-02")), "Node forecast")
				}
			} else {
				row[4] = mute("not projected")
				row[5] = mute("no upward trend")
			}
		}
		rows = append(rows, row)
	}
	for _, ns := range series {
		forecastRow(ns.name, "CPU %", ns.times, ns.cpu)
		forecastRow(ns.name, "Memory %", ns.times, ns.mem)
	}

	// Storage pools: today's fill. A shared pool is one pool however many nodes
	// report it, counted once at the highest reading; a node's own pool (local,
	// local-lvm) exists once per node, so it is keyed on the node as well.
	type poolKey struct {
		storage string
		node    uuid.UUID
	}
	type poolFill struct {
		pool                db.StoragePool
		pct                 float64
		name, label, detail string
	}
	var fills []poolFill
	at := map[poolKey]int{}
	for _, p := range pools {
		if p.Total <= 0 || !p.Enabled {
			continue
		}
		k := poolKey{storage: p.Storage}
		if !p.Shared {
			k.node = p.NodeID
		}
		if i, ok := at[k]; !ok {
			at[k] = len(fills)
			fills = append(fills, poolFill{pool: p})
		} else if p.Used > fills[i].pool.Used {
			fills[i].pool = p
		}
	}
	for i := range fills {
		f := &fills[i]
		f.pct = float64(f.pool.Used) / float64(f.pool.Total) * 100
		f.name, f.label, f.detail = f.pool.Storage, f.pool.Storage, f.pool.Type
		if f.pool.Shared {
			f.detail += " · shared"
		} else if n, ok := nodeNames[f.pool.NodeID]; ok {
			f.label += " on " + n
			f.detail += " · " + n
		}
	}
	sort.SliceStable(fills, func(i, j int) bool {
		if fills[i].pct != fills[j].pct {
			return fills[i].pct > fills[j].pct
		}
		return fills[i].label < fills[j].label
	})
	meters := make([]Meter, 0, len(fills))
	var fullestPool string
	var fullestPct float64
	for _, f := range fills {
		meters = append(meters, Meter{Name: f.name, Detail: f.detail, Used: float64(f.pool.Used), Total: float64(f.pool.Total), Tone: capacityTone(f.pct),
			UsedText: fmt.Sprintf("%s of %s", formatBytes(float64(f.pool.Used)), formatBytes(float64(f.pool.Total))), Projection: "Today's fill; pool history is not yet sampled"})
		if f.pct > fullestPct {
			fullestPct, fullestPool = f.pct, f.label
		}
		if f.pct >= 90 {
			data.addFinding(SevCritical, fmt.Sprintf("Storage pool %s is %.0f %% full.", f.label, f.pct), "", "Storage pools")
		} else if f.pct >= 80 {
			data.addFinding(SevWarning, fmt.Sprintf("Storage pool %s is %.0f %% full.", f.label, f.pct), "", "Storage pools")
		}
	}

	kpis := []KPI{}
	if soonest.days >= 0 {
		kpis = append(kpis, KPI{Label: "Soonest exhaustion", Value: fmt.Sprintf("%.0f", soonest.days), Unit: " days", Hero: true, Detail: soonest.node + " " + soonest.metric, Tone: toneIf(soonest.days < 90, ToneWarn)})
	} else {
		kpis = append(kpis, KPI{Label: "Soonest exhaustion", Value: "none", Hero: true, Detail: "no node trends upward over the period", Tone: ToneGood})
	}
	kpis = append(kpis, KPI{Label: "Nodes forecast", Value: fmt.Sprint(len(series)), Detail: periodDays(gc.since, gc.now) + " of daily samples"})
	if fullestPool != "" {
		kpis = append(kpis, KPI{Label: "Fullest storage pool", Value: fmt.Sprintf("%.0f", fullestPct), Unit: "%", Detail: fullestPool, Tone: capacityTone(fullestPct)})
	}
	data.KPIs = kpis

	data.addSection("Node forecast", "A least-squares line through each node's daily average, projected to 100 %. Days to exhaust is blank when the trend is flat or falling.",
		wideTable("", &Table{Columns: []Column{nowrapCol("Node"), col("Metric"), numCol("Current"), numCol("Trend / day"), numCol("Days to exhaust"), col("Exhaustion date")}, Rows: rows}, "No nodes with two or more days of metrics."))
	data.addSection("Storage pools", "", metersBlock("", "", meters, "No storage pools report a size."))
	data.Notes = []NoteGroup{{Title: "How this report was computed", Items: []string{
		"Node trends are fitted to the period's daily averages; a fit needs at least two days.",
		"Storage pool figures are the collector's latest reading; growth is not projected until pool history is sampled.",
		"Shared pools are counted once, at the highest reading any node reports; a node's own pools, such as local, are listed per node.",
	}}}
	return nil
}

// --- VM resource usage ---

type vmReportStats struct {
	name, nodeName, vmType, status string
	vmid                           int
	cpuAvg, cpuMax                 float64
	memUsed, memTotal              float64
	memPct                         float64
	memSource                      memSource
	diskRead, diskWrite            float64
	netIn, netOut                  float64
	cpuCount                       int
	diskTotal                      int64
	hasMetrics                     bool
}

// memSource is whose view a guest's memory figure is (vm_metrics.mem_guest).
type memSource int

const (
	memSourceUnknown memSource = iota // no reading in the period records one
	memSourceGuest                    // the guest's own: balloon statistics, or a container's cgroup
	memSourceHost                     // the host's view of a VM without balloon statistics
	memSourceMixed                    // a real share of both
)

func (m memSource) String() string {
	switch m {
	case memSourceGuest:
		return "guest"
	case memSourceHost:
		return "host RSS"
	case memSourceMixed:
		return "guest and host RSS"
	}
	return "not recorded"
}

// memSourceOf judges a figure from its readings' sources. A few host readings
// among a VM's own are normal — PVE has no balloon statistics until the
// guest's driver loads after a boot — so nine in ten decide it. ownPct is the
// average of the guest's own readings; past the allocation it is not one.
func memSourceOf(guest, host int64, ownPct float64) memSource {
	total := guest + host
	switch {
	case total == 0:
		return memSourceUnknown
	case guest*10 >= total*9 && ownPct <= 100:
		return memSourceGuest
	case host*10 >= total*9:
		return memSourceHost
	}
	return memSourceMixed
}

func (g *Generator) generateVMResourceUsage(ctx context.Context, gc genCtx, data *ReportData) error {
	vms, err := g.queries.ListVMsByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return fmt.Errorf("list VMs: %w", err)
	}
	nodes, err := g.queries.ListNodesByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	nodeNames := make(map[uuid.UUID]string)
	for _, n := range nodes {
		nodeNames[n.ID] = n.Name
	}

	stats := make([]vmReportStats, 0, len(vms))
	for _, vm := range vms {
		if vm.Template {
			continue
		}
		s := vmReportStats{name: vm.Name, nodeName: nodeNames[vm.NodeID], vmType: vm.Type, status: vm.Status, vmid: int(vm.Vmid), cpuCount: int(vm.CpuCount), diskTotal: vm.DiskTotal}
		metrics, mErr := g.vmDailyUsage(ctx, vm.ID, gc.since)
		if mErr != nil {
			return fmt.Errorf("daily metrics for guest %d: %w", vm.Vmid, mErr)
		}
		if len(metrics) > 0 {
			s.hasMetrics = true
			var cpuSum, memUsedSum, memTotalSum float64
			for _, m := range metrics {
				cpuSum += m.cpuPct
				s.cpuMax = maxf(s.cpuMax, m.cpuMaxPct)
				memUsedSum += m.memUsed
				memTotalSum += m.memTotal
			}
			n := float64(len(metrics))
			s.cpuAvg = cpuSum / n
			s.memUsed = memUsedSum / n
			s.memTotal = memTotalSum / n
			if s.memTotal > 0 {
				s.memPct = s.memUsed / s.memTotal * 100
			}
		}
		ioRates, ioErr := g.queries.GetVMIODailyRate(ctx, db.GetVMIODailyRateParams{VmID: vm.ID, Time: gc.since})
		if ioErr != nil {
			return fmt.Errorf("IO rates for guest %d: %w", vm.Vmid, ioErr)
		}
		var guestN, hostN int64
		var guestUsed, guestTotal float64
		if len(ioRates) > 0 {
			n := float64(len(ioRates))
			for _, io := range ioRates {
				s.diskRead += io.DiskReadRate / n
				s.diskWrite += io.DiskWriteRate / n
				s.netIn += io.NetInRate / n
				s.netOut += io.NetOutRate / n
				guestN, hostN = guestN+io.GuestSamples, hostN+io.HostSamples
				guestUsed, guestTotal = guestUsed+io.GuestMemUsed, guestTotal+io.GuestMemTotal
			}
		}
		if s.hasMetrics {
			// The guest's own figure is averaged over its own readings alone, in
			// the raw window their source is kept for: the period average can
			// blend in host readings from before its driver reported.
			ownPct := 0.0
			if guestTotal > 0 {
				ownPct = guestUsed / guestTotal * 100
			}
			if s.memSource = memSourceOf(guestN, hostN, ownPct); s.memSource == memSourceGuest {
				s.memUsed, s.memTotal, s.memPct = guestUsed/float64(guestN), guestTotal/float64(guestN), ownPct
			}
		}
		stats = append(stats, s)
	}
	buildVMResourceUsage(gc, stats, data)
	return nil
}

func buildVMResourceUsage(gc genCtx, stats []vmReportStats, data *ReportData) {
	topN := gc.params.TopN
	if topN <= 0 {
		topN = 10
	}
	data.Subtitle = "Which guests used the cluster's CPU, memory, network and disk over the period, and which sat idle."

	var running, stopped, qemu, lxc, idle int
	var idleNames []string
	for _, s := range stats {
		switch s.status {
		case "running":
			running++
		case "stopped":
			stopped++
		}
		switch s.vmType {
		case "qemu":
			qemu++
		case "lxc":
			lxc++
		}
		if s.status == "running" && s.hasMetrics && s.cpuAvg < 2 && s.netIn+s.netOut < 10<<10 && s.diskRead+s.diskWrite < 50<<10 {
			idle++
			idleNames = append(idleNames, fmt.Sprintf("%s (%d)", s.name, s.vmid))
		}
	}
	data.addMeta("Scope", fmt.Sprintf("%s · top %d per table", plural(len(stats), "guest", "guests"), topN))
	data.KPIs = []KPI{
		{Label: "Guests", Value: fmt.Sprint(len(stats)), Hero: true, Detail: fmt.Sprintf("%d VMs · %d containers", qemu, lxc)},
		{Label: "Running", Value: fmt.Sprint(running), Tone: ToneGood},
		{Label: "Stopped", Value: fmt.Sprint(stopped)},
		{Label: "Idle while running", Value: fmt.Sprint(idle), Detail: "< 2 % CPU and little I/O all period", Tone: toneIf(idle > 0, ToneWarn)},
	}
	if idle > 0 {
		data.addFinding(SevInfo, fmt.Sprintf("%s idle for the whole period while running:", pluralWere(idle, "guest")), nameList(idleNames, 6)+". They hold memory and vCPUs that could be reclaimed.", "All guests")
	}
	for _, s := range stats {
		// Only the guest's own figure says anything about pressure inside it:
		// the host's view of a VM sits near 100 % once its RAM has been touched.
		if s.hasMetrics && s.memSource == memSourceGuest && s.memPct >= 95 && s.status == "running" {
			// A container's figure leaves page cache out; a VM's is total minus
			// free, and only a Linux guest's free leaves its page cache out too.
			text := "by its own count; it may be swapping or about to."
			if s.vmType == "qemu" {
				text = "by its own count, which for a Linux guest includes page cache; it may be swapping or about to, so check inside the guest."
			}
			data.addFinding(SevWarning, fmt.Sprintf("%s (%d) averaged %.0f %% of its memory", s.name, s.vmid, s.memPct), text, "Top memory consumers")
		}
		if s.hasMetrics && s.cpuAvg >= 90 && s.status == "running" {
			data.addFinding(SevWarning, fmt.Sprintf("%s (%d) averaged %.0f %% CPU", s.name, s.vmid, s.cpuAvg), "over the period; it is CPU-bound.", "Top CPU consumers")
		}
	}

	top := func(from []vmReportStats, key func(vmReportStats) float64) []vmReportStats {
		out := make([]vmReportStats, len(from))
		copy(out, from)
		sort.SliceStable(out, func(i, j int) bool { return key(out[i]) > key(out[j]) })
		if len(out) > topN {
			out = out[:topN]
		}
		var kept []vmReportStats
		for _, s := range out {
			if key(s) > 0 {
				kept = append(kept, s)
			}
		}
		return kept
	}
	barChart := func(title string, items []vmReportStats, key func(vmReportStats) float64, format string) Block {
		cats := make([]string, 0, len(items))
		vals := make([]float64, 0, len(items))
		for _, s := range items {
			cats = append(cats, s.name)
			vals = append(vals, key(s))
		}
		return chartBlock(title, "", &Chart{Kind: ChartBars, Categories: cats, Format: format, Series: []Series{{Name: title, Tone: ToneAccent, Values: vals}}, AriaLabel: title})
	}
	cpuKey := func(s vmReportStats) float64 { return s.cpuAvg }
	memKey := func(s vmReportStats) float64 { return s.memPct }
	netKey := func(s vmReportStats) float64 { return s.netIn + s.netOut }
	diskKey := func(s vmReportStats) float64 { return s.diskRead + s.diskWrite }

	// Memory is ranked only among the guests' own figures; the host's view of
	// a VM is listed apart.
	var ownMem, otherMem []vmReportStats
	for _, s := range stats {
		if s.memSource == memSourceGuest {
			ownMem = append(ownMem, s)
		} else {
			otherMem = append(otherMem, s)
		}
	}
	topCPU, topMem, topOtherMem, topNet, topDisk := top(stats, cpuKey), top(ownMem, memKey), top(otherMem, memKey), top(stats, netKey), top(stats, diskKey)
	cpuRows := make([][]Cell, 0, len(topCPU))
	for _, s := range topCPU {
		cpuRows = append(cpuRows, []Cell{strong(s.name), idCell(s.vmid), text(s.nodeName), num(s.cpuCount), num(fmt.Sprintf("%.1f", s.cpuAvg)), num(fmt.Sprintf("%.1f", s.cpuMax))})
	}
	memRow := func(s vmReportStats) []Cell {
		return []Cell{strong(s.name), idCell(s.vmid), text(s.nodeName), num(fmt.Sprintf("%.1f", s.memPct)), num(formatBytes(s.memUsed)), num(formatBytes(s.memTotal))}
	}
	memRows := make([][]Cell, 0, len(topMem))
	for _, s := range topMem {
		memRows = append(memRows, memRow(s))
	}
	otherMemRows := make([][]Cell, 0, len(topOtherMem))
	for _, s := range topOtherMem {
		otherMemRows = append(otherMemRows, append(memRow(s), text(s.memSource.String())))
	}
	memColumns := func(extra ...Column) []Column {
		return append([]Column{nowrapCol("Guest"), nowrapCol("VMID"), col("Node"), numCol("Mem avg %"), numCol("Mem used"), numCol("Mem total")}, extra...)
	}
	memBlocks := []Block{
		barChart("Memory avg %", topMem, memKey, FormatPercent),
		tableBlock("", "", &Table{Columns: memColumns(), Rows: memRows}, "No guest reported its own memory use in the period."),
	}
	if len(otherMemRows) > 0 {
		memBlocks = append(memBlocks, tableBlock("Not reported by the guest",
			"Without balloon statistics from its driver, a VM's figure is the memory the host holds for it (host RSS): it nears or passes 100 % once the guest has touched its allocation, and says nothing about pressure inside it.",
			&Table{Columns: memColumns(col("Source")), Rows: otherMemRows}, ""))
	}
	netRows := make([][]Cell, 0, len(topNet))
	for _, s := range topNet {
		netRows = append(netRows, []Cell{strong(s.name), idCell(s.vmid), text(s.nodeName), num(formatBytesRate(s.netIn)), num(formatBytesRate(s.netOut)), num(formatBytesRate(s.netIn + s.netOut))})
	}
	diskRows := make([][]Cell, 0, len(topDisk))
	for _, s := range topDisk {
		diskRows = append(diskRows, []Cell{strong(s.name), idCell(s.vmid), text(s.nodeName), num(formatBytesRate(s.diskRead)), num(formatBytesRate(s.diskWrite)), num(formatBytesRate(s.diskRead + s.diskWrite))})
	}
	data.addSection("Top CPU consumers", fmt.Sprintf("Period average CPU %% per guest, top %d.", topN),
		barChart("CPU avg %", topCPU, cpuKey, FormatPercent),
		tableBlock("", "", &Table{Columns: []Column{nowrapCol("Guest"), nowrapCol("VMID"), col("Node"), numCol("vCPUs"), numCol("CPU avg %"), numCol("CPU peak %")}, Rows: cpuRows}, "No guest metrics in the period."))
	data.addSection("Top memory consumers", fmt.Sprintf("Average memory %% of each guest's own allocation as the guest itself reports it (its balloon driver, or a container's cgroup), top %d.", topN), memBlocks...)
	data.addSection("Top network consumers", fmt.Sprintf("Average in plus out, top %d.", topN),
		barChart("Network in+out", topNet, netKey, FormatRate),
		tableBlock("", "", &Table{Columns: []Column{nowrapCol("Guest"), nowrapCol("VMID"), col("Node"), numCol("In/s"), numCol("Out/s"), numCol("Total/s")}, Rows: netRows}, "No guest I/O in the period."))
	data.addSection("Top disk I/O consumers", fmt.Sprintf("Average read plus write, top %d.", topN),
		barChart("Disk read+write", topDisk, diskKey, FormatRate),
		tableBlock("", "", &Table{Columns: []Column{nowrapCol("Guest"), nowrapCol("VMID"), col("Node"), numCol("Read/s"), numCol("Write/s"), numCol("Total/s")}, Rows: diskRows}, "No guest I/O in the period."))

	all := make([][]Cell, 0, len(stats))
	sorted := make([]vmReportStats, len(stats))
	copy(sorted, stats)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].vmid < sorted[j].vmid })
	for _, s := range sorted {
		kind := "VM"
		if s.vmType == "lxc" {
			kind = "CT"
		}
		cpu, mem, memSrc := mute("—"), mute("—"), mute("—")
		if s.hasMetrics {
			cpu = num(fmt.Sprintf("%.1f", s.cpuAvg))
			mem = num(fmt.Sprintf("%.1f", s.memPct))
			memSrc = text(s.memSource.String())
		}
		all = append(all, []Cell{idCell(s.vmid), strong(s.name), chips(Chip{Text: kind, Kind: ChipType}), text(s.nodeName), statusPill(s.status), num(s.cpuCount), cpu, mem, memSrc, num(formatBytes(float64(s.diskTotal))), num(formatBytesRate(s.netIn)), num(formatBytesRate(s.netOut))})
	}
	data.addSection("All guests", "", wideTable("", &Table{Columns: []Column{nowrapCol("VMID"), nowrapCol("Guest"), col("Type"), col("Node"), col("Status"), numCol("vCPUs"), numCol("CPU avg %"), numCol("Mem avg %"), col("Mem source"), numCol("Disk alloc"), numCol("Net in/s"), numCol("Net out/s")}, Rows: all}, "No guests on this cluster."))
	data.Notes = []NoteGroup{{Title: "How this report was computed", Items: []string{
		"Averages are over the hourly guest metric rollups within the period, except a guest's own memory figure (below); a guest with no rollups shows a dash.",
		"CPU % is of the guest's own vCPUs; memory % is of its own allocation, not of the node.",
		"Memory is the guest's own figure when its balloon driver sends statistics (total minus free, where a Linux guest counts its page cache as used) and, for a container, its cgroup's usage without page cache. Otherwise it is the host's view (host RSS), and no memory warning is raised from it.",
		"A figure counts as the guest's own when nine in ten of its readings that record a source are, and is then averaged over those readings alone. Raw readings are kept for 30 days, so on a longer period it covers the last 30.",
		"A guest counts as idle when it was running with under 2 % average CPU, under 10 KiB/s of network and under 50 KiB/s of disk I/O for the whole period.",
		"Templates are excluded.",
	}}}
}

func pluralWere(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s was", noun)
	}
	return fmt.Sprintf("%d %ss were", n, noun)
}
