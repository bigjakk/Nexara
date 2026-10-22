package reports

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// fakeQuerier is the store the generator-level tests run against. It embeds
// the generated interface so only the queries a test cares about are
// implemented; anything else panics with a nil dereference, which is the
// right outcome for a query a test did not expect.
type fakeQuerier struct {
	db.Querier

	cluster    db.Cluster
	veeamErr   error
	pbsMetErr  error
	veeamReads int

	// The resource reports' inputs. Unset nodes and pools fall back to the
	// single node and PBS pool the backup tests use.
	nodes     []db.Node
	pools     []db.StoragePool
	vms       []db.Vm
	nodeDaily map[uuid.UUID][]db.GetNodeMetricsDailyAvgRow
	vmDaily   map[uuid.UUID][]db.GetVMMetricsDailyAvgRow
	vmIO      map[uuid.UUID][]db.GetVMIODailyRateRow
	rightsize []db.GetVMRightsizingStatsRow
}

var (
	testCluster = uuid.MustParse("00000000-0000-0000-0000-0000000000c1")
	testUser    = uuid.MustParse("00000000-0000-0000-0000-0000000000e1")
	testNode    = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	testPBS     = uuid.MustParse("00000000-0000-0000-0000-000000000501")
	testVeeam   = uuid.MustParse("00000000-0000-0000-0000-000000000701")
)

func (f *fakeQuerier) GetCluster(context.Context, uuid.UUID) (db.Cluster, error) {
	return f.cluster, nil
}
func (f *fakeQuerier) ListAllVMs(context.Context) ([]db.ListAllVMsRow, error) {
	return []db.ListAllVMsRow{
		{ID: uuid.New(), ClusterID: testCluster, NodeID: testNode, Vmid: 100, Name: "dc01", Type: "qemu", Status: "running", ClusterName: "cluster01"},
		{ID: uuid.New(), ClusterID: testCluster, NodeID: testNode, Vmid: 101, Name: "ca01", Type: "qemu", Status: "running", ClusterName: "cluster01"},
	}, nil
}
func (f *fakeQuerier) ListClusters(context.Context) ([]db.Cluster, error) {
	return []db.Cluster{f.cluster}, nil
}
func (f *fakeQuerier) ListStoragePoolsByCluster(context.Context, uuid.UUID) ([]db.StoragePool, error) {
	if f.pools != nil {
		return f.pools, nil
	}
	return []db.StoragePool{{ClusterID: testCluster, NodeID: testNode, Storage: "store01", Type: "pbs"}}, nil
}
func (f *fakeQuerier) ListVMsByCluster(context.Context, uuid.UUID) ([]db.Vm, error) {
	return f.vms, nil
}
func (f *fakeQuerier) GetNodeMetricsDailyAvg(_ context.Context, p db.GetNodeMetricsDailyAvgParams) ([]db.GetNodeMetricsDailyAvgRow, error) {
	return f.nodeDaily[p.NodeID], nil
}
func (f *fakeQuerier) GetNodeIODailyRate(context.Context, db.GetNodeIODailyRateParams) ([]db.GetNodeIODailyRateRow, error) {
	return nil, nil
}
func (f *fakeQuerier) GetVMMetricsDailyAvg(_ context.Context, p db.GetVMMetricsDailyAvgParams) ([]db.GetVMMetricsDailyAvgRow, error) {
	return f.vmDaily[p.VmID], nil
}
func (f *fakeQuerier) GetVMIODailyRate(_ context.Context, p db.GetVMIODailyRateParams) ([]db.GetVMIODailyRateRow, error) {
	return f.vmIO[p.VmID], nil
}
func (f *fakeQuerier) GetVMRightsizingStats(context.Context, db.GetVMRightsizingStatsParams) ([]db.GetVMRightsizingStatsRow, error) {
	return f.rightsize, nil
}
func (f *fakeQuerier) ListPBSServers(context.Context) ([]db.PbsServer, error) {
	return []db.PbsServer{{ID: testPBS, Name: "pbs01"}}, nil
}
func (f *fakeQuerier) ListPBSSnapshotsByServer(context.Context, uuid.UUID) ([]db.PbsSnapshot, error) {
	return []db.PbsSnapshot{{PbsServerID: testPBS, Datastore: "store01", BackupType: "vm", BackupID: "100", BackupTime: testNow.Add(-2 * time.Hour).Unix()}}, nil
}
func (f *fakeQuerier) ListVeeamGuestProtectionForCluster(context.Context, uuid.UUID) ([]db.ListVeeamGuestProtectionForClusterRow, error) {
	f.veeamReads++
	if f.veeamErr != nil {
		return nil, f.veeamErr
	}
	return []db.ListVeeamGuestProtectionForClusterRow{{Vmid: 101, LatestRestorePoint: pgtype.Timestamptz{Time: testNow.Add(-time.Hour), Valid: true}, RestorePointCount: 5, MatchMethod: "smbios", LatestMalwareStatus: "Clean"}}, nil
}
func (f *fakeQuerier) ListVeeamInfrastructureGuestsForCluster(context.Context, uuid.UUID) ([]db.ListVeeamInfrastructureGuestsForClusterRow, error) {
	return nil, nil
}
func (f *fakeQuerier) ListNodesByCluster(context.Context, uuid.UUID) ([]db.Node, error) {
	if f.nodes != nil {
		return f.nodes, nil
	}
	return []db.Node{{ID: testNode, ClusterID: testCluster, Name: "pve-01", Status: "online"}}, nil
}
func (f *fakeQuerier) GetLatestPBSDatastoreMetrics(context.Context, uuid.UUID) ([]db.PbsDatastoreMetric, error) {
	if f.pbsMetErr != nil {
		return nil, f.pbsMetErr
	}
	return []db.PbsDatastoreMetric{{Time: testNow, PbsServerID: testPBS, Datastore: "store01", Total: 8 << 40, Used: 3 << 40, Avail: 5 << 40}}, nil
}
func (f *fakeQuerier) GetPBSDatastoreMetricsHistory(context.Context, db.GetPBSDatastoreMetricsHistoryParams) ([]db.GetPBSDatastoreMetricsHistoryRow, error) {
	return nil, nil
}
func (f *fakeQuerier) ListVeeamPlatformsForCluster(context.Context, uuid.UUID) ([]db.ListVeeamPlatformsForClusterRow, error) {
	return []db.ListVeeamPlatformsForClusterRow{{VeeamServerID: testVeeam, PlatformID: uuid.New(), ServerName: "vbr01", ServerVersion: "13.1.0.411"}}, nil
}
func (f *fakeQuerier) ListVeeamRepositoriesByServer(context.Context, uuid.UUID) ([]db.VeeamRepository, error) {
	return nil, nil
}
func (f *fakeQuerier) ListVeeamOrphanedObjects(context.Context, uuid.UUID) ([]db.ListVeeamOrphanedObjectsRow, error) {
	return nil, nil
}
func (f *fakeQuerier) ListTaskHistoryByTypeInWindow(context.Context, db.ListTaskHistoryByTypeInWindowParams) ([]db.TaskHistory, error) {
	return nil, nil
}
func (f *fakeQuerier) ListVeeamSessionsForClusterInWindow(context.Context, db.ListVeeamSessionsForClusterInWindowParams) ([]db.VeeamSession, error) {
	return nil, nil
}

// fakePerms answers the one question the generator asks.
type fakePerms struct {
	allow bool
	err   error
	asked []string
}

func (p *fakePerms) HasPermission(_ context.Context, user uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
	p.asked = append(p.asked, strings.Join([]string{user.String(), action, resource, scopeType, scopeID.String()}, " "))
	return p.allow, p.err
}

func newTestGenerator(q db.Querier, perms PermissionChecker) *Generator {
	g := NewGenerator(q, perms, nil)
	g.now = func() time.Time { return testNow }
	return g
}

func backupRequest() Request {
	return Request{Type: string(TypeBackupCompliance), ClusterID: testCluster, TimeRangeHours: 168, RequestedBy: testUser}
}

func hasSection(d *ReportData, title string) bool {
	for _, s := range d.Sections {
		if s.Title == title {
			return true
		}
	}
	return false
}

// sectionTable is the table block titled block in section ("" for untitled).
func sectionTable(d *ReportData, section, block string) *Table {
	for _, s := range d.Sections {
		for _, b := range s.Blocks {
			if s.Title == section && b.Kind == BlockTable && b.Title == block {
				return b.Table
			}
		}
	}
	return nil
}

func generate(t *testing.T, q *fakeQuerier, rt ReportType) *ReportData {
	t.Helper()
	data, err := newTestGenerator(q, nil).Generate(context.Background(), Request{Type: string(rt), ClusterID: testCluster, TimeRangeHours: 168})
	if err != nil {
		t.Fatalf("Generate %s: %v", rt, err)
	}
	return data
}

func TestGenerate_VeeamRequiresTheRequestersGrant(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		perms     PermissionChecker
		user      uuid.UUID
		wantVeeam bool
	}{
		{"no engine at all", nil, testUser, false},
		{"engine denies", &fakePerms{allow: false}, testUser, false},
		{"engine errors", &fakePerms{err: errors.New("redis down")}, testUser, false},
		{"no requesting user", &fakePerms{allow: true}, uuid.Nil, false},
		{"engine allows", &fakePerms{allow: true}, testUser, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := &fakeQuerier{cluster: db.Cluster{ID: testCluster, Name: "cluster01"}}
			g := newTestGenerator(q, tc.perms)
			req := backupRequest()
			req.RequestedBy = tc.user
			data, err := g.Generate(context.Background(), req)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			veeamMentioned := strings.Contains(data.Subtitle, "Veeam")
			if veeamMentioned != tc.wantVeeam {
				t.Errorf("subtitle = %q, want Veeam mentioned = %v", data.Subtitle, tc.wantVeeam)
			}
			if (q.veeamReads > 0) != tc.wantVeeam {
				t.Errorf("Veeam protection was read %d times, want reads = %v", q.veeamReads, tc.wantVeeam)
			}
			// The Veeam-only guest is protected only when Veeam was consulted.
			var guests *Table
			for _, s := range data.Sections {
				if s.Title == "Guests" {
					guests = s.Blocks[0].Table
				}
			}
			if guests == nil {
				t.Fatal("no Guests section")
			}
			for _, row := range guests.Rows {
				if row[1].Text == "ca01" {
					if got := row[4].Text; (got == "Protected") != tc.wantVeeam {
						t.Errorf("ca01 coverage = %q, want protected = %v", got, tc.wantVeeam)
					}
				}
			}
			if p, ok := tc.perms.(*fakePerms); ok && tc.user != uuid.Nil {
				if len(p.asked) != 1 || !strings.Contains(p.asked[0], "view veeam cluster "+testCluster.String()) {
					t.Errorf("permission asked = %v, want one view:veeam check on the cluster", p.asked)
				}
			}
		})
	}
}

func TestGenerate_FailsLoudlyWhenAConsultedSourceIsUnreadable(t *testing.T) {
	t.Parallel()
	// Veeam is in scope and unreadable: the run must fail rather than report
	// every Veeam-only guest as unprotected.
	q := &fakeQuerier{cluster: db.Cluster{ID: testCluster, Name: "cluster01"}, veeamErr: errors.New("veeam tables unavailable")}
	if _, err := newTestGenerator(q, &fakePerms{allow: true}).Generate(context.Background(), backupRequest()); err == nil {
		t.Error("expected an error when the Veeam read fails under a view:veeam grant")
	}
	// Out of scope, the same failure is irrelevant: the tables are never read.
	q = &fakeQuerier{cluster: db.Cluster{ID: testCluster, Name: "cluster01"}, veeamErr: errors.New("veeam tables unavailable")}
	if _, err := newTestGenerator(q, &fakePerms{allow: false}).Generate(context.Background(), backupRequest()); err != nil {
		t.Errorf("a denied requester must not be affected by the Veeam read: %v", err)
	}
	// PBS capacity metrics failing must not become "no datastore mounted".
	q = &fakeQuerier{cluster: db.Cluster{ID: testCluster, Name: "cluster01"}, pbsMetErr: errors.New("metrics unavailable")}
	if _, err := newTestGenerator(q, nil).Generate(context.Background(), backupRequest()); err == nil {
		t.Error("expected an error when PBS datastore metrics cannot be read")
	}
}

func TestGenerate_ParamsDefaultFieldByField(t *testing.T) {
	t.Parallel()
	q := &fakeQuerier{cluster: db.Cluster{ID: testCluster, Name: "cluster01"}}
	req := backupRequest()
	req.Params = Params{TopN: 5} // stale hours deliberately unset
	data, err := newTestGenerator(q, nil).Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(data.KPIs[0].Label, "24 h") {
		t.Errorf("hero label = %q, want the 24 h default when only top_n was set", data.KPIs[0].Label)
	}
	if !hasSection(data, "Coverage") || !hasSection(data, "Backup runs in the period") || !hasSection(data, "Repository capacity") {
		t.Errorf("sections = %v", data.Sections)
	}
	if data.Schema != SchemaVersion || data.ReportType != "backup_compliance" || !strings.HasPrefix(data.Title, "Backup compliance · cluster01 · 7 days to 2026-09-14") {
		t.Errorf("header = %q schema %d type %q", data.Title, data.Schema, data.ReportType)
	}
}

func TestNormalizeParams(t *testing.T) {
	t.Parallel()
	_, raw, err := NormalizeParams(nil)
	if err != nil || string(raw) != "{}" {
		t.Errorf("nil → %q %v", raw, err)
	}
	p, raw, err := NormalizeParams([]byte(`{"stale_after_hours": 48, "future_key": true, "sections": {"runs": false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"stale_after_hours":48,"sections":{"runs":false}}` {
		t.Errorf("canonical = %s", raw)
	}
	if p.StaleAfterHours != 48 || p.TopN != 10 || p.SectionEnabled("runs") {
		t.Errorf("effective = %+v", p)
	}
	if _, _, err := NormalizeParams([]byte(`{"top_n": 0.5}`)); err == nil {
		t.Error("a non-integer top_n must be rejected")
	}
}

// The rollups hold PVE's 0–1 CPU ratio; every resource report must print it as
// percent. 0.33 shown as "0.3", and every running guest counted idle, was the bug.
func TestResourceReports_CPUIsPercent(t *testing.T) {
	t.Parallel()
	vm := uuid.MustParse("00000000-0000-0000-0000-0000000000b1")
	day := func(daysAgo int) time.Time { return testNow.Add(-time.Duration(daysAgo) * 24 * time.Hour) }
	q := &fakeQuerier{
		cluster: db.Cluster{ID: testCluster, Name: "cluster01"},
		vms:     []db.Vm{{ID: vm, NodeID: testNode, Vmid: 101, Name: "linux01", Type: "qemu", Status: "running", CpuCount: 4}},
		nodeDaily: map[uuid.UUID][]db.GetNodeMetricsDailyAvgRow{testNode: {
			{Day: day(2), Cpu: 0.33, CpuMax: 0.5, MemUsed: 4 << 30, MemTotal: 16 << 30},
			{Day: day(1), Cpu: 0.33, CpuMax: 0.5, MemUsed: 4 << 30, MemTotal: 16 << 30},
		}},
		vmDaily:   map[uuid.UUID][]db.GetVMMetricsDailyAvgRow{vm: {{Day: day(1), Cpu: 0.33, CpuMax: 0.5, MemUsed: 1 << 30, MemTotal: 4 << 30}}},
		rightsize: []db.GetVMRightsizingStatsRow{{VmID: vm, Samples: 2880, RunningSamples: 2880, FirstSample: day(2), CpuAvg: 0.33, CpuP95: 0.33, CpuP99: 0.4, CpuMax: 0.5}},
	}
	cases := []struct {
		report  ReportType
		section string
		want    map[int]string // column → first row's cell
	}{
		{TypeResourceUtilization, "Nodes", map[int]string{2: "33.0", 3: "50.0"}},
		{TypeVMResourceUsage, "All guests", map[int]string{6: "33.0"}},
		{TypeCapacityForecast, "Node forecast", map[int]string{2: "33.0%"}},
		{TypeRightsizing, "CPU", map[int]string{5: "33.0", 6: "33.0", 8: "50.0"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.report), func(t *testing.T) {
			t.Parallel()
			data := generate(t, q, tc.report)
			table := sectionTable(data, tc.section, "")
			if table == nil || len(table.Rows) == 0 {
				t.Fatalf("no %q table", tc.section)
			}
			for col, want := range tc.want {
				if got := table.Rows[0][col].Text; got != want {
					t.Errorf("column %d = %q, want %q", col, got, want)
				}
			}
			if tc.report == TypeVMResourceUsage {
				if k := kpiByLabel(data, "Idle while running"); k == nil || k.Value != "0" {
					t.Errorf("idle KPI = %+v, want 0 for a guest at 33 %% CPU", k)
				}
			}
			if tc.report == TypeRightsizing {
				// A year asked for is the 30 days the raw readings cover, in the masthead too.
				long, err := newTestGenerator(q, nil).Generate(context.Background(), Request{Type: string(tc.report), ClusterID: testCluster, TimeRangeHours: 8760})
				if err != nil {
					t.Fatalf("Generate a year: %v", err)
				}
				if long.TimeRange.Hours != rightsizingMaxHours || !strings.Contains(long.Title, "30 days") {
					t.Errorf("a year's request covers %d h, titled %q; want %d h, 30 days", long.TimeRange.Hours, long.Title, rightsizingMaxHours)
				}
			}
		})
	}
}

// The swap warning reads only the guest's own figure, averaged over its own
// readings: a VM without balloon statistics holds its whole allocation on the
// host, and a period average blends that in from before a driver reported.
func TestVMResourceUsage_SwapWarningNeedsTheGuestsOwnFigure(t *testing.T) {
	t.Parallel()
	own, host, late := uuid.MustParse("00000000-0000-0000-0000-0000000000b1"), uuid.MustParse("00000000-0000-0000-0000-0000000000b2"), uuid.MustParse("00000000-0000-0000-0000-0000000000b3")
	day := testNow.Add(-24 * time.Hour)
	q := &fakeQuerier{
		cluster: db.Cluster{ID: testCluster, Name: "cluster01"},
		vms: []db.Vm{
			{ID: own, NodeID: testNode, Vmid: 101, Name: "linux01", Type: "qemu", Status: "running"},
			{ID: host, NodeID: testNode, Vmid: 102, Name: "linux02", Type: "qemu", Status: "running"},
			{ID: late, NodeID: testNode, Vmid: 103, Name: "linux03", Type: "qemu", Status: "running"},
		},
		vmDaily: map[uuid.UUID][]db.GetVMMetricsDailyAvgRow{
			own:  {{Day: day, Cpu: 0.05, MemUsed: 0.96 * (8 << 30), MemTotal: 8 << 30}},
			host: {{Day: day, Cpu: 0.05, MemUsed: 1.004 * (24 << 30), MemTotal: 24 << 30}},
			late: {{Day: day, Cpu: 0.05, MemUsed: 0.979 * (16 << 30), MemTotal: 16 << 30}},
		},
		vmIO: map[uuid.UUID][]db.GetVMIODailyRateRow{
			own:  {{Day: day, GuestSamples: 2016, HostSamples: 3, GuestMemUsed: 2016 * 0.96 * (8 << 30), GuestMemTotal: 2016 * (8 << 30)}},
			host: {{Day: day, HostSamples: 2016}},
			late: {{Day: day, GuestSamples: 1900, HostSamples: 116, GuestMemUsed: 1900 * 0.6 * (16 << 30), GuestMemTotal: 1900 * (16 << 30)}},
		},
	}
	data := generate(t, q, TypeVMResourceUsage)
	if !hasFinding(data, SevWarning, "linux01 (101) averaged 96 % of its memory") {
		t.Errorf("no swap warning for the balloon-reported 96 %%: %v", findingLeads(data))
	}
	if hasFinding(data, SevWarning, "linux02") || hasFinding(data, SevWarning, "linux03") {
		t.Errorf("a figure that is not the guest's own raised a warning: %v", findingLeads(data))
	}
	ownTable, other := sectionTable(data, "Top memory consumers", ""), sectionTable(data, "Top memory consumers", "Not reported by the guest")
	if ownTable == nil || len(ownTable.Rows) != 2 || ownTable.Rows[0][0].Text != "linux01" || ownTable.Rows[1][0].Text != "linux03" || ownTable.Rows[1][3].Text != "60.0" || ownTable.Rows[1][4].Text != "9.6 GiB" {
		t.Errorf("guest-reported table = %+v, want linux01, then linux03 at its own 60.0 (9.6 GiB)", ownTable)
	}
	if other == nil || len(other.Rows) != 1 || other.Rows[0][0].Text != "linux02" || other.Rows[0][3].Text != "100.4" || other.Rows[0][6].Text != "host RSS" {
		t.Errorf("host table = %+v, want linux02 at 100.4 labelled host RSS", other)
	}
	if all := sectionTable(data, "All guests", ""); all == nil || len(all.Rows) != 3 || all.Rows[1][8].Text != "host RSS" {
		t.Errorf("All guests = %+v, want linux02's mem source as host RSS", all)
	}
}

// Shared pools are one pool however many nodes report them; local and
// local-lvm are one per node. Keying every pool on its name kept one node's.
func TestCapacityForecast_StoragePoolsPerNodeUnlessShared(t *testing.T) {
	t.Parallel()
	nodes := []db.Node{
		{ID: uuid.MustParse("00000000-0000-0000-0000-0000000000a1"), Name: "pve-01", Status: "online"},
		{ID: uuid.MustParse("00000000-0000-0000-0000-0000000000a2"), Name: "pve-02", Status: "online"},
		{ID: uuid.MustParse("00000000-0000-0000-0000-0000000000a3"), Name: "pve-03", Status: "online"},
	}
	pools := make([]db.StoragePool, 0, 4*len(nodes))
	for i, n := range nodes {
		used := int64(i+1) << 30
		if n.Name == "pve-02" {
			used = 92 << 30
		}
		pools = append(pools,
			db.StoragePool{NodeID: n.ID, Storage: "local", Type: "dir", Enabled: true, Total: 100 << 30, Used: used},
			db.StoragePool{NodeID: n.ID, Storage: "local-lvm", Type: "lvmthin", Enabled: true, Total: 500 << 30, Used: int64(i+1) << 32},
			db.StoragePool{NodeID: n.ID, Storage: "store01", Type: "rbd", Enabled: true, Shared: true, Total: 4 << 40, Used: int64(10+i) << 37},
			db.StoragePool{NodeID: n.ID, Storage: "store02", Type: "pbs", Enabled: true, Shared: true, Total: 8 << 40, Used: 2 << 40},
		)
	}
	q := &fakeQuerier{cluster: db.Cluster{ID: testCluster, Name: "cluster01"}, nodes: nodes, pools: pools}
	data := generate(t, q, TypeCapacityForecast)

	var meters []Meter
	for _, s := range data.Sections {
		for _, b := range s.Blocks {
			if s.Title == "Storage pools" && b.Kind == BlockMeters {
				meters = b.Meters
			}
		}
	}
	got := map[string]int{}
	for _, m := range meters {
		got[m.Name]++
		if m.Name == "store01" && m.Used != float64(int64(12)<<37) {
			t.Errorf("store01 used = %v, want the highest node's reading", m.Used)
		}
	}
	if len(meters) != 8 || got["local"] != 3 || got["local-lvm"] != 3 || got["store01"] != 1 || got["store02"] != 1 {
		t.Errorf("meters = %v (%d), want local and local-lvm per node and each shared pool once", got, len(meters))
	}
	if meters[0].Detail != "dir · pve-02" {
		t.Errorf("fullest meter detail = %q, want dir · pve-02", meters[0].Detail)
	}
	if !hasFinding(data, SevCritical, "Storage pool local on pve-02 is 92 % full.") {
		t.Errorf("findings = %v, want the full local pool named with its node", findingLeads(data))
	}
	if k := kpiByLabel(data, "Fullest storage pool"); k == nil || k.Detail != "local on pve-02" {
		t.Errorf("fullest pool KPI = %+v, want local on pve-02", k)
	}
}

// The sizes are binary multiples and are labelled so, matching the UI's numbers.
func TestFormatBytes_LabelsBinaryUnits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in          float64
		bytes, rate string
	}{
		{512, "512 B", "512 B/s"},
		{1536, "1.5 KiB", "1.5 KiB/s"},
		{3 << 19, "1.5 MiB", "1.5 MiB/s"},
		{24 << 30, "24.0 GiB", "24.0 GiB/s"},
		{3 << 39, "1.5 TiB", "1536.0 GiB/s"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.bytes {
			t.Errorf("formatBytes(%v) = %q, want %q", c.in, got, c.bytes)
		}
		if got := formatBytesRate(c.in); got != c.rate {
			t.Errorf("formatBytesRate(%v) = %q, want %q", c.in, got, c.rate)
		}
	}
}
