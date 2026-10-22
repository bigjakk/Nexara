package reports

import (
	"testing"
	"time"
)

// rsGuest ran for the whole of a 30-day window with its own memory figure, at
// levels that raise no flag; each case changes only what it tests.
func rsGuest(vmType string, vcpus int, change func(*rightsizingStats)) rightsizingStats {
	s := rightsizingStats{
		name: "linux01", vmid: 101, vmType: vmType, vcpus: vcpus, allocated: 8 << 30,
		samples: 86400, running: 86400, firstSample: testNow.Add(-30 * 24 * time.Hour),
		cpuAvg: 40, cpuP95: 50, cpuP99: 60, cpuMax: 70,
		guestN: 86400, memAvgPct: 60, memP95Pct: 70, memMaxPct: 80, memP95: 5.6 * (1 << 30), memMax: 6.4 * (1 << 30),
		memSource: memSourceGuest,
	}
	if change != nil {
		change(&s)
	}
	return s
}

// Each flag fires on its side of its threshold, and only where the figure can
// judge it: vCPU over-allocation on VMs (a container's vCPUs are a limit),
// memory tightness on containers (a VM's own figure counts page cache), memory
// flags on the guest's own figure only, and nothing under an hour of running.
func TestRightsizingFlags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    rightsizingStats
		want rsFlags
	}{
		{"a working guest raises nothing", rsGuest("qemu", 4, nil), rsFlags{}},
		{"VM p95 under 25 % on 2 vCPUs", rsGuest("qemu", 2, func(s *rightsizingStats) { s.cpuP95 = 24.9 }), rsFlags{cpuOver: true}},
		{"VM p95 at 25 %", rsGuest("qemu", 2, func(s *rightsizingStats) { s.cpuP95 = 25 }), rsFlags{}},
		{"one vCPU cannot come down", rsGuest("qemu", 1, func(s *rightsizingStats) { s.cpuP95 = 5 }), rsFlags{}},
		{"a container's vCPUs are a limit", rsGuest("lxc", 4, func(s *rightsizingStats) { s.cpuP95 = 5 }), rsFlags{}},
		{"p95 at 90 % is CPU-bound", rsGuest("lxc", 2, func(s *rightsizingStats) { s.cpuP95 = 90 }), rsFlags{cpuBound: true}},
		{"p95 under 90 %", rsGuest("qemu", 2, func(s *rightsizingStats) { s.cpuP95 = 89.9 }), rsFlags{}},
		{"own memory p95 under half", rsGuest("qemu", 2, func(s *rightsizingStats) { s.memP95Pct = 49.9 }), rsFlags{memOver: true}},
		{"own memory p95 at half", rsGuest("qemu", 2, func(s *rightsizingStats) { s.memP95Pct = 50 }), rsFlags{}},
		{"the host's view says nothing", rsGuest("qemu", 2, func(s *rightsizingStats) { s.memP95Pct, s.memSource = 10, memSourceHost }), rsFlags{}},
		{"a container at 90 % is tight", rsGuest("lxc", 2, func(s *rightsizingStats) { s.memP95Pct = 90 }), rsFlags{memTight: true}},
		{"a VM's own 95 % counts page cache", rsGuest("qemu", 2, func(s *rightsizingStats) { s.memP95Pct = 95 }), rsFlags{}},
		{"own figure on just under nine in ten running readings", rsGuest("lxc", 2, func(s *rightsizingStats) { s.memP95Pct, s.guestN = 95, 77759 }), rsFlags{}},
		{"own figure on nine in ten running readings", rsGuest("lxc", 2, func(s *rightsizingStats) { s.memP95Pct, s.guestN = 95, 77760 }), rsFlags{memTight: true}},
		{"running under 10 % of the time", rsGuest("qemu", 4, func(s *rightsizingStats) { s.running = 8639 }), rsFlags{rarelyRuns: true}},
		{"running 10 % of the time", rsGuest("qemu", 4, func(s *rightsizingStats) { s.running = 8640 }), rsFlags{}},
		{"under an hour of running raises nothing", rsGuest("qemu", 8, func(s *rightsizingStats) { s.running, s.cpuP95 = 100, 1 }), rsFlags{}},
		{"never ran raises nothing", rsGuest("qemu", 8, func(s *rightsizingStats) { s.running = 0 }), rsFlags{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rightsizingFlags(tc.s, testNow); got != tc.want {
				t.Errorf("flags = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// What a guest's row and the memory KPI say when the figure is missing or
// partial: no readings, a host-only figure, a mixed one, a VM whose own
// readings cover a tenth of its running time (readings from before the source
// was recorded count against it), and a guest that appeared mid-window.
func TestBuildRightsizing_Rows(t *testing.T) {
	t.Parallel()
	stats := []rightsizingStats{
		rsGuest("qemu", 4, func(s *rightsizingStats) { s.vmid = 101 }),
		rsGuest("qemu", 4, func(s *rightsizingStats) {
			s.name, s.vmid, s.memSource, s.guestN, s.hostN = "linux02", 102, memSourceHost, 0, 86400
		}),
		rsGuest("qemu", 2, func(s *rightsizingStats) {
			s.name, s.vmid, s.samples, s.running, s.guestN, s.memSource = "linux03", 103, 0, 0, 0, memSourceUnknown
		}),
		rsGuest("lxc", 2, func(s *rightsizingStats) {
			s.name, s.vmid, s.firstSample = "linux04", 104, testNow.Add(-10*24*time.Hour)
		}),
		rsGuest("qemu", 2, func(s *rightsizingStats) {
			s.name, s.vmid, s.samples, s.running, s.guestN, s.hostN, s.memSource = "linux05", 105, 1000, 1000, 600, 400, memSourceMixed
		}),
		rsGuest("qemu", 2, func(s *rightsizingStats) { s.name, s.vmid, s.guestN = "linux06", 106, 8640 }),
	}
	data := &ReportData{}
	buildRightsizing(testCtx(), stats, data)
	cpu, mem := sectionTable(data, "CPU", ""), sectionTable(data, "Memory", "")
	if cpu == nil || mem == nil || len(cpu.Rows) != 6 || len(mem.Rows) != 6 {
		t.Fatalf("CPU %+v, Memory %+v: want six rows each", cpu, mem)
	}
	checks := []struct {
		name      string
		cell      Cell
		text, sub string
	}{
		{"busy vCPUs are p95 times vCPUs", cpu.Rows[0][9], "2.0", ""},
		{"no readings in the window", cpu.Rows[2][10], "0.0", "no readings in the window"},
		{"a late guest's uptime says since when", cpu.Rows[3][10], "100.0", "observed since " + testNow.Add(-10*24*time.Hour).Format("2006-01-02")},
		{"the host's view is not a figure", mem.Rows[1][9], "0 %", "can't tell: host view only"},
		{"a partial figure shows its coverage", mem.Rows[4][9], "60 %", ""},
		{"the guest's own p95", mem.Rows[0][6], "70.0", ""},
		{"own readings on a tenth of the running time", mem.Rows[5][9], "10 %", ""},
	}
	for _, c := range checks {
		if c.cell.Text != c.text || c.cell.Sub != c.sub {
			t.Errorf("%s: cell = %q / %q, want %q / %q", c.name, c.cell.Text, c.cell.Sub, c.text, c.sub)
		}
	}
	if k := kpiByLabel(data, "Own memory figure"); k == nil || k.Value != "2 of 6" || k.Detail != "2 partial · 1 host view only · 1 not recorded" {
		t.Errorf("own memory KPI = %+v, want 2 of 6 with 2 partial, 1 host view only, 1 not recorded", k)
	}
}
