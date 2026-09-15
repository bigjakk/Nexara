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
	return []db.StoragePool{{ClusterID: testCluster, NodeID: testNode, Storage: "store01", Type: "pbs"}}, nil
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
