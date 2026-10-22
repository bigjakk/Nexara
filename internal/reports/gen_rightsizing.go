package reports

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// rightsizingMaxHours is the longest window the rightsizing report covers. Its
// figures come from raw guest readings, which are kept for 30 days: the
// rollups hold only averages and peaks, count the zeros written while a guest
// is stopped, and do not record whose view a memory figure is.
const rightsizingMaxHours = 30 * 24

// rightsizingQueryTimeout bounds the one grouped scan of the window's raw
// readings, which holds a pool connection for its whole run. It costs about
// 0.2 s per guest on a 30-day window, so this covers a few thousand guests.
const rightsizingQueryTimeout = 10 * time.Minute

// The flag thresholds. The report's notes state each one.
const (
	rsCPUOverPct   = 25.0      // p95 under this on 2+ vCPUs: over-allocated (VMs)
	rsCPUBoundPct  = 90.0      // p95 at or over this: CPU-bound
	rsMemOverPct   = 50.0      // the guest's own p95 under this: over-allocated
	rsMemTightPct  = 90.0      // the guest's own p95 at or over this: tight (containers)
	rsRarelyRunPct = 10.0      // running under this share of the observed time
	rsMinRunning   = time.Hour // less running time than this is never flagged
	// rsLateStart is how far after the cluster's first reading in the window a
	// guest's first one must fall to say "observed since": readings are taken
	// every collector pass, so every guest starts a little after the window.
	rsLateStart = time.Hour
)

type rightsizingStats struct {
	name, nodeName, vmType string
	vmid, vcpus            int
	allocated              float64 // bytes, the current allocation
	samples, running       int64
	firstSample            time.Time
	// CPU over running readings, as percent of the guest's own vCPUs.
	cpuAvg, cpuP95, cpuP99, cpuMax float64
	// The guest's own memory readings, and the host-view ones.
	guestN, hostN                   int64
	memAvgPct, memP95Pct, memMaxPct float64 // percent of each reading's allocation
	memP95, memMax                  float64 // bytes
	memSource                       memSource
}

func (s rightsizingStats) uptimePct() float64 {
	if s.samples == 0 {
		return 0
	}
	return float64(s.running) / float64(s.samples) * 100
}

// runningTime is how long the guest ran within the span it was observed.
func (s rightsizingStats) runningTime(now time.Time) time.Duration {
	if s.samples == 0 {
		return 0
	}
	return time.Duration(float64(now.Sub(s.firstSample)) * float64(s.running) / float64(s.samples))
}

func (s rightsizingStats) busyVCPUs() float64 { return s.cpuP95 / 100 * float64(s.vcpus) }

// ownCoveragePct is the share of the guest's running readings that carry its
// own memory figure. Readings with no source count against it: a VM's from
// before the source was recorded are running readings all the same.
func (s rightsizingStats) ownCoveragePct() float64 {
	if s.running == 0 {
		return 0
	}
	return float64(s.guestN) / float64(s.running) * 100
}

// ownFigureFull says the guest's own memory figure covers nine in ten of its
// running readings, enough to judge its sizing on.
func (s rightsizingStats) ownFigureFull() bool {
	return s.memSource == memSourceGuest && s.guestN*10 >= s.running*9
}

func (g *Generator) generateRightsizing(ctx context.Context, gc genCtx, data *ReportData) error {
	vms, err := g.queries.ListVMsByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return fmt.Errorf("list VMs: %w", err)
	}
	nodes, err := g.queries.ListNodesByCluster(ctx, gc.cluster.ID)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	nodeNames := make(map[uuid.UUID]string, len(nodes))
	for _, n := range nodes {
		nodeNames[n.ID] = n.Name
	}

	qctx, cancel := context.WithTimeout(ctx, rightsizingQueryTimeout)
	defer cancel()
	rows, err := g.queries.GetVMRightsizingStats(qctx, db.GetVMRightsizingStatsParams{ClusterID: gc.cluster.ID, Since: gc.since})
	if err != nil {
		return fmt.Errorf("rightsizing readings: %w", err)
	}
	byVM := make(map[uuid.UUID]db.GetVMRightsizingStatsRow, len(rows))
	for _, r := range rows {
		byVM[r.VmID] = r
	}

	stats := make([]rightsizingStats, 0, len(vms))
	for _, vm := range vms {
		if vm.Template {
			continue
		}
		s := rightsizingStats{name: vm.Name, nodeName: nodeNames[vm.NodeID], vmType: vm.Type, vmid: int(vm.Vmid), vcpus: int(vm.CpuCount), allocated: float64(vm.MemTotal)}
		if r, ok := byVM[vm.ID]; ok {
			s.samples, s.running, s.firstSample = r.Samples, r.RunningSamples, r.FirstSample
			s.cpuAvg, s.cpuP95, s.cpuP99, s.cpuMax = r.CpuAvg*100, r.CpuP95*100, r.CpuP99*100, r.CpuMax*100
			s.guestN, s.hostN = r.GuestSamples, r.HostSamples
			s.memAvgPct, s.memP95Pct, s.memMaxPct = r.GuestMemAvgRatio*100, r.GuestMemP95Ratio*100, r.GuestMemMaxRatio*100
			s.memP95, s.memMax = r.GuestMemP95, r.GuestMemMax
			s.memSource = memSourceOf(s.guestN, s.hostN, s.memAvgPct)
		}
		stats = append(stats, s)
	}
	buildRightsizing(gc, stats, data)
	return nil
}

// rsFlags is which flags a guest raises. Only a guest that ran for at least
// rsMinRunning in the window raises any.
type rsFlags struct {
	cpuOver, cpuBound, memOver, memTight, rarelyRuns bool
}

func rightsizingFlags(s rightsizingStats, now time.Time) rsFlags {
	if s.running == 0 || s.runningTime(now) < rsMinRunning {
		return rsFlags{}
	}
	var f rsFlags
	// A container's vCPUs are a limit, and with none set PVE reports the host's
	// count: neither is an allocation to give back.
	f.cpuOver = s.vmType == "qemu" && s.vcpus >= 2 && s.cpuP95 < rsCPUOverPct
	f.cpuBound = s.cpuP95 >= rsCPUBoundPct
	if s.ownFigureFull() {
		f.memOver = s.memP95Pct < rsMemOverPct
		// A VM's own figure counts a Linux guest's page cache as used, so a
		// high one is not pressure; a container's cgroup figure leaves it out.
		f.memTight = s.vmType == "lxc" && s.memP95Pct >= rsMemTightPct
	}
	f.rarelyRuns = s.uptimePct() < rsRarelyRunPct
	return f
}

func buildRightsizing(gc genCtx, stats []rightsizingStats, data *ReportData) {
	data.Subtitle = "How hard each guest works its vCPUs and memory, measured over the time it ran, against what it is allocated."
	sort.SliceStable(stats, func(i, j int) bool { return stats[i].vmid < stats[j].vmid })

	var clusterFirst time.Time
	for _, s := range stats {
		if s.samples > 0 && (clusterFirst.IsZero() || s.firstSample.Before(clusterFirst)) {
			clusterFirst = s.firstSample
		}
	}

	var qemu, lxc, measured, totalVCPUs int
	var full, partial, hostOnly, unrecorded int
	var busy float64
	var cpuOver, cpuBound, memOver, memTight, rarely []string
	for _, s := range stats {
		switch s.vmType {
		case "qemu":
			qemu++
		case "lxc":
			lxc++
		}
		totalVCPUs += s.vcpus
		if s.running > 0 {
			measured++
			busy += s.busyVCPUs()
		}
		switch {
		case s.ownFigureFull():
			full++
		case s.memSource == memSourceGuest || s.memSource == memSourceMixed:
			partial++
		case s.memSource == memSourceHost:
			hostOnly++
		default:
			unrecorded++
		}
		f := rightsizingFlags(s, gc.now)
		label := fmt.Sprintf("%s (%d)", s.name, s.vmid)
		if f.cpuOver {
			cpuOver = append(cpuOver, fmt.Sprintf("%s: %.0f %% of %d vCPUs", label, s.cpuP95, s.vcpus))
		}
		if f.cpuBound {
			cpuBound = append(cpuBound, fmt.Sprintf("%s: %.0f %% of %d vCPUs", label, s.cpuP95, s.vcpus))
		}
		if f.memOver {
			memOver = append(memOver, fmt.Sprintf("%s: %s of %s", label, formatBytes(s.memP95), formatBytes(s.allocated)))
		}
		if f.memTight {
			memTight = append(memTight, fmt.Sprintf("%s: %.0f %%", label, s.memP95Pct))
		}
		if f.rarelyRuns {
			rarely = append(rarely, fmt.Sprintf("%s: %.0f %%", label, s.uptimePct()))
		}
	}

	flagged := len(cpuOver) + len(cpuBound) + len(memOver) + len(memTight) + len(rarely)
	data.addMeta("Scope", plural(len(stats), "guest", "guests"))
	data.KPIs = []KPI{
		{Label: "Guests", Value: fmt.Sprint(len(stats)), Hero: true, Detail: fmt.Sprintf("%d VMs · %d containers · %d ran in the window", qemu, lxc, measured)},
		{Label: "vCPUs allocated", Value: fmt.Sprint(totalVCPUs), Detail: fmt.Sprintf("≈%.1f busy at each guest's p95", busy)},
		{Label: "Own memory figure", Value: fmt.Sprintf("%d of %d", full, len(stats)), Detail: fmt.Sprintf("%d partial · %d host view only · %d not recorded", partial, hostOnly, unrecorded)},
		{Label: "Flags", Value: fmt.Sprint(flagged), Tone: toneIf(len(cpuBound)+len(memTight) > 0, ToneWarn)},
	}

	if len(cpuBound) > 0 {
		data.addFinding(SevWarning, fmt.Sprintf("%s at %.0f %% CPU or more at p95:", pluralWere(len(cpuBound), "guest"), rsCPUBoundPct), nameList(cpuBound, 6)+". They may need more vCPUs, or a less busy node.", "CPU")
	}
	if len(memTight) > 0 {
		data.addFinding(SevWarning, fmt.Sprintf("%s at %.0f %% or more of their memory at p95:", pluralWere(len(memTight), "container"), rsMemTightPct), nameList(memTight, 6)+". A container's figure leaves page cache out, so this is real pressure.", "Memory")
	}
	if len(cpuOver) > 0 {
		data.addFinding(SevInfo, fmt.Sprintf("%s under %.0f %% of their vCPUs at p95:", pluralWere(len(cpuOver), "VM"), rsCPUOverPct), nameList(cpuOver, 6)+". Their vCPU count could come down.", "CPU")
	}
	if len(memOver) > 0 {
		data.addFinding(SevInfo, fmt.Sprintf("%s under %.0f %% of their memory at p95, by their own count:", pluralWere(len(memOver), "guest"), rsMemOverPct), nameList(memOver, 6)+". Their allocation could come down.", "Memory")
	}
	if len(rarely) > 0 {
		data.addFinding(SevInfo, fmt.Sprintf("%s running under %.0f %% of the time:", pluralWere(len(rarely), "guest"), rsRarelyRunPct), nameList(rarely, 6)+". They hold their allocation while they run.", "CPU")
	}

	pct := func(v float64) Cell { return num(fmt.Sprintf("%.1f", v)) }
	dash := mute("—")
	cpuRows := make([][]Cell, 0, len(stats))
	memRows := make([][]Cell, 0, len(stats))
	for _, s := range stats {
		lead := []Cell{idCell(s.vmid), strong(s.name), chips(Chip{Text: guestKind(s.vmType), Kind: ChipType}), text(s.nodeName)}
		if s.running == 0 {
			why := "not running in the window"
			if s.samples == 0 {
				why = "no readings in the window"
			}
			cpuRows = append(cpuRows, append(lead, num(s.vcpus), dash, dash, dash, dash, dash, textSub("0.0", why)))
		} else {
			up := pct(s.uptimePct())
			if !clusterFirst.IsZero() && s.firstSample.Sub(clusterFirst) > rsLateStart {
				up = textSub(fmt.Sprintf("%.1f", s.uptimePct()), "observed since "+s.firstSample.UTC().Format("2006-01-02"))
			}
			cpuRows = append(cpuRows, append(lead, num(s.vcpus), pct(s.cpuAvg), pct(s.cpuP95), pct(s.cpuP99), pct(s.cpuMax), num(fmt.Sprintf("%.1f", s.busyVCPUs())), up))
		}

		alloc := num(formatBytes(s.allocated))
		switch s.memSource {
		case memSourceGuest, memSourceMixed:
			memRows = append(memRows, append(lead, alloc, pct(s.memAvgPct), pct(s.memP95Pct), num(formatBytes(s.memP95)), num(formatBytes(s.memMax)), num(fmt.Sprintf("%.0f %%", s.ownCoveragePct()))))
		case memSourceHost:
			memRows = append(memRows, append(lead, alloc, dash, dash, dash, dash, textSub("0 %", "can't tell: host view only")))
		default:
			memRows = append(memRows, append(lead, alloc, dash, dash, dash, dash, mute("not recorded")))
		}
	}
	leadCols := []Column{nowrapCol("VMID"), nowrapCol("Guest"), col("Type"), col("Node")}
	data.addSection("CPU", "Each guest's CPU over the readings taken while it ran, as % of its own vCPUs, and the share of the time it ran.",
		wideTable("", &Table{Columns: append(append([]Column{}, leadCols...), numCol("vCPUs"), numCol("Avg %"), numCol("p95 %"), numCol("p99 %"), numCol("Max %"), numCol("Busy vCPUs at p95"), numCol("Up %")), Rows: cpuRows}, "No guests on this cluster."))
	data.addSection("Memory", "Each guest's memory as the guest itself reports it, against its allocation. A VM measured only from the host says nothing about use inside it.",
		wideTable("", &Table{Columns: append(append([]Column{}, leadCols...), numCol("Allocated"), numCol("Own avg %"), numCol("Own p95 %"), numCol("Own p95"), numCol("Own max"), numCol("Own figure")), Rows: memRows}, "No guests on this cluster."))

	data.Notes = []NoteGroup{{Title: "How this report was computed", Items: []string{
		"Figures come from raw guest readings, which are kept for 30 days, so the report covers at most the last 30 days.",
		"A reading counts as running when the collector saw the guest running: it records a memory source or any memory in use. A paused VM counts as running.",
		"CPU % is of the guest's own vCPUs, over its running readings only, so time spent stopped does not read as idle. p95 is the level 95 % of those readings stayed at or under; busy vCPUs at p95 is that level times the vCPU count.",
		"Memory is the guest's own figure: its balloon driver's statistics, where a Linux guest counts its page cache as used, or a container's cgroup usage without page cache. A VM without balloon statistics is measured from the host (host RSS), which says nothing about use inside it, so the report does not infer a figure.",
		"Own figure is the share of the guest's running readings that carry its own memory figure. A container's readings always were its own. A VM's are known to be its own only from the upgrade that started recording whose figure a reading is, so a window reaching back before that upgrade shows a lower Own figure.",
		"Up % is the share of the guest's readings in the window taken while it ran.",
		fmt.Sprintf("Flags: CPU over-allocated is p95 under %.0f %% on a VM with 2 or more vCPUs (a container's vCPUs are a limit, not an allocation). CPU-bound is p95 at %.0f %% or more. Memory over-allocated is the guest's own p95 under %.0f %%. Memory tight is a container's own p95 at %.0f %% or more; a VM's own figure includes page cache. Running under %.0f %% of the time is flagged too.", rsCPUOverPct, rsCPUBoundPct, rsMemOverPct, rsMemTightPct, rsRarelyRunPct),
		"A guest is flagged only when it ran for at least an hour in the window, and on memory only when its own figure covers nine in ten of its running readings.",
		"vCPUs and allocated memory are the current ones. Templates are excluded, and a guest Nexara re-added under a new id is measured from then on.",
	}}}
}
