package backupcoverage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The coverage verdict is the sentence this whole package exists to say, and
// every branch of it is a claim about whether someone's data is recoverable.
func TestVerdict(t *testing.T) {
	t.Parallel()
	const (
		now   = int64(1_000_000)
		stale = int64(24 * 3600)
	)
	fresh := now - 3600    // an hour ago
	old := now - 5*24*3600 // five days ago

	tests := []struct {
		name           string
		eligibility    string
		pbs, veeam     bool
		freshest       int64
		wantProtection string
		wantStatus     string
	}{
		{
			name:        "protected by both, recently",
			eligibility: Eligible, pbs: true, veeam: true, freshest: fresh,
			wantProtection: ProtectionBoth, wantStatus: StatusRecent,
		},
		{
			// The case that was simply wrong before Veeam data reached this
			// function: a guest Veeam protects reported "none".
			name:        "Veeam only still counts as protected",
			eligibility: Eligible, pbs: false, veeam: true, freshest: fresh,
			wantProtection: ProtectionVeeam, wantStatus: StatusRecent,
		},
		{
			name:        "PBS only",
			eligibility: Eligible, pbs: true, veeam: false, freshest: fresh,
			wantProtection: ProtectionPBS, wantStatus: StatusRecent,
		},
		{
			name:        "protected but overdue",
			eligibility: Eligible, pbs: true, veeam: false, freshest: old,
			wantProtection: ProtectionPBS, wantStatus: StatusStale,
		},
		{
			// "stale" would read as "there is an old backup" — there is not.
			name:        "unprotected is never stale, even with an ancient timestamp",
			eligibility: Eligible, pbs: false, veeam: false, freshest: old,
			wantProtection: ProtectionNone, wantStatus: StatusNone,
		},
		{
			// A Veeam worker appliance with no backup is not an alarm, and
			// rendering it as one is how a coverage view teaches operators to
			// ignore it.
			name:        "a Veeam worker is not an alarm",
			eligibility: VeeamWorker, pbs: false, veeam: false, freshest: 0,
			wantProtection: ProtectionNotEligible, wantStatus: StatusNotEligible,
		},
		{
			// Ineligibility wins even when a backup exists: the VBR server
			// being backed up does not make it a workload guest.
			name:        "ineligibility outranks an existing backup",
			eligibility: VeeamBackupServer, pbs: true, veeam: true, freshest: fresh,
			wantProtection: ProtectionNotEligible, wantStatus: StatusNotEligible,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			protection, status := Verdict(tt.eligibility, tt.pbs, tt.veeam, tt.freshest, now, stale)
			if protection != tt.wantProtection {
				t.Errorf("protection = %q, want %q", protection, tt.wantProtection)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}

// fakeStore is the in-memory Querier the Compute tests run against.
type fakeStore struct {
	vms      []db.ListAllVMsRow
	clusters []db.Cluster
	pools    map[uuid.UUID][]db.StoragePool
	servers  []db.PbsServer
	snaps    map[uuid.UUID][]db.PbsSnapshot
	veeam    map[uuid.UUID][]db.ListVeeamGuestProtectionForClusterRow
	infra    map[uuid.UUID][]db.ListVeeamInfrastructureGuestsForClusterRow

	veeamErr, infraErr, poolsErr, snapsErr error

	veeamReads []uuid.UUID
}

func (f *fakeStore) ListAllVMs(context.Context) ([]db.ListAllVMsRow, error) { return f.vms, nil }
func (f *fakeStore) ListClusters(context.Context) ([]db.Cluster, error)     { return f.clusters, nil }
func (f *fakeStore) ListStoragePoolsByCluster(_ context.Context, id uuid.UUID) ([]db.StoragePool, error) {
	if f.poolsErr != nil {
		return nil, f.poolsErr
	}
	return f.pools[id], nil
}
func (f *fakeStore) ListPBSServers(context.Context) ([]db.PbsServer, error) { return f.servers, nil }
func (f *fakeStore) ListPBSSnapshotsByServer(_ context.Context, id uuid.UUID) ([]db.PbsSnapshot, error) {
	if f.snapsErr != nil {
		return nil, f.snapsErr
	}
	return f.snaps[id], nil
}
func (f *fakeStore) ListVeeamGuestProtectionForCluster(_ context.Context, id uuid.UUID) ([]db.ListVeeamGuestProtectionForClusterRow, error) {
	f.veeamReads = append(f.veeamReads, id)
	if f.veeamErr != nil {
		return nil, f.veeamErr
	}
	return f.veeam[id], nil
}
func (f *fakeStore) ListVeeamInfrastructureGuestsForCluster(_ context.Context, id uuid.UUID) ([]db.ListVeeamInfrastructureGuestsForClusterRow, error) {
	if f.infraErr != nil {
		return nil, f.infraErr
	}
	return f.infra[id], nil
}

var (
	clusterA = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	clusterB = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	nodeA    = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	pbs01    = uuid.MustParse("00000000-0000-0000-0000-000000000501")
)

func vm(cluster uuid.UUID, vmid int32, name, kind string) db.ListAllVMsRow {
	clusterName := "cluster01"
	if cluster == clusterB {
		clusterName = "cluster02"
	}
	return db.ListAllVMsRow{ID: uuid.New(), ClusterID: cluster, NodeID: nodeA, Vmid: vmid, Name: name, Type: kind, Status: "running", ClusterName: clusterName}
}

func pool(cluster uuid.UUID, storage, kind string) db.StoragePool {
	return db.StoragePool{ClusterID: cluster, NodeID: nodeA, Storage: storage, Type: kind}
}

// The estate every Compute test starts from: two clusters that both have a
// VMID 100, each mounting its own PBS datastore, with Veeam mapped to
// cluster A only.
func newEstate(now time.Time) *fakeStore {
	fresh := now.Add(-3 * time.Hour)
	old := now.Add(-5 * 24 * time.Hour)
	return &fakeStore{
		clusters: []db.Cluster{{ID: clusterA, Name: "cluster01"}, {ID: clusterB, Name: "cluster02"}},
		vms: []db.ListAllVMsRow{
			vm(clusterA, 100, "dc01", "qemu"),
			vm(clusterA, 101, "ca01", "qemu"),    // Veeam only
			vm(clusterA, 102, "linux01", "lxc"),  // PBS only, container
			vm(clusterA, 103, "linux02", "qemu"), // Veeam object, points all pruned
			vm(clusterA, 104, "win01", "qemu"),   // nothing at all
			vm(clusterA, 106, "win02", "qemu"),   // both providers, PBS the older copy
			vm(clusterA, 150, "veeam-worker01", "qemu"),
			vm(clusterA, 151, "vbr01", "qemu"),   // backup server, also listed as a worker
			vm(clusterB, 100, "linux10", "qemu"), // same VMID as cluster A's dc01
			vm(clusterB, 105, "linux11", "qemu"),
		},
		pools: map[uuid.UUID][]db.StoragePool{
			clusterA: {pool(clusterA, "store01", "pbs"), pool(clusterA, "local-zfs", "zfspool")},
			clusterB: {pool(clusterB, "store02", "pbs")},
		},
		servers: []db.PbsServer{{ID: pbs01, Name: "pbs01"}},
		snaps: map[uuid.UUID][]db.PbsSnapshot{
			pbs01: {
				// The old row is listed first so a "first row wins" bug would
				// be caught by the LatestBackup assertion.
				{PbsServerID: pbs01, Datastore: "store01", BackupType: "vm", BackupID: "100", BackupTime: old.Unix()},
				{PbsServerID: pbs01, Datastore: "store01", BackupType: "vm", BackupID: "100", BackupTime: fresh.Unix()},
				{PbsServerID: pbs01, Datastore: "store01", BackupType: "ct", BackupID: "102", BackupTime: old.Unix()},
				{PbsServerID: pbs01, Datastore: "store01", BackupType: "vm", BackupID: "106", BackupTime: old.Unix()},
				// cluster B's VMID 100 backs up to ITS datastore; this row must
				// never count for cluster A's VMID 100 and vice versa.
				{PbsServerID: pbs01, Datastore: "store02", BackupType: "vm", BackupID: "100", BackupTime: old.Unix()},
				// A datastore no cluster mounts: invisible to everyone.
				{PbsServerID: pbs01, Datastore: "orphan-store", BackupType: "vm", BackupID: "105", BackupTime: fresh.Unix()},
			},
		},
		veeam: map[uuid.UUID][]db.ListVeeamGuestProtectionForClusterRow{
			clusterA: {
				{Vmid: 101, LatestRestorePoint: pgtype.Timestamptz{Time: fresh, Valid: true}, LatestMalwareStatus: "Clean", RestorePointCount: 12, RestorePointBytes: 1 << 30, MatchMethod: "smbios"},
				{Vmid: 103, RestorePointCount: 0, MatchMethod: "name", LastRunFailed: true},
				{Vmid: 106, LatestRestorePoint: pgtype.Timestamptz{Time: fresh, Valid: true}, RestorePointCount: 4, MatchMethod: "manual"},
			},
			clusterB: {
				{Vmid: 105, LatestRestorePoint: pgtype.Timestamptz{Time: fresh, Valid: true}, RestorePointCount: 3, MatchMethod: "smbios"},
			},
		},
		infra: map[uuid.UUID][]db.ListVeeamInfrastructureGuestsForClusterRow{
			// The VBR server's worker row deliberately arrives BEFORE its
			// backup_server row: the more specific role must win regardless.
			clusterA: {{Vmid: 150, Role: "worker"}, {Vmid: 151, Role: "worker"}, {Vmid: 151, Role: "backup_server"}},
		},
	}
}

func byName(entries []Entry) map[string]Entry {
	out := make(map[string]Entry, len(entries))
	for _, e := range entries {
		out[e.ClusterName+"/"+e.Name] = e
	}
	return out
}

func mustEntry(t *testing.T, got map[string]Entry, key string) Entry {
	t.Helper()
	e, ok := got[key]
	if !ok {
		t.Fatalf("no entry %q", key)
	}
	return e
}

func allVeeam(uuid.UUID) bool { return true }

func TestCompute_ScopesPBSSnapshotsToTheClusterDatastore(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	store := newEstate(now)

	entries, err := Compute(context.Background(), store, Scope{Veeam: allVeeam}, Options{Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	got := byName(entries)

	// Cluster A's VMID 100 sees its own two snapshots on store01, the fresh one
	// winning; cluster B's VMID 100 sees only the old one on store02.
	a := mustEntry(t, got, "cluster01/dc01")
	if a.BackupCount != 2 || a.CoverageStatus != StatusRecent || a.Protection != ProtectionPBS {
		t.Errorf("cluster A vmid 100 = count %d status %q protection %q, want 2 recent pbs", a.BackupCount, a.CoverageStatus, a.Protection)
	}
	if a.LatestBackup == nil || *a.LatestBackup != now.Add(-3*time.Hour).Unix() || a.Freshest != *a.LatestBackup {
		t.Errorf("cluster A vmid 100 latest backup = %v freshest %d, want the newer snapshot", a.LatestBackup, a.Freshest)
	}
	// Inventory fields pass through untouched.
	if a.ClusterName != "cluster01" || a.ClusterID != clusterA.String() || a.Type != "qemu" || a.Status != "running" || a.NodeID != nodeA || !a.VeeamCapable {
		t.Errorf("inventory fields not carried through: %+v", a)
	}
	b := mustEntry(t, got, "cluster02/linux10")
	if b.BackupCount != 1 || b.CoverageStatus != StatusStale {
		t.Errorf("cluster B vmid 100 = count %d status %q, want 1 stale", b.BackupCount, b.CoverageStatus)
	}
	// A datastore nobody mounts protects nobody, whatever the VMID.
	if e := mustEntry(t, got, "cluster02/linux11"); e.BackupCount != 0 {
		t.Errorf("linux11 counted a snapshot from an unmounted datastore: %+v", e)
	}
}

func TestCompute_VeeamProtectionAndEligibility(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	store := newEstate(now)

	entries, err := Compute(context.Background(), store, Scope{Veeam: allVeeam}, Options{Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	got := byName(entries)

	// Veeam only: protected, recent, and the PBS figures stay empty.
	ca := mustEntry(t, got, "cluster01/ca01")
	if ca.Protection != ProtectionVeeam || ca.CoverageStatus != StatusRecent || ca.LatestBackup != nil {
		t.Errorf("Veeam-only guest = %+v", ca)
	}
	if ca.Veeam == nil || !ca.Veeam.Protected || ca.Veeam.RestorePointCount != 12 || ca.Freshest != now.Add(-3*time.Hour).Unix() {
		t.Errorf("Veeam-only guest veeam side = %+v freshest %d", ca.Veeam, ca.Freshest)
	}
	// Both providers: the newer copy from either side sets the freshness, and
	// the PBS figures still describe PBS alone.
	w2 := mustEntry(t, got, "cluster01/win02")
	if w2.Protection != ProtectionBoth || w2.CoverageStatus != StatusRecent {
		t.Errorf("both-provider guest = %+v", w2)
	}
	if w2.Freshest != now.Add(-3*time.Hour).Unix() || w2.LatestBackup == nil || *w2.LatestBackup != now.Add(-5*24*time.Hour).Unix() || w2.BackupCount != 1 {
		t.Errorf("both-provider freshness = %d, PBS latest %v count %d; want Veeam's fresh point and PBS's old one", w2.Freshest, w2.LatestBackup, w2.BackupCount)
	}
	// An object whose points were all pruned is NOT protection.
	l2 := mustEntry(t, got, "cluster01/linux02")
	if l2.Protection != ProtectionNone || l2.CoverageStatus != StatusNone || l2.Veeam == nil || l2.Veeam.Protected {
		t.Errorf("pruned Veeam object should not protect: %+v veeam %+v", l2, l2.Veeam)
	}
	// Containers are targets, just not Veeam-capable.
	l1 := mustEntry(t, got, "cluster01/linux01")
	if l1.VeeamCapable || l1.Protection != ProtectionPBS || l1.CoverageStatus != StatusStale {
		t.Errorf("container = %+v", l1)
	}
	// The worker appliance is excluded rather than alarmed, and the backup
	// server outranks its own worker role whatever order the rows arrived in.
	w := mustEntry(t, got, "cluster01/veeam-worker01")
	if w.Eligibility != VeeamWorker || w.Protection != ProtectionNotEligible || w.CoverageStatus != StatusNotEligible || w.Freshest != 0 {
		t.Errorf("worker = %+v", w)
	}
	if e := mustEntry(t, got, "cluster01/vbr01"); e.Eligibility != VeeamBackupServer {
		t.Errorf("VBR server eligibility = %q, want backup_server", e.Eligibility)
	}
	// Nothing anywhere.
	if e := mustEntry(t, got, "cluster01/win01"); e.Protection != ProtectionNone || e.Freshest != 0 {
		t.Errorf("unprotected guest = %+v", e)
	}
}

func TestCompute_VeeamScopeIsHonoured(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	store := newEstate(now)

	// The caller may see Veeam data on cluster B only.
	entries, err := Compute(context.Background(), store, Scope{Veeam: OneCluster(clusterB)}, Options{Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	got := byName(entries)

	// Cluster A's Veeam-only guest reads exactly as it did before Veeam
	// existed, and its worker is an ordinary unprotected guest: telling this
	// caller it is a Veeam appliance would leak the mapping they hold no
	// grant over.
	if e := mustEntry(t, got, "cluster01/ca01"); e.Veeam != nil || e.Protection != ProtectionNone {
		t.Errorf("Veeam data leaked into an unscoped cluster: %+v", e)
	}
	if e := mustEntry(t, got, "cluster01/veeam-worker01"); e.Eligibility != Eligible {
		t.Errorf("eligibility leaked into an unscoped cluster: %+v", e)
	}
	if e := mustEntry(t, got, "cluster02/linux11"); e.Veeam == nil || e.Protection != ProtectionVeeam {
		t.Errorf("Veeam data missing on the scoped cluster: %+v", e)
	}
	for _, id := range store.veeamReads {
		if id == clusterA {
			t.Errorf("Veeam rows were read for a cluster outside the Veeam scope")
		}
	}
}

func TestCompute_ClusterScopeAndStaleThreshold(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	store := newEstate(now)
	// Make cluster A's container 30 h old: stale under the default, recent
	// under a 48 h report parameter.
	store.snaps[pbs01][2].BackupTime = now.Add(-30 * time.Hour).Unix()

	entries, err := Compute(context.Background(), store, Scope{Cluster: OneCluster(clusterA)}, Options{Now: now, StaleAfter: 48 * time.Hour})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for _, e := range entries {
		if e.ClusterID != clusterA.String() {
			t.Fatalf("entry outside the cluster scope: %+v", e)
		}
	}
	if len(entries) != 8 {
		t.Fatalf("got %d entries for cluster A, want 8", len(entries))
	}
	if e := mustEntry(t, byName(entries), "cluster01/linux01"); e.CoverageStatus != StatusRecent {
		t.Errorf("30 h old guest under a 48 h threshold = %q, want recent", e.CoverageStatus)
	}

	entries, err = Compute(context.Background(), store, Scope{Cluster: OneCluster(clusterA)}, Options{Now: now})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if e := mustEntry(t, byName(entries), "cluster01/linux01"); e.CoverageStatus != StatusStale {
		t.Errorf("30 h old guest under the default threshold = %q, want stale", e.CoverageStatus)
	}
}

func TestCompute_VeeamReadFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)

	// Strict mode, which the report uses, refuses to answer: a compliance
	// document that calls every Veeam-only guest "no backup" because a table
	// was unreadable would be a lie.
	strict := newEstate(now)
	strict.veeamErr = errors.New("veeam tables unavailable")
	if _, err := Compute(context.Background(), strict, Scope{Veeam: allVeeam}, Options{Now: now, VeeamStrict: true}); err == nil {
		t.Error("strict Compute should fail when the Veeam protection read fails")
	}
	strictInfra := newEstate(now)
	strictInfra.infraErr = errors.New("infrastructure table unavailable")
	if _, err := Compute(context.Background(), strictInfra, Scope{Veeam: allVeeam}, Options{Now: now, VeeamStrict: true}); err == nil {
		t.Error("strict Compute should fail when the Veeam infrastructure read fails")
	}

	// The coverage page degrades: PBS answers survive, Veeam-only guests read
	// as unprotected, and the worker is still excluded because the
	// infrastructure read is separate from the protection read.
	store := newEstate(now)
	store.veeamErr = errors.New("veeam tables unavailable")
	entries, err := Compute(context.Background(), store, Scope{Veeam: allVeeam}, Options{Now: now})
	if err != nil {
		t.Fatalf("Compute should degrade rather than fail: %v", err)
	}
	got := byName(entries)
	if e := mustEntry(t, got, "cluster01/dc01"); e.Protection != ProtectionPBS {
		t.Errorf("PBS protection lost when Veeam was unreadable: %+v", e)
	}
	if e := mustEntry(t, got, "cluster01/ca01"); e.Veeam != nil || e.Protection != ProtectionNone {
		t.Errorf("Veeam data invented from a failed read: %+v", e)
	}
	if e := mustEntry(t, got, "cluster01/veeam-worker01"); e.Eligibility != VeeamWorker {
		t.Errorf("worker eligibility lost: %+v", e)
	}

	// Infrastructure failing alone degrades the other way round.
	infra := newEstate(now)
	infra.infraErr = errors.New("infrastructure table unavailable")
	entries, err = Compute(context.Background(), infra, Scope{Veeam: allVeeam}, Options{Now: now})
	if err != nil {
		t.Fatalf("Compute should degrade when only the infrastructure read fails: %v", err)
	}
	got = byName(entries)
	if e := mustEntry(t, got, "cluster01/veeam-worker01"); e.Eligibility != Eligible {
		t.Errorf("worker with unreadable infrastructure = %q, want eligible (degraded)", e.Eligibility)
	}
	if e := mustEntry(t, got, "cluster01/ca01"); e.Protection != ProtectionVeeam {
		t.Errorf("Veeam protection lost when only infrastructure was unreadable: %+v", e)
	}
}

func TestCompute_PBSReadFailuresAreErrors(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)

	// A pool or snapshot listing that fails used to be skipped, which turned a
	// database error into a confident "no backup" for every guest it covered.
	pools := newEstate(now)
	pools.poolsErr = errors.New("storage_pools unavailable")
	if _, err := Compute(context.Background(), pools, Scope{}, Options{Now: now}); err == nil {
		t.Error("a failed storage pool listing must fail the computation")
	}
	snaps := newEstate(now)
	snaps.snapsErr = errors.New("pbs_snapshots unavailable")
	if _, err := Compute(context.Background(), snaps, Scope{}, Options{Now: now}); err == nil {
		t.Error("a failed PBS snapshot listing must fail the computation")
	}
}
