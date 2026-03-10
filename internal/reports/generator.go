package reports

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Generator builds report data from database queries.
type Generator struct {
	queries *db.Queries
	logger  *slog.Logger
}

// NewGenerator creates a new report generator.
func NewGenerator(queries *db.Queries, logger *slog.Logger) *Generator {
	return &Generator{queries: queries, logger: logger}
}

// Generate produces a report of the given type for a cluster.
func (g *Generator) Generate(ctx context.Context, reportType string, clusterID uuid.UUID, timeRangeHours int) (*ReportData, error) {
	cluster, err := g.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("get cluster: %w", err)
	}

	now := time.Now().UTC()
	since := now.Add(-time.Duration(timeRangeHours) * time.Hour)

	data := &ReportData{
		ClusterName: cluster.Name,
		ClusterID:   clusterID.String(),
		ReportType:  reportType,
		GeneratedAt: now.Format(time.RFC3339),
		TimeRange: TimeRange{
			StartTime: since.Format(time.RFC3339),
			EndTime:   now.Format(time.RFC3339),
			Hours:     timeRangeHours,
		},
	}

	switch ReportType(reportType) {
	case TypeResourceUtilization:
		data.Title = fmt.Sprintf("Resource Utilization Report - %s", cluster.Name)
		return g.generateResourceUtilization(ctx, data, clusterID, since)
	case TypeCapacityForecast:
		data.Title = fmt.Sprintf("Capacity Forecast Report - %s", cluster.Name)
		return g.generateCapacityForecast(ctx, data, clusterID, since)
	case TypeBackupCompliance:
		data.Title = fmt.Sprintf("Backup Compliance Report - %s", cluster.Name)
		return g.generateBackupCompliance(ctx, data, clusterID)
	case TypePatchStatus:
		data.Title = fmt.Sprintf("Patch Status Report - %s", cluster.Name)
		return g.generatePatchStatus(ctx, data, clusterID)
	case TypeUptimeSummary:
		data.Title = fmt.Sprintf("Uptime Summary Report - %s", cluster.Name)
		return g.generateUptimeSummary(ctx, data, clusterID)
	case TypeVMResourceUsage:
		data.Title = fmt.Sprintf("VM Resource Usage Report - %s", cluster.Name)
		return g.generateVMResourceUsage(ctx, data, clusterID, since)
	default:
		return nil, fmt.Errorf("unsupported report type: %s", reportType)
	}
}

func (g *Generator) generateResourceUtilization(ctx context.Context, data *ReportData, clusterID uuid.UUID, since time.Time) (*ReportData, error) {
	nodes, err := g.queries.ListNodesByCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	// Per-node summary: aggregate all days into one row per node.
	section := ReportSection{
		Title:   "Node Resource Utilization (Period Average)",
		Headers: []string{"Node", "Status", "CPU Avg %", "CPU Peak %", "Mem Avg %", "Mem Peak %", "Mem Used", "Mem Total", "Disk Read/s", "Disk Write/s", "Net In/s", "Net Out/s"},
		Rows:    []map[string]string{},
	}

	for _, node := range nodes {
		metrics, mErr := g.queries.GetNodeMetricsDailyAvg(ctx, db.GetNodeMetricsDailyAvgParams{
			NodeID: node.ID,
			Bucket: since,
		})
		if mErr != nil {
			g.logger.Warn("failed to get node daily metrics", "node", node.Name, "error", mErr)
			continue
		}

		// Fetch IO rates computed from raw counter deltas.
		ioRates, ioErr := g.queries.GetNodeIODailyRate(ctx, db.GetNodeIODailyRateParams{
			NodeID: node.ID,
			Time:   since,
		})
		if ioErr != nil {
			g.logger.Warn("failed to get node IO rates", "node", node.Name, "error", ioErr)
		}

		if len(metrics) == 0 {
			section.Rows = append(section.Rows, map[string]string{
				"Node":         node.Name,
				"Status":       node.Status,
				"CPU Avg %":    "N/A",
				"CPU Peak %":   "N/A",
				"Mem Avg %":    "N/A",
				"Mem Peak %":   "N/A",
				"Mem Used":     "N/A",
				"Mem Total":    "N/A",
				"Disk Read/s":  "N/A",
				"Disk Write/s": "N/A",
				"Net In/s":     "N/A",
				"Net Out/s":    "N/A",
			})
			continue
		}

		// Aggregate CPU/memory across all days for this node.
		var cpuSum, cpuMax, memUsedSum, memUsedMax, memTotalSum float64
		for _, m := range metrics {
			cpuSum += m.Cpu
			if m.CpuMax > cpuMax {
				cpuMax = m.CpuMax
			}
			memUsedSum += m.MemUsed
			if m.MemUsedMax > memUsedMax {
				memUsedMax = m.MemUsedMax
			}
			memTotalSum += m.MemTotal
		}
		n := float64(len(metrics))
		cpuAvg := cpuSum / n
		memAvgTotal := memTotalSum / n
		memAvgUsed := memUsedSum / n
		memPct := 0.0
		memMaxPct := 0.0
		if memAvgTotal > 0 {
			memPct = memAvgUsed / memAvgTotal * 100
			memMaxPct = memUsedMax / memAvgTotal * 100
		}

		// Aggregate IO rates across all days.
		var diskReadSum, diskWriteSum, netInSum, netOutSum float64
		ioCount := float64(len(ioRates))
		for _, io := range ioRates {
			diskReadSum += io.DiskReadRate
			diskWriteSum += io.DiskWriteRate
			netInSum += io.NetInRate
			netOutSum += io.NetOutRate
		}
		diskReadAvg, diskWriteAvg, netInAvg, netOutAvg := 0.0, 0.0, 0.0, 0.0
		if ioCount > 0 {
			diskReadAvg = diskReadSum / ioCount
			diskWriteAvg = diskWriteSum / ioCount
			netInAvg = netInSum / ioCount
			netOutAvg = netOutSum / ioCount
		}

		section.Rows = append(section.Rows, map[string]string{
			"Node":         node.Name,
			"Status":       node.Status,
			"CPU Avg %":    fmt.Sprintf("%.1f", cpuAvg),
			"CPU Peak %":   fmt.Sprintf("%.1f", cpuMax),
			"Mem Avg %":    fmt.Sprintf("%.1f", memPct),
			"Mem Peak %":   fmt.Sprintf("%.1f", memMaxPct),
			"Mem Used":     formatBytes(memAvgUsed),
			"Mem Total":    formatBytes(memAvgTotal),
			"Disk Read/s":  formatBytesRate(diskReadAvg),
			"Disk Write/s": formatBytesRate(diskWriteAvg),
			"Net In/s":     formatBytesRate(netInAvg),
			"Net Out/s":    formatBytesRate(netOutAvg),
		})
	}

	data.Sections = append(data.Sections, section)

	// Cluster-wide daily trend.
	clusterMetrics, err := g.queries.GetClusterMetricsDailyAvg(ctx, db.GetClusterMetricsDailyAvgParams{
		ClusterID: clusterID,
		Bucket:    since,
	})
	if err == nil && len(clusterMetrics) > 0 {
		// Compute cluster-wide IO rates per day by summing all nodes.
		type dayIO struct {
			diskRead, diskWrite, netIn, netOut float64
			count                              int
		}
		clusterIO := make(map[string]*dayIO)
		for _, node := range nodes {
			ioRates, ioErr := g.queries.GetNodeIODailyRate(ctx, db.GetNodeIODailyRateParams{
				NodeID: node.ID,
				Time:   since,
			})
			if ioErr != nil {
				continue
			}
			for _, io := range ioRates {
				key := io.Day.Format("2006-01-02")
				d, ok := clusterIO[key]
				if !ok {
					d = &dayIO{}
					clusterIO[key] = d
				}
				d.diskRead += io.DiskReadRate
				d.diskWrite += io.DiskWriteRate
				d.netIn += io.NetInRate
				d.netOut += io.NetOutRate
				d.count++
			}
		}

		trend := ReportSection{
			Title:   "Cluster Daily Trend",
			Headers: []string{"Day", "CPU Avg %", "CPU Peak %", "Mem Avg %", "Mem Peak %", "Disk Read/s", "Disk Write/s", "Net In/s", "Net Out/s"},
			Rows:    []map[string]string{},
		}
		for _, m := range clusterMetrics {
			memPct := 0.0
			memMaxPct := 0.0
			if m.MemTotal > 0 {
				memPct = m.MemUsed / m.MemTotal * 100
				memMaxPct = m.MemUsedMax / m.MemTotal * 100
			}
			dayKey := m.Day.Format("2006-01-02")
			diskR, diskW, nIn, nOut := "N/A", "N/A", "N/A", "N/A"
			if d, ok := clusterIO[dayKey]; ok && d.count > 0 {
				diskR = formatBytesRate(d.diskRead)
				diskW = formatBytesRate(d.diskWrite)
				nIn = formatBytesRate(d.netIn)
				nOut = formatBytesRate(d.netOut)
			}
			trend.Rows = append(trend.Rows, map[string]string{
				"Day":          dayKey,
				"CPU Avg %":    fmt.Sprintf("%.1f", m.Cpu),
				"CPU Peak %":   fmt.Sprintf("%.1f", m.CpuMax),
				"Mem Avg %":    fmt.Sprintf("%.1f", memPct),
				"Mem Peak %":   fmt.Sprintf("%.1f", memMaxPct),
				"Disk Read/s":  diskR,
				"Disk Write/s": diskW,
				"Net In/s":     nIn,
				"Net Out/s":    nOut,
			})
		}
		data.Sections = append(data.Sections, trend)
	}

	return data, nil
}

func (g *Generator) generateCapacityForecast(ctx context.Context, data *ReportData, clusterID uuid.UUID, since time.Time) (*ReportData, error) {
	nodes, err := g.queries.ListNodesByCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	section := ReportSection{
		Title:   "Capacity Forecast (Linear Regression)",
		Headers: []string{"Node", "Metric", "Current Value", "Trend/Day", "Days to Exhaust", "Exhaustion Date"},
		Rows:    []map[string]string{},
	}

	for _, node := range nodes {
		metrics, mErr := g.queries.GetNodeMetricsDailyAvg(ctx, db.GetNodeMetricsDailyAvgParams{
			NodeID: node.ID,
			Bucket: since,
		})
		if mErr != nil || len(metrics) < 2 {
			continue
		}

		times := make([]time.Time, len(metrics))
		cpuVals := make([]float64, len(metrics))
		memVals := make([]float64, len(metrics))

		for i, m := range metrics {
			times[i] = m.Day
			cpuVals[i] = m.Cpu
			if m.MemTotal > 0 {
				memVals[i] = m.MemUsed / m.MemTotal * 100
			}
		}

		// CPU forecast to 100%
		addForecastRow(&section, node.Name, "CPU %", cpuVals, times, 100)
		// Memory forecast to 100%
		addForecastRow(&section, node.Name, "Memory %", memVals, times, 100)
	}

	data.Sections = append(data.Sections, section)
	return data, nil
}

func addForecastRow(section *ReportSection, nodeName, metric string, values []float64, times []time.Time, threshold float64) {
	if len(values) < 2 {
		return
	}
	currentVal := values[len(values)-1]

	// Compute trend per day using linear regression
	start := times[0]
	xs := make([]float64, len(times))
	for i, t := range times {
		xs[i] = t.Sub(start).Hours() / 24.0
	}
	slope, _, ok := LinearRegression(xs, values)

	row := map[string]string{
		"Node":            nodeName,
		"Metric":          metric,
		"Current Value":   fmt.Sprintf("%.1f%%", currentVal),
		"Trend/Day":       "N/A",
		"Days to Exhaust": "N/A",
		"Exhaustion Date": "N/A",
	}

	if ok {
		row["Trend/Day"] = fmt.Sprintf("%+.2f%%", slope)
		days, date := ForecastMetric(times, values, threshold)
		if days != nil {
			row["Days to Exhaust"] = fmt.Sprintf("%.0f", *days)
		}
		if date != nil {
			row["Exhaustion Date"] = date.Format("2006-01-02")
		}
	}

	section.Rows = append(section.Rows, row)
}

func (g *Generator) generateBackupCompliance(ctx context.Context, data *ReportData, clusterID uuid.UUID) (*ReportData, error) {
	vms, err := g.queries.ListVMsByCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}

	nodes, err := g.queries.ListNodesByCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	nodeNames := make(map[uuid.UUID]string)
	for _, n := range nodes {
		nodeNames[n.ID] = n.Name
	}

	// Get PBS snapshots for this cluster's VMs
	type backupInfo struct {
		lastTime time.Time
	}
	backups := make(map[int]backupInfo) // vmid -> last backup

	// Look up PBS snapshots by backup_id (VMID as string)
	for _, vm := range vms {
		snaps, sErr := g.queries.ListPBSSnapshotsByBackupID(ctx, strconv.Itoa(int(vm.Vmid)))
		if sErr != nil || len(snaps) == 0 {
			continue
		}
		backups[int(vm.Vmid)] = backupInfo{lastTime: time.Unix(snaps[0].BackupTime, 0)}
	}

	staleThreshold := 48 * time.Hour
	var totalVMs, backedUp, missing, stale int

	section := ReportSection{
		Title:   "VM Backup Compliance",
		Headers: []string{"VM Name", "VMID", "Node", "Has Backup", "Last Backup", "Backup Age", "Status"},
		Rows:    []map[string]string{},
	}

	for _, vm := range vms {
		if vm.Template {
			continue
		}
		totalVMs++

		row := map[string]string{
			"VM Name":     vm.Name,
			"VMID":        strconv.Itoa(int(vm.Vmid)),
			"Node":        nodeNames[vm.NodeID],
			"Has Backup":  "No",
			"Last Backup": "N/A",
			"Backup Age":  "N/A",
			"Status":      "Missing",
		}

		if b, ok := backups[int(vm.Vmid)]; ok {
			backedUp++
			age := time.Since(b.lastTime)
			row["Has Backup"] = "Yes"
			row["Last Backup"] = b.lastTime.Format("2006-01-02 15:04")
			row["Backup Age"] = formatDuration(age)
			if age > staleThreshold {
				stale++
				row["Status"] = "Stale"
			} else {
				row["Status"] = "OK"
			}
		} else {
			missing++
		}

		section.Rows = append(section.Rows, row)
	}

	coveragePct := 0.0
	if totalVMs > 0 {
		coveragePct = float64(backedUp) / float64(totalVMs) * 100
	}

	summary := ReportSection{
		Title:   "Backup Compliance Summary",
		Headers: []string{"Total VMs", "Backed Up", "Missing Backups", "Stale Backups", "Coverage %"},
		Rows: []map[string]string{{
			"Total VMs":       strconv.Itoa(totalVMs),
			"Backed Up":       strconv.Itoa(backedUp),
			"Missing Backups": strconv.Itoa(missing),
			"Stale Backups":   strconv.Itoa(stale),
			"Coverage %":      fmt.Sprintf("%.1f", coveragePct),
		}},
	}

	data.Sections = append(data.Sections, summary, section)
	return data, nil
}

func (g *Generator) generatePatchStatus(ctx context.Context, data *ReportData, clusterID uuid.UUID) (*ReportData, error) {
	scan, err := g.queries.GetLatestCVEScan(ctx, clusterID)
	if err != nil {
		// No scan data — return empty sections
		data.Sections = append(data.Sections, ReportSection{
			Title:   "Patch Status",
			Headers: []string{"Info"},
			Rows: []map[string]string{{
				"Info": "No CVE scan data available for this cluster",
			}},
		})
		return data, nil
	}

	scanNodes, err := g.queries.ListCVEScanNodes(ctx, scan.ID)
	if err != nil {
		return nil, fmt.Errorf("list scan nodes: %w", err)
	}

	summary := ReportSection{
		Title:   "CVE Scan Summary",
		Headers: []string{"Scan Date", "Total Vulnerabilities", "Critical", "High", "Medium", "Low", "Status"},
		Rows: []map[string]string{{
			"Scan Date":            scan.CreatedAt.Format("2006-01-02 15:04"),
			"Total Vulnerabilities": strconv.FormatInt(int64(scan.TotalVulns), 10),
			"Critical":             strconv.FormatInt(int64(scan.CriticalCount), 10),
			"High":                 strconv.FormatInt(int64(scan.HighCount), 10),
			"Medium":               strconv.FormatInt(int64(scan.MediumCount), 10),
			"Low":                  strconv.FormatInt(int64(scan.LowCount), 10),
			"Status":               scan.Status,
		}},
	}

	nodeSection := ReportSection{
		Title:   "Per-Node Vulnerability Breakdown",
		Headers: []string{"Node", "Total Vulns", "Posture Score", "Status"},
		Rows:    []map[string]string{},
	}
	for _, n := range scanNodes {
		posture := float64(0)
		if n.PostureScore.Valid {
			posture = float64(n.PostureScore.Float32)
		}
		nodeSection.Rows = append(nodeSection.Rows, map[string]string{
			"Node":          n.NodeName,
			"Total Vulns":   strconv.FormatInt(int64(n.VulnsFound), 10),
			"Posture Score": fmt.Sprintf("%.0f", posture),
			"Status":        n.Status,
		})
	}

	data.Sections = append(data.Sections, summary, nodeSection)
	return data, nil
}

func (g *Generator) generateUptimeSummary(ctx context.Context, data *ReportData, clusterID uuid.UUID) (*ReportData, error) {
	nodes, err := g.queries.ListNodesByCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	section := ReportSection{
		Title:   "Node Uptime",
		Headers: []string{"Node", "Status", "Uptime", "PVE Version"},
		Rows:    []map[string]string{},
	}

	var totalUptime, maxUptime int64
	onlineCount := 0
	for _, n := range nodes {
		uptime := time.Duration(n.Uptime) * time.Second
		section.Rows = append(section.Rows, map[string]string{
			"Node":        n.Name,
			"Status":      n.Status,
			"Uptime":      formatDuration(uptime),
			"PVE Version": n.PveVersion,
		})
		totalUptime += n.Uptime
		if n.Uptime > maxUptime {
			maxUptime = n.Uptime
		}
		if n.Status == "online" {
			onlineCount++
		}
	}

	slaPct := 0.0
	if len(nodes) > 0 {
		slaPct = float64(onlineCount) / float64(len(nodes)) * 100
	}

	summary := ReportSection{
		Title:   "Cluster Uptime Summary",
		Headers: []string{"Total Nodes", "Online Nodes", "SLA %", "Longest Uptime"},
		Rows: []map[string]string{{
			"Total Nodes":    strconv.Itoa(len(nodes)),
			"Online Nodes":   strconv.Itoa(onlineCount),
			"SLA %":          fmt.Sprintf("%.1f", slaPct),
			"Longest Uptime": formatDuration(time.Duration(maxUptime) * time.Second),
		}},
	}

	data.Sections = append(data.Sections, summary, section)
	return data, nil
}

func (g *Generator) generateVMResourceUsage(ctx context.Context, data *ReportData, clusterID uuid.UUID, since time.Time) (*ReportData, error) {
	vms, err := g.queries.ListVMsByCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}

	nodes, err := g.queries.ListNodesByCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	nodeNames := make(map[uuid.UUID]string)
	for _, n := range nodes {
		nodeNames[n.ID] = n.Name
	}

	// Collect per-VM metrics.
	stats := make([]vmReportStats, 0, len(vms))

	for _, vm := range vms {
		if vm.Template {
			continue
		}

		s := vmReportStats{
			name:      vm.Name,
			nodeName:  nodeNames[vm.NodeID],
			vmType:    vm.Type,
			status:    vm.Status,
			vmid:      int(vm.Vmid),
			cpuCount:  int(vm.CpuCount),
			diskTotal: vm.DiskTotal,
		}

		metrics, mErr := g.queries.GetVMMetricsDailyAvg(ctx, db.GetVMMetricsDailyAvgParams{
			VmID:   vm.ID,
			Bucket: since,
		})
		if mErr == nil && len(metrics) > 0 {
			var cpuSum, cpuMax, memUsedSum, memTotalSum, memUsedMax float64
			for _, m := range metrics {
				cpuSum += m.Cpu
				if m.CpuMax > cpuMax {
					cpuMax = m.CpuMax
				}
				memUsedSum += m.MemUsed
				if m.MemUsedMax > memUsedMax {
					memUsedMax = m.MemUsedMax
				}
				memTotalSum += m.MemTotal
			}
			n := float64(len(metrics))
			s.cpuAvg = cpuSum / n
			s.cpuMax = cpuMax
			s.memUsed = memUsedSum / n
			s.memTotal = memTotalSum / n
			if s.memTotal > 0 {
				s.memPct = s.memUsed / s.memTotal * 100
			}
		}

		ioRates, ioErr := g.queries.GetVMIODailyRate(ctx, db.GetVMIODailyRateParams{
			VmID: vm.ID,
			Time: since,
		})
		if ioErr == nil && len(ioRates) > 0 {
			var drSum, dwSum, niSum, noSum float64
			for _, io := range ioRates {
				drSum += io.DiskReadRate
				dwSum += io.DiskWriteRate
				niSum += io.NetInRate
				noSum += io.NetOutRate
			}
			ioN := float64(len(ioRates))
			s.diskRead = drSum / ioN
			s.diskWrite = dwSum / ioN
			s.netIn = niSum / ioN
			s.netOut = noSum / ioN
		}

		stats = append(stats, s)
	}

	// --- Section 1: VM Inventory Summary ---
	var totalVMs, runningVMs, stoppedVMs, qemuCount, lxcCount int
	for _, s := range stats {
		totalVMs++
		switch s.status {
		case "running":
			runningVMs++
		case "stopped":
			stoppedVMs++
		}
		switch s.vmType {
		case "qemu":
			qemuCount++
		case "lxc":
			lxcCount++
		}
	}
	inventorySummary := ReportSection{
		Title:   "VM Inventory Summary",
		Headers: []string{"Total VMs/CTs", "Running", "Stopped", "QEMU VMs", "LXC Containers"},
		Rows: []map[string]string{{
			"Total VMs/CTs":  strconv.Itoa(totalVMs),
			"Running":        strconv.Itoa(runningVMs),
			"Stopped":        strconv.Itoa(stoppedVMs),
			"QEMU VMs":       strconv.Itoa(qemuCount),
			"LXC Containers": strconv.Itoa(lxcCount),
		}},
	}

	// --- Section 2: Top CPU Consumers (top 10) ---
	topCPU := make([]vmReportStats, len(stats))
	copy(topCPU, stats)
	sortDesc(topCPU, func(s vmReportStats) float64 { return s.cpuAvg })
	limit := 10
	if len(topCPU) < limit {
		limit = len(topCPU)
	}
	cpuSection := ReportSection{
		Title:   "Top CPU Consumers (Avg %)",
		Headers: []string{"VM Name", "VMID", "Type", "Node", "Status", "vCPUs", "CPU Avg %", "CPU Peak %"},
		Rows:    []map[string]string{},
	}
	for _, s := range topCPU[:limit] {
		cpuSection.Rows = append(cpuSection.Rows, map[string]string{
			"VM Name":    s.name,
			"VMID":       strconv.Itoa(s.vmid),
			"Type":       s.vmType,
			"Node":       s.nodeName,
			"Status":     s.status,
			"vCPUs":      strconv.Itoa(s.cpuCount),
			"CPU Avg %":  fmt.Sprintf("%.1f", s.cpuAvg),
			"CPU Peak %": fmt.Sprintf("%.1f", s.cpuMax),
		})
	}

	// --- Section 3: Top Memory Consumers (top 10) ---
	topMem := make([]vmReportStats, len(stats))
	copy(topMem, stats)
	sortDesc(topMem, func(s vmReportStats) float64 { return s.memPct })
	limit = 10
	if len(topMem) < limit {
		limit = len(topMem)
	}
	memSection := ReportSection{
		Title:   "Top Memory Consumers (Avg %)",
		Headers: []string{"VM Name", "VMID", "Type", "Node", "Status", "Mem Avg %", "Mem Used", "Mem Total"},
		Rows:    []map[string]string{},
	}
	for _, s := range topMem[:limit] {
		memSection.Rows = append(memSection.Rows, map[string]string{
			"VM Name":   s.name,
			"VMID":      strconv.Itoa(s.vmid),
			"Type":      s.vmType,
			"Node":      s.nodeName,
			"Status":    s.status,
			"Mem Avg %": fmt.Sprintf("%.1f", s.memPct),
			"Mem Used":  formatBytes(s.memUsed),
			"Mem Total": formatBytes(s.memTotal),
		})
	}

	// --- Section 4: Top Network Consumers (top 10) ---
	topNet := make([]vmReportStats, len(stats))
	copy(topNet, stats)
	sortDesc(topNet, func(s vmReportStats) float64 { return s.netIn + s.netOut })
	limit = 10
	if len(topNet) < limit {
		limit = len(topNet)
	}
	netSection := ReportSection{
		Title:   "Top Network Consumers (Avg In+Out)",
		Headers: []string{"VM Name", "VMID", "Type", "Node", "Net In/s", "Net Out/s", "Total/s"},
		Rows:    []map[string]string{},
	}
	for _, s := range topNet[:limit] {
		netSection.Rows = append(netSection.Rows, map[string]string{
			"VM Name":  s.name,
			"VMID":     strconv.Itoa(s.vmid),
			"Type":     s.vmType,
			"Node":     s.nodeName,
			"Net In/s": formatBytesRate(s.netIn),
			"Net Out/s": formatBytesRate(s.netOut),
			"Total/s":  formatBytesRate(s.netIn + s.netOut),
		})
	}

	// --- Section 5: Top Disk I/O Consumers (top 10) ---
	topDisk := make([]vmReportStats, len(stats))
	copy(topDisk, stats)
	sortDesc(topDisk, func(s vmReportStats) float64 { return s.diskRead + s.diskWrite })
	limit = 10
	if len(topDisk) < limit {
		limit = len(topDisk)
	}
	diskSection := ReportSection{
		Title:   "Top Disk I/O Consumers (Avg Read+Write)",
		Headers: []string{"VM Name", "VMID", "Type", "Node", "Disk Read/s", "Disk Write/s", "Total/s"},
		Rows:    []map[string]string{},
	}
	for _, s := range topDisk[:limit] {
		diskSection.Rows = append(diskSection.Rows, map[string]string{
			"VM Name":      s.name,
			"VMID":         strconv.Itoa(s.vmid),
			"Type":         s.vmType,
			"Node":         s.nodeName,
			"Disk Read/s":  formatBytesRate(s.diskRead),
			"Disk Write/s": formatBytesRate(s.diskWrite),
			"Total/s":      formatBytesRate(s.diskRead + s.diskWrite),
		})
	}

	// --- Section 6: Full VM List ---
	allVMs := ReportSection{
		Title:   "All VMs/Containers",
		Headers: []string{"VM Name", "VMID", "Type", "Node", "Status", "vCPUs", "CPU Avg %", "Mem Avg %", "Disk Alloc", "Net In/s", "Net Out/s"},
		Rows:    []map[string]string{},
	}
	for _, s := range stats {
		allVMs.Rows = append(allVMs.Rows, map[string]string{
			"VM Name":   s.name,
			"VMID":      strconv.Itoa(s.vmid),
			"Type":      s.vmType,
			"Node":      s.nodeName,
			"Status":    s.status,
			"vCPUs":     strconv.Itoa(s.cpuCount),
			"CPU Avg %": fmt.Sprintf("%.1f", s.cpuAvg),
			"Mem Avg %": fmt.Sprintf("%.1f", s.memPct),
			"Disk Alloc": formatBytes(float64(s.diskTotal)),
			"Net In/s":  formatBytesRate(s.netIn),
			"Net Out/s": formatBytesRate(s.netOut),
		})
	}

	data.Sections = append(data.Sections, inventorySummary, cpuSection, memSection, netSection, diskSection, allVMs)
	return data, nil
}

type vmReportStats struct {
	name, nodeName, vmType, status string
	vmid                           int
	cpuAvg, cpuMax                 float64
	memUsed, memTotal              float64
	memPct                         float64
	diskRead, diskWrite            float64
	netIn, netOut                  float64
	cpuCount                       int
	diskTotal                      int64
}

// sortDesc sorts a slice in descending order by the given key function.
func sortDesc(s []vmReportStats, key func(vmReportStats) float64) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if key(s[j]) > key(s[i]) {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

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
