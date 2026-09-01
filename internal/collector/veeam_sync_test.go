package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/veeam"
)

// testVeeamKey is a valid 32-byte hex key, so crypto.Decrypt succeeds and the
// sync reaches the client rather than failing at the credential.
const testVeeamKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeVeeamQueries records what the syncer wrote.
type fakeVeeamQueries struct {
	mu sync.Mutex

	servers []db.VeeamServer

	repositories  []db.UpsertVeeamRepositoryParams
	repoMetrics   []db.InsertVeeamRepositoryMetricParams
	jobs          []db.UpsertVeeamJobParams
	objects       []db.UpsertVeeamBackupObjectParams
	restorePoints []db.UpsertVeeamRestorePointParams
	sessions      []db.UpsertVeeamSessionParams
	unfinished    []uuid.UUID
	deletedSess   []db.DeleteVeeamSessionParams
	platforms     []db.UpsertVeeamPlatformParams
	syncErrors    []db.SetVeeamServerSyncErrorParams
	syncSuccesses []uuid.UUID
	// serverNote is the FINAL value written to last_sync_error, as opposed to
	// the append-only history above.
	serverNote     string
	prunedPoints   []db.PruneVeeamRestorePointsParams
	prunedSessions []db.PruneVeeamSessionsParams
	derived        []uuid.UUID

	staleRepos   []db.DeleteStaleVeeamRepositoriesParams
	staleJobs    []db.DeleteStaleVeeamJobsParams
	staleObjects []db.DeleteStaleVeeamBackupObjectsParams

	// existing* are what the List*ByServer calls return — what the sync
	// compares against when deciding whether an empty listing is suspicious.
	existingObjects []db.VeeamBackupObject
	existingJobs    []db.VeeamJob
	existingRepos   []db.VeeamRepository
	// watermark drives GetVeeamSessionWatermark; zero means pgx.ErrNoRows.
	watermark time.Time
	// sessionsSynced records that SetVeeamSessionsSyncedAt was called, i.e.
	// that a session pass ran to completion.
	sessionsSynced bool

	// Correlation. correlated records the server ids the call was made for,
	// so a test can assert the enrichment ran.
	correlated []uuid.UUID
	// Veeam's own guests on the cluster.
	infrastructure      []db.UpsertVeeamInfrastructureParams
	staleInfrastructure []db.DeleteStaleVeeamInfrastructureParams
	resolvedInfra       []uuid.UUID
	// correlateErr makes CorrelateVeeamBackupObjects fail, to prove a failed
	// enrichment does not fail the pass that produced a good inventory.
	correlateErr error
}

func (q *fakeVeeamQueries) ListActiveVeeamServers(context.Context) ([]db.VeeamServer, error) {
	return q.servers, nil
}

func (q *fakeVeeamQueries) SetVeeamServerSyncCompleted(_ context.Context, arg db.SetVeeamServerSyncCompletedParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.syncSuccesses = append(q.syncSuccesses, arg.ID)
	// The server row is a single value, not a log — tests must assert the
	// FINAL state, which is what let the warning-erased-by-success bug pass a
	// review-clean test suite.
	q.serverNote = arg.LastSyncError
	return nil
}

func (q *fakeVeeamQueries) SetVeeamServerSyncError(_ context.Context, arg db.SetVeeamServerSyncErrorParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.syncErrors = append(q.syncErrors, arg)
	q.serverNote = arg.LastSyncError
	return nil
}

// note is the value last_sync_error actually ends up holding.
func (q *fakeVeeamQueries) note() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.serverNote
}

func (q *fakeVeeamQueries) ListVeeamRepositoriesByServer(context.Context, uuid.UUID) ([]db.VeeamRepository, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.existingRepos, nil
}

func (q *fakeVeeamQueries) CountVeeamJobsByServer(context.Context, uuid.UUID) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return int64(len(q.existingJobs)), nil
}

func (q *fakeVeeamQueries) UpsertVeeamInfrastructure(_ context.Context, arg db.UpsertVeeamInfrastructureParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.infrastructure = append(q.infrastructure, arg)
	return nil
}

func (q *fakeVeeamQueries) DeleteStaleVeeamInfrastructure(_ context.Context, arg db.DeleteStaleVeeamInfrastructureParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.staleInfrastructure = append(q.staleInfrastructure, arg)
	return nil
}

func (q *fakeVeeamQueries) ResolveVeeamInfrastructureGuests(_ context.Context, serverID uuid.UUID) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.resolvedInfra = append(q.resolvedInfra, serverID)
	return 0, nil
}

func (q *fakeVeeamQueries) CorrelateVeeamBackupObjects(_ context.Context, serverID uuid.UUID) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.correlated = append(q.correlated, serverID)
	return 0, q.correlateErr
}

func (q *fakeVeeamQueries) UpsertVeeamPlatform(_ context.Context, arg db.UpsertVeeamPlatformParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.platforms = append(q.platforms, arg)
	return nil
}

func (q *fakeVeeamQueries) UpsertVeeamRepository(_ context.Context, arg db.UpsertVeeamRepositoryParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.repositories = append(q.repositories, arg)
	return nil
}

func (q *fakeVeeamQueries) DeleteStaleVeeamRepositories(_ context.Context, arg db.DeleteStaleVeeamRepositoriesParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.staleRepos = append(q.staleRepos, arg)
	return nil
}

func (q *fakeVeeamQueries) InsertVeeamRepositoryMetric(_ context.Context, arg db.InsertVeeamRepositoryMetricParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.repoMetrics = append(q.repoMetrics, arg)
	return nil
}

func (q *fakeVeeamQueries) UpsertVeeamJob(_ context.Context, arg db.UpsertVeeamJobParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, arg)
	return nil
}

func (q *fakeVeeamQueries) DeleteStaleVeeamJobs(_ context.Context, arg db.DeleteStaleVeeamJobsParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.staleJobs = append(q.staleJobs, arg)
	return nil
}

func (q *fakeVeeamQueries) DeriveVeeamJobPlatforms(_ context.Context, id uuid.UUID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.derived = append(q.derived, id)
	return nil
}

func (q *fakeVeeamQueries) ListVeeamBackupObjectsByServer(context.Context, uuid.UUID) ([]db.VeeamBackupObject, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.existingObjects, nil
}

func (q *fakeVeeamQueries) UpsertVeeamBackupObject(_ context.Context, arg db.UpsertVeeamBackupObjectParams) (db.VeeamBackupObject, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.objects = append(q.objects, arg)
	// A stable surrogate id derived from the Veeam object id, so a test can
	// tell which row a restore point or touch refers to.
	return db.VeeamBackupObject{
		ID:                 uuid.NewSHA1(uuid.Nil, arg.VeeamObjectID[:]),
		VeeamObjectID:      arg.VeeamObjectID,
		RestorePointsCount: arg.RestorePointsCount,
	}, nil
}

func (q *fakeVeeamQueries) DeleteStaleVeeamBackupObjects(_ context.Context, arg db.DeleteStaleVeeamBackupObjectsParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.staleObjects = append(q.staleObjects, arg)
	return nil
}

func (q *fakeVeeamQueries) UpsertVeeamRestorePoint(_ context.Context, arg db.UpsertVeeamRestorePointParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.restorePoints = append(q.restorePoints, arg)
	return nil
}

func (q *fakeVeeamQueries) PruneVeeamRestorePoints(_ context.Context, arg db.PruneVeeamRestorePointsParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.prunedPoints = append(q.prunedPoints, arg)
	return nil
}

func (q *fakeVeeamQueries) GetVeeamSessionWatermark(context.Context, uuid.UUID) (time.Time, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.watermark.IsZero() {
		return time.Time{}, pgx.ErrNoRows
	}
	return q.watermark, nil
}

// sessionsSyncedAt records that the session pass completed, which is what
// tells sessionWatermark "a poll has run" as distinct from "the table has
// rows". Job control writes session rows of its own, so the two are no longer
// the same question.
func (q *fakeVeeamQueries) SetVeeamSessionsSyncedAt(context.Context, uuid.UUID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sessionsSynced = true
	return nil
}

func (q *fakeVeeamQueries) UpsertVeeamSession(_ context.Context, arg db.UpsertVeeamSessionParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sessions = append(q.sessions, arg)
	return nil
}

func (q *fakeVeeamQueries) ListVeeamUnfinishedSessions(
	_ context.Context, _ db.ListVeeamUnfinishedSessionsParams,
) ([]uuid.UUID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]uuid.UUID(nil), q.unfinished...), nil
}

func (q *fakeVeeamQueries) DeleteVeeamSession(_ context.Context, arg db.DeleteVeeamSessionParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.deletedSess = append(q.deletedSess, arg)
	return nil
}

func (q *fakeVeeamQueries) PruneVeeamSessions(_ context.Context, arg db.PruneVeeamSessionsParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.prunedSessions = append(q.prunedSessions, arg)
	return nil
}

// fakeVeeamClient is a scriptable VeeamClient.
type fakeVeeamClient struct {
	mu sync.Mutex

	repositories []veeam.Repository
	jobs         []veeam.JobState
	objects      []veeam.BackupObject
	points       map[string][]veeam.RestorePoint
	sessions     []veeam.Session
	license      *veeam.License

	// byID answers the single-session re-read; sessionErrByID overrides it
	// with a failure for one id.
	byID           map[string]veeam.Session
	sessionErrByID map[string]error
	sessionGets    []string

	reposErr    error
	jobsErr     error
	objectsErr  error
	sessionsErr error
	pointsErr   map[string]error

	// pointCalls records which object ids had their restore points fetched.
	pointCalls []string
	// inFlight/peak track how many listings overlap, so a test can tell a
	// bounded fan-out from a serial loop.
	inFlight int
	peak     int
	// sessionsSince records the watermark each poll asked for.
	sessionsSince []time.Time
	logouts       int

	// Veeam's own guests on the cluster.
	proxies    []veeam.ProxyState
	managed    []veeam.ManagedServer
	proxiesErr error
	managedErr error
}

func (c *fakeVeeamClient) Repositories(context.Context) ([]veeam.Repository, error) {
	return c.repositories, c.reposErr
}
func (c *fakeVeeamClient) JobStates(context.Context) ([]veeam.JobState, error) {
	return c.jobs, c.jobsErr
}
func (c *fakeVeeamClient) BackupObjects(context.Context) ([]veeam.BackupObject, error) {
	return c.objects, c.objectsErr
}

func (c *fakeVeeamClient) RestorePointsForObject(_ context.Context, objectID string) ([]veeam.RestorePoint, error) {
	c.mu.Lock()
	c.pointCalls = append(c.pointCalls, objectID)
	c.inFlight++
	c.peak = max(c.peak, c.inFlight)
	err, failing := c.pointsErr[objectID]
	points := c.points[objectID]
	c.mu.Unlock()

	// Long enough for the workers to genuinely overlap; without it a fast
	// fake finishes each call before the next starts and peak is always 1.
	time.Sleep(2 * time.Millisecond)

	c.mu.Lock()
	c.inFlight--
	c.mu.Unlock()

	if failing {
		return nil, err
	}
	return points, nil
}

func (c *fakeVeeamClient) maxConcurrent() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peak
}

func (c *fakeVeeamClient) ProxyStates(context.Context) ([]veeam.ProxyState, error) {
	return c.proxies, c.proxiesErr
}

func (c *fakeVeeamClient) ManagedServers(context.Context) ([]veeam.ManagedServer, error) {
	return c.managed, c.managedErr
}

func (c *fakeVeeamClient) Sessions(_ context.Context, since time.Time, _ int) ([]veeam.Session, error) {
	c.mu.Lock()
	c.sessionsSince = append(c.sessionsSince, since)
	c.mu.Unlock()
	return c.sessions, c.sessionsErr
}

func (c *fakeVeeamClient) Session(_ context.Context, sessionID string) (*veeam.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionGets = append(c.sessionGets, sessionID)
	if err, failing := c.sessionErrByID[sessionID]; failing {
		return nil, err
	}
	s, ok := c.byID[sessionID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", veeam.ErrSessionNotFound, sessionID)
	}
	return &s, nil
}

func (c *fakeVeeamClient) License(context.Context) (*veeam.License, error) {
	if c.license == nil {
		return nil, errors.New("no licence")
	}
	return c.license, nil
}

func (c *fakeVeeamClient) Logout(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logouts++
	return nil
}

func newVeeamTestSyncer(t *testing.T, q *fakeVeeamQueries, c *fakeVeeamClient) *VeeamSyncer {
	t.Helper()
	s := NewVeeamSyncer(q, testVeeamKey, VeeamSyncConfig{
		RestorePointRetention: 30 * 24 * time.Hour,
		SessionRetention:      30 * 24 * time.Hour,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.clientFactory = func(veeam.Config) (VeeamClient, error) { return c, nil }
	return s
}

func testVeeamServer(t *testing.T) db.VeeamServer {
	t.Helper()
	encrypted, err := crypto.Encrypt("correct-horse", testVeeamKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return db.VeeamServer{
		ID:                uuid.New(),
		Name:              "vbr01",
		BaseUrl:           "https://vbr.example.com:9419",
		Username:          `ad\jdoe`,
		PasswordEncrypted: encrypted,
		ApiRevision:       "1.3-rev2",
		Enabled:           true,
	}
}

const (
	testPlatformID = "01208ee8-47fe-4ea8-8727-5115874da1ad"
	testObjectID   = "3aad74b4-8013-4b41-b266-d21b6d88cc21"
	testJobID      = "3953c24f-bbe6-41fc-ae2f-a34e25bfd614"
)

func TestVeeamSync_Inventory(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		repositories: []veeam.Repository{
			{ID: uuid.NewString(), Name: "repo-nas-01", Type: "WinLocal", CapacityGB: 100, FreeGB: 40, UsedSpaceGB: 60, IsOnline: true},
		},
		jobs: []veeam.JobState{
			{ID: testJobID, Name: "Onsite_Daily", Type: veeam.ProxmoxJobType, Status: "Stopped", LastResult: "Success"},
			// A vSphere job on the same server must not be stored — Nexara
			// has nothing to say about it.
			{ID: uuid.NewString(), Name: "VMware_Daily", Type: "BackupJob"},
		},
		objects: []veeam.BackupObject{
			{ID: testObjectID, ObjectID: "316e531d-55c0-4fef-adc2-f1bb9c4e1873", PlatformName: "Proxmox",
				PlatformID: testPlatformID, Name: "web01", Type: "VM", RestorePointsCount: 2, Size: 1024},
			{ID: uuid.NewString(), PlatformName: "VMware", Name: "esx-guest"},
		},
		points: map[string][]veeam.RestorePoint{
			testObjectID: {
				{ID: uuid.NewString(), Name: "web01", Type: "Increment", MalwareStatus: "Clean",
					CreationTime:      veeam.Timestamp{Time: time.Now().Add(-2 * time.Hour)},
					AllowedOperations: []string{"StartFlrRestore"}},
			},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	if len(q.repositories) != 1 {
		t.Fatalf("repositories upserted = %d, want 1", len(q.repositories))
	}
	// Float GB must reach the database as bytes.
	if got, want := q.repositories[0].CapacityBytes, int64(100)*1024*1024*1024; got != want {
		t.Errorf("CapacityBytes = %d, want %d", got, want)
	}
	if len(q.repoMetrics) != 1 {
		t.Errorf("capacity samples = %d, want 1", len(q.repoMetrics))
	}

	// Only the Proxmox job and the Proxmox object are stored.
	if len(q.jobs) != 1 || q.jobs[0].Name != "Onsite_Daily" {
		t.Errorf("jobs = %+v, want only the Proxmox job", q.jobs)
	}
	if len(q.objects) != 1 || q.objects[0].Name != "web01" {
		t.Errorf("objects = %+v, want only the Proxmox object", q.objects)
	}
	if q.objects[0].SmbiosUuid != "316e531d-55c0-4fef-adc2-f1bb9c4e1873" {
		t.Errorf("SmbiosUuid = %q — objectId is what makes correlation deterministic", q.objects[0].SmbiosUuid)
	}

	if len(q.restorePoints) != 1 {
		t.Fatalf("restore points = %d, want 1", len(q.restorePoints))
	}
	if !q.restorePoints[0].SupportsFlr {
		t.Error("SupportsFlr false despite StartFlrRestore being allowed")
	}

	// A successful pass sweeps stale rows and clears the error.
	if len(q.staleRepos) != 1 || len(q.staleJobs) != 1 || len(q.staleObjects) != 1 {
		t.Error("a successful pass should sweep each section's stale rows")
	}
	if len(q.syncSuccesses) != 1 {
		t.Errorf("syncSuccesses = %+v, want one", q.syncSuccesses)
	}
	if len(q.syncErrors) != 0 {
		t.Errorf("syncErrors = %+v, want none on a clean pass", q.syncErrors)
	}
	if c.logouts != 1 {
		t.Errorf("logouts = %d, want 1 — the token should not be left to expire", c.logouts)
	}
}

// A section that could not be read must leave its rows alone. Sweeping on a
// failed fetch would turn a Veeam outage into "everything was deleted".
func TestVeeamSync_PartialFailureDoesNotSweep(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		repositories: []veeam.Repository{{ID: uuid.NewString(), Name: "repo", CapacityGB: 10}},
		jobsErr:      errors.New("upstream exploded"),
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	if len(q.staleRepos) != 1 {
		t.Error("repositories were read successfully and should still be swept")
	}
	if len(q.staleJobs) != 0 {
		t.Error("jobs failed to read but their rows were swept anyway")
	}
	if len(q.staleObjects) != 0 {
		t.Error("backup objects were never reached but their rows were swept")
	}
	// The pass reports the failure rather than silently half-succeeding —
	// and does NOT stamp last_sync_at, which SetVeeamServerSyncSuccess owns.
	if len(q.syncErrors) != 1 || q.syncErrors[0].LastSyncError == "" {
		t.Errorf("syncErrors = %+v, want the failure recorded", q.syncErrors)
	}
	if len(q.syncSuccesses) != 0 {
		t.Errorf("a failed pass stamped last_sync_at: %+v", q.syncSuccesses)
	}
	// And nothing is pruned on the back of a failed pass.
	if len(q.prunedPoints) != 0 {
		t.Error("restore points were pruned after a failed pass")
	}
}

// Restore points are fetched for EVERY object, every pass. The count-based
// skip this replaced looked free and was not: a job that keeps N points
// saturates at N, the count stops moving, and the skip becomes permanent — so
// Nexara would freeze on the day the ceiling was hit and never learn another
// restore point.
func TestVeeamSync_AlwaysFetchesRestorePoints(t *testing.T) {
	server := testVeeamServer(t)
	objectUUID := uuid.MustParse(testObjectID)

	q := &fakeVeeamQueries{
		servers: []db.VeeamServer{server},
		// Already on record with the SAME count Veeam reports — the exact
		// steady state of a count-retention job.
		existingObjects: []db.VeeamBackupObject{
			{VeeamObjectID: objectUUID, RestorePointsCount: 14},
		},
	}
	c := &fakeVeeamClient{
		objects: []veeam.BackupObject{
			{ID: testObjectID, PlatformName: "Proxmox", PlatformID: testPlatformID,
				Name: "web01", RestorePointsCount: 14},
		},
		points: map[string][]veeam.RestorePoint{
			testObjectID: {{ID: uuid.NewString(), Name: "web01",
				CreationTime: veeam.Timestamp{Time: time.Now()}}},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	if len(c.pointCalls) != 1 {
		t.Errorf("restore-point fetches = %d, want 1 — an unchanged count must not skip the fetch", len(c.pointCalls))
	}
	if len(q.restorePoints) != 1 {
		t.Errorf("restore points stored = %d, want 1", len(q.restorePoints))
	}
}

// GET /backupObjects returns one row per (guest × backup) and REPEATS the
// object id across them — 27 rows carried 18 distinct ids on the live server.
// The rows fold to one per guest, and the per-backup restore-point counts sum,
// because /backupObjects/{id}/restorePoints returns the guest's points across
// every backup. Measured on the lab: a guest whose rows read [3, 17, 9] has 29
// points, so keeping any single row's count would disagree with the points
// stored beside it.
func TestVeeamSync_FoldsDuplicateObjectRowsPerGuest(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}

	const size = int64(53687091200)
	dup := func(backupID string, count int, failed bool) veeam.BackupObject {
		return veeam.BackupObject{
			ID: testObjectID, ObjectID: "smbios-uuid", PlatformName: "Proxmox",
			PlatformID: testPlatformID, Name: "automation", Type: "VM",
			BackupID: backupID, RestorePointsCount: count, Size: size,
			LastRunFailed: failed,
		}
	}
	c := &fakeVeeamClient{
		objects: []veeam.BackupObject{
			dup(uuid.NewString(), 3, false),
			dup(uuid.NewString(), 17, true),
			dup(uuid.NewString(), 9, false),
		},
		points: map[string][]veeam.RestorePoint{
			testObjectID: {{ID: uuid.NewString(), CreationTime: veeam.Timestamp{Time: time.Now()}}},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	if len(q.objects) != 1 {
		t.Fatalf("upserts = %d, want one row per guest", len(q.objects))
	}
	if got := q.objects[0].RestorePointsCount; got != 29 {
		t.Errorf("RestorePointsCount = %d, want the sum across backups (29)", got)
	}
	if got := q.objects[0].SizeBytes; got != size {
		t.Errorf("SizeBytes = %d, want the guest's own size %d — summing would multiply it by its backup count", got, size)
	}
	if !q.objects[0].LastRunFailed {
		t.Error("LastRunFailed = false; a failure in any of a guest's backups is worth surfacing")
	}
	// And the fan-out runs once per guest, not once per listing row.
	if len(c.pointCalls) != 1 {
		t.Errorf("restore-point fetches = %d, want 1 — duplicates re-fetch the same guest", len(c.pointCalls))
	}
}

// A successful-but-EMPTY listing must not delete everything. The VBR REST
// service answers before its backup service has loaded its catalog after a
// restart, so an empty read is a real transient state — and acting on it would
// cascade away every restore point and then report the server healthy.
func TestVeeamSync_EmptyListingDoesNotWipeInventory(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{
		servers: []db.VeeamServer{server},
		existingObjects: []db.VeeamBackupObject{
			{VeeamObjectID: uuid.MustParse(testObjectID), RestorePointsCount: 3},
		},
		existingJobs:  []db.VeeamJob{{VeeamID: uuid.MustParse(testJobID)}},
		existingRepos: []db.VeeamRepository{{VeeamID: uuid.New()}},
	}
	// Everything reads fine and returns nothing.
	c := &fakeVeeamClient{}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	if len(q.staleObjects) != 0 {
		t.Error("an empty backup-object listing swept every object")
	}
	if len(q.staleJobs) != 0 {
		t.Error("an empty job listing swept every job")
	}
	if len(q.staleRepos) != 0 {
		t.Error("an empty repository listing swept every repository")
	}
	// And it SURVIVES the pass's own success. Asserting an append here is
	// what let the original bug through: the warning was written and then
	// cleared moments later by the same pass completing.
	if q.note() == "" {
		t.Error("the refusal to prune was erased by the pass that raised it")
	}

	// The restore-point prune is gated on the same verdict. Without that, a
	// short retention window deletes every guest's recovery history while the
	// guard congratulates itself for keeping the objects.
	if len(q.prunedPoints) != 0 {
		t.Error("restore points were pruned on a pass that refused to sweep")
	}

	// Correlation is NOT gated on it. It reads the object set as it stands —
	// including the one this pass deliberately kept — and has nothing to do
	// with restore-point retention. Inheriting the guard would strand a
	// freshly mapped platform uncorrelated for as long as a VBR server took
	// to finish loading its catalog after a restart.
	if len(q.correlated) != 1 {
		t.Errorf("correlated = %v, want the pass to still correlate", q.correlated)
	}
}

// A server that genuinely has nothing yet must still converge — the guard is
// "do not delete on an empty read when rows exist", not "never sweep".
func TestVeeamSync_EmptyListingSweepsWhenNothingWasStored(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	syncer := newVeeamTestSyncer(t, q, &fakeVeeamClient{})

	syncer.SyncInventory(context.Background())

	if len(q.staleObjects) != 1 || len(q.staleJobs) != 1 || len(q.staleRepos) != 1 {
		t.Error("a server with no rows on either side should still sweep")
	}
	if q.note() != "" {
		t.Errorf("an empty server reported %q", q.note())
	}
}

// One object's restore points failing must not abort the pass — otherwise a
// single bad guest stalls every other guest's data.
func TestVeeamSync_OneObjectsRestorePointsFailingDoesNotAbortThePass(t *testing.T) {
	server := testVeeamServer(t)
	badID := uuid.NewString()
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		objects: []veeam.BackupObject{
			{ID: badID, PlatformName: "Proxmox", Name: "broken", RestorePointsCount: 1},
			{ID: testObjectID, PlatformName: "Proxmox", Name: "web01", RestorePointsCount: 1},
		},
		pointsErr: map[string]error{badID: errors.New("boom")},
		points: map[string][]veeam.RestorePoint{
			testObjectID: {{ID: uuid.NewString(), Name: "web01", CreationTime: veeam.Timestamp{Time: time.Now()}}},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	if len(q.restorePoints) != 1 {
		t.Errorf("restore points stored = %d, want the healthy object's point", len(q.restorePoints))
	}
	// The pass as a whole still succeeded.
	if len(q.syncSuccesses) != 1 {
		t.Errorf("syncSuccesses = %+v, want success", q.syncSuccesses)
	}
}

func TestVeeamSync_SessionsUseWatermarkWithOverlap(t *testing.T) {
	// sessions_synced_at set: this is a server whose session pass has run
	// before, which is what allows the watermark to narrow at all.
	server := testVeeamServer(t)
	server.SessionsSyncedAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}
	mark := time.Now().Add(-1 * time.Hour)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}, watermark: mark}
	c := &fakeVeeamClient{}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(c.sessionsSince) != 1 {
		t.Fatalf("session polls = %d, want 1", len(c.sessionsSince))
	}
	// The overlap is what closes the gap around a session whose server-side
	// timestamp lands fractionally before the newest row already stored.
	want := mark.Add(-veeamSessionOverlap)
	if got := c.sessionsSince[0]; !got.Equal(want) {
		t.Errorf("createdAfterFilter = %v, want the watermark less the overlap (%v)", got, want)
	}
}

// An empty table must not ask for the server's entire history — a real VBR
// holds tens of thousands of sessions.
func TestVeeamSync_FirstSyncBoundsSessionHistory(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(c.sessionsSince) != 1 {
		t.Fatalf("session polls = %d, want 1", len(c.sessionsSince))
	}
	since := c.sessionsSince[0]
	if since.IsZero() {
		t.Fatal("first sync asked for the whole history")
	}
	expected := time.Now().Add(-30 * 24 * time.Hour)
	if since.Before(expected.Add(-time.Minute)) || since.After(expected.Add(time.Minute)) {
		t.Errorf("first-sync watermark = %v, want the start of the retention window (~%v)", since, expected)
	}
}

// The trap job control introduced: a session row can now exist before any
// session poll has succeeded, because starting a job writes one. Treating that
// row as the watermark anchors `since` to minutes ago and — since a watermark
// only ever moves forward — skips the retention window's backfill for good.
func TestVeeamSync_FirstSyncIgnoresASessionRowNoPollWrote(t *testing.T) {
	// sessions_synced_at is NULL: no session pass has ever completed. The
	// watermark is non-zero anyway, standing in for the row job control wrote
	// when an operator started a job.
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{
		servers:   []db.VeeamServer{server},
		watermark: time.Now().Add(-2 * time.Minute),
	}
	c := &fakeVeeamClient{}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(c.sessionsSince) != 1 {
		t.Fatalf("session polls = %d, want 1", len(c.sessionsSince))
	}
	expected := time.Now().Add(-30 * 24 * time.Hour)
	if since := c.sessionsSince[0]; since.After(expected.Add(time.Minute)) {
		t.Errorf("first-sync watermark = %v, want the start of the retention window (~%v) — "+
			"a control-written session row must not narrow the backfill", since, expected)
	}
}

// And once the pass has completed, the stamp is what lets it narrow.
func TestVeeamSync_StampsTheSessionPassOnSuccess(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.sessionsSynced {
		t.Error("a successful session poll did not stamp sessions_synced_at; every later poll would re-read the whole retention window")
	}
}

func TestVeeamSync_StoresOnlyProxmoxSessions(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		sessions: []veeam.Session{
			{ID: uuid.NewString(), SessionType: veeam.PlatformBackupSessionType, PlatformName: "Proxmox",
				PlatformID: testPlatformID, JobID: testJobID, Name: "Onsite_Daily",
				CreationTime: veeam.Timestamp{Time: time.Now()}},
			// Same session type, different platform — typeFilter cannot
			// exclude this, so the client-side platform check must.
			{ID: uuid.NewString(), SessionType: veeam.PlatformBackupSessionType, PlatformName: "Nutanix",
				CreationTime: veeam.Timestamp{Time: time.Now()}},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(q.sessions) != 1 || q.sessions[0].Name != "Onsite_Daily" {
		t.Errorf("sessions stored = %+v, want only the Proxmox run", q.sessions)
	}
	// Sessions are the only bridge from a job to its platform, so derivation
	// runs as soon as they land.
	if len(q.derived) != 1 {
		t.Errorf("job platform derivation ran %d times, want 1", len(q.derived))
	}
	if len(q.prunedSessions) != 1 {
		t.Errorf("session prune ran %d times, want 1", len(q.prunedSessions))
	}
}

// Veeam uses the all-zero UUID for "no related session". Storing it literally
// would mint an identifier every other unrelated row also matches.
func TestOptionalUUID(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		valid bool
	}{
		{"real id", testPlatformID, true},
		{"empty", "", false},
		{"all-zero sentinel", "00000000-0000-0000-0000-000000000000", false},
		{"garbage", "not-a-uuid", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := optionalUUID(tc.in); got.Valid != tc.valid {
				t.Errorf("optionalUUID(%q).Valid = %v, want %v", tc.in, got.Valid, tc.valid)
			}
		})
	}
}

func TestOptionalTimestamp(t *testing.T) {
	if got := optionalTimestamp(time.Time{}); got.Valid {
		t.Error("the zero time should store as NULL, not the year 1")
	}
	now := time.Now()
	got := optionalTimestamp(now)
	if !got.Valid || !got.Time.Equal(now) {
		t.Errorf("optionalTimestamp(%v) = %+v", now, got)
	}
}

// A failing server must back off, and must not be retried on the very next
// tick — a 60s session poll against an unreachable VBR is a lot of pointless
// connection attempts.
func TestVeeamSync_BacksOffAfterFailure(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{sessionsErr: errors.New("unreachable")}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())
	if len(c.sessionsSince) != 1 {
		t.Fatalf("first poll did not happen")
	}

	// The next tick is inside the backoff window and must be skipped.
	syncer.SyncSessions(context.Background())
	if len(c.sessionsSince) != 1 {
		t.Errorf("polls = %d, want the second tick skipped by backoff", len(c.sessionsSince))
	}

	// A success clears it.
	syncer.backoffMu.Lock()
	delete(syncer.backoff, backoffKey(server.ID, veeamPassSessions))
	syncer.backoffMu.Unlock()
	c.sessionsErr = nil

	syncer.SyncSessions(context.Background())
	if len(c.sessionsSince) != 2 {
		t.Errorf("polls = %d, want the retry to happen once backoff cleared", len(c.sessionsSince))
	}
	syncer.backoffMu.Lock()
	_, stillBackedOff := syncer.backoff[backoffKey(server.ID, veeamPassSessions)]
	syncer.backoffMu.Unlock()
	if stillBackedOff {
		t.Error("a successful pass should clear the backoff state")
	}
}

// The two passes keep SEPARATE backoff state, and only the inventory pass
// writes the server row's status.
//
// Sharing either made a persistently broken inventory pass invisible: the
// healthy 60s session poll cleared its backoff every minute so it never
// actually backed off, and cleared last_sync_error every minute so the UI
// error flashed on and off in a loop.
func TestVeeamSync_PassesDoNotClearEachOther(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	// Inventory is broken; sessions are fine.
	c := &fakeVeeamClient{objectsErr: errors.New("backupObjects is down")}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())
	if q.note() == "" {
		t.Fatal("the inventory failure was not recorded")
	}

	// A healthy session poll must not clear it.
	syncer.SyncSessions(context.Background())

	if q.note() == "" {
		t.Error("a healthy session poll erased the inventory pass's error")
	}
	if len(q.syncSuccesses) != 0 {
		t.Error("a session poll stamped last_sync_at, which only a successful inventory pass may do")
	}
	syncer.backoffMu.Lock()
	_, invBackedOff := syncer.backoff[backoffKey(server.ID, veeamPassInventory)]
	syncer.backoffMu.Unlock()
	if !invBackedOff {
		t.Error("a successful session poll cleared the inventory pass's backoff")
	}
}

// One transient failure mid-batch must not leave a permanent hole. Sessions
// arrive newest-first, so they are stored oldest-first: whatever lands is a
// contiguous run from the oldest, and the watermark only advances over rows
// that actually made it.
func TestVeeamSync_SessionsStoredOldestFirst(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}

	base := time.Now().Add(-3 * time.Hour)
	newest := veeam.Session{
		ID: uuid.NewString(), SessionType: veeam.PlatformBackupSessionType,
		PlatformName: "Proxmox", Name: "newest",
		CreationTime: veeam.Timestamp{Time: base.Add(2 * time.Hour)},
	}
	middle := veeam.Session{
		ID: uuid.NewString(), SessionType: veeam.PlatformBackupSessionType,
		PlatformName: "Proxmox", Name: "middle",
		CreationTime: veeam.Timestamp{Time: base.Add(time.Hour)},
	}
	oldest := veeam.Session{
		ID: uuid.NewString(), SessionType: veeam.PlatformBackupSessionType,
		PlatformName: "Proxmox", Name: "oldest",
		CreationTime: veeam.Timestamp{Time: base},
	}
	// As the API returns them.
	c := &fakeVeeamClient{sessions: []veeam.Session{newest, middle, oldest}}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(q.sessions) != 3 {
		t.Fatalf("stored %d sessions, want 3", len(q.sessions))
	}
	want := []string{"oldest", "middle", "newest"}
	for i, name := range want {
		if q.sessions[i].Name != name {
			t.Errorf("session[%d] = %q, want %q — storing newest-first would advance the watermark past rows a mid-batch failure never stored",
				i, q.sessions[i].Name, name)
		}
	}
}

// A session whose timestamp did not parse cannot be stored: the column is NOT
// NULL, the zero time would be pruned on this very pass, and it would corrupt
// the watermark.
func TestVeeamSync_SkipsSessionsWithNoParseableTime(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		sessions: []veeam.Session{
			{ID: uuid.NewString(), SessionType: veeam.PlatformBackupSessionType,
				PlatformName: "Proxmox", Name: "unparseable"},
			{ID: uuid.NewString(), SessionType: veeam.PlatformBackupSessionType,
				PlatformName: "Proxmox", Name: "good",
				CreationTime: veeam.Timestamp{Time: time.Now()}},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(q.sessions) != 1 || q.sessions[0].Name != "good" {
		t.Errorf("stored %+v, want only the session with a usable timestamp",
			q.sessions)
	}
}

// The bug: a job painted "Running" for hours after its run finished.
//
// The poll filters on CREATION time, but a session's STATE keeps changing long
// after it is created — so once a newer run pushes the watermark past a run
// that is still in flight, no later poll ever asks for that run again. Its
// state froze at "Working", ListVeeamJobsByServer derived a live run from it,
// and the job showed Running with a Stop button until the 30-day prune.
func TestVeeamSync_ReReadsAnInFlightSessionThePollHasPassed(t *testing.T) {
	server := testVeeamServer(t)
	server.SessionsSyncedAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true}

	// Stored as "Working" yesterday, and now well behind the watermark.
	stuck := uuid.New()
	started := time.Now().Add(-20 * time.Hour)
	q := &fakeVeeamQueries{
		servers:    []db.VeeamServer{server},
		watermark:  time.Now().Add(-time.Hour),
		unfinished: []uuid.UUID{stuck},
	}

	// The poll returns nothing — the run is far outside its window. Veeam
	// still knows the session, and reports it finished.
	ended := started.Add(2 * time.Hour)
	c := &fakeVeeamClient{
		byID: map[string]veeam.Session{
			stuck.String(): {
				ID: stuck.String(), SessionType: veeam.PlatformBackupSessionType,
				PlatformName: "Proxmox", Name: "Onsite_Daily_Offsite_Linux",
				State: "Stopped", CreationTime: veeam.Timestamp{Time: started},
				EndTime: &veeam.Timestamp{Time: ended},
				Result:  veeam.SessionResult{Result: "Success"},
			},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(c.sessionGets) != 1 || c.sessionGets[0] != stuck.String() {
		t.Fatalf("single-session reads = %v, want the one stuck run (%s)",
			c.sessionGets, stuck)
	}
	if len(q.sessions) != 1 {
		t.Fatalf("stored %d sessions, want the re-read one", len(q.sessions))
	}
	// "Stopped" is the whole point: it is the only state the live-run LATERAL
	// treats as terminal, so anything else leaves the job painted Running.
	if got := q.sessions[0].State; got != "Stopped" {
		t.Errorf("state = %q, want %q — a job stays Running until its session reaches a terminal state",
			got, "Stopped")
	}
	if !q.sessions[0].EndTime.Valid {
		t.Error("end_time is still NULL after the re-read")
	}
	if len(q.deletedSess) != 0 {
		t.Errorf("deleted %v — a session Veeam still has must be converged, not dropped", q.deletedSess)
	}
}

// A run the poll just refreshed is already current; spending a request per
// session to learn that would make every pass cost one call per running job
// for nothing.
func TestVeeamSync_DoesNotReReadASessionThePollJustStored(t *testing.T) {
	server := testVeeamServer(t)
	live := uuid.New()
	q := &fakeVeeamQueries{
		servers:    []db.VeeamServer{server},
		unfinished: []uuid.UUID{live},
	}
	c := &fakeVeeamClient{
		sessions: []veeam.Session{{
			ID: live.String(), SessionType: veeam.PlatformBackupSessionType,
			PlatformName: "Proxmox", Name: "in-flight", State: "Working",
			CreationTime: veeam.Timestamp{Time: time.Now().Add(-5 * time.Minute)},
		}},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(c.sessionGets) != 0 {
		t.Errorf("re-read %v, want none — the poll had already stored it this pass", c.sessionGets)
	}
	if len(q.sessions) != 1 {
		t.Fatalf("stored %d sessions, want 1", len(q.sessions))
	}
}

// A 404 means Veeam has forgotten the session. It can never reach a terminal
// state now, so leaving the row is precisely what pins its job to "Running"
// forever — the mirror drops it, as the sweeps do for every other object.
func TestVeeamSync_DropsAnUnfinishedSessionVeeamNoLongerHas(t *testing.T) {
	server := testVeeamServer(t)
	gone := uuid.New()
	q := &fakeVeeamQueries{
		servers:    []db.VeeamServer{server},
		unfinished: []uuid.UUID{gone},
	}
	// byID is empty, so the fake answers 404.
	c := &fakeVeeamClient{}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(q.deletedSess) != 1 || q.deletedSess[0].VeeamID != gone {
		t.Fatalf("deleted %v, want the vanished session (%s)", q.deletedSess, gone)
	}
	if q.deletedSess[0].VeeamServerID != server.ID {
		t.Errorf("deleted scoped to server %s, want %s — an unscoped delete reaches another server's rows",
			q.deletedSess[0].VeeamServerID, server.ID)
	}
}

// Every failure that is NOT a definite 404 is transient. Deleting a run's
// history over a momentary error is not recoverable, so the row waits for the
// next pass.
func TestVeeamSync_KeepsAnUnfinishedSessionWhenTheReReadFails(t *testing.T) {
	server := testVeeamServer(t)
	unreadable := uuid.New()
	q := &fakeVeeamQueries{
		servers:    []db.VeeamServer{server},
		unfinished: []uuid.UUID{unreadable},
	}
	c := &fakeVeeamClient{
		sessionErrByID: map[string]error{
			unreadable.String(): &veeam.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(q.deletedSess) != 0 {
		t.Errorf("deleted %v on a transient failure — the run's history is gone for good", q.deletedSess)
	}
	// The pass itself still succeeded: the poll worked, and losing its
	// watermark advance would re-fetch the whole window every pass for as long
	// as this one session stayed unreadable.
	if !q.sessionsSynced {
		t.Error("an unreadable session failed the whole pass, which stalls the watermark")
	}
}

// The near-miss this fix nearly shipped: a 404 does NOT reach the collector
// only from the session endpoint. A failed token grant surfaces the token
// endpoint's own *APIError verbatim, so a restarting VBR answering 404 on
// /oauth2/token used to look byte-identical to "this run is gone" — and,
// because a failed grant leaves the cached token untouched, every remaining id
// in the batch failed the same way. One restart would have deleted up to fifty
// rows, including runs in flight at that moment, taking the nexara_stopped
// provenance that suppresses a false veeam_job_failed with them.
func TestVeeamSync_A404FromElsewhereDoesNotDeleteSessions(t *testing.T) {
	server := testVeeamServer(t)
	live := uuid.New()
	q := &fakeVeeamQueries{
		servers:    []db.VeeamServer{server},
		unfinished: []uuid.UUID{live},
	}
	// A 404 *APIError that did NOT come from GET /sessions/{id} — exactly what
	// the token endpoint hands back through a failed re-grant.
	c := &fakeVeeamClient{
		sessionErrByID: map[string]error{
			live.String(): &veeam.APIError{
				StatusCode: http.StatusNotFound, ErrorCode: "NotFound", Message: "Not Found",
			},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(q.deletedSess) != 0 {
		t.Errorf("deleted %v on a 404 the session endpoint never sent — only veeam.ErrSessionNotFound may delete a run",
			q.deletedSess)
	}
}

// A broken credential is a fact about the CLIENT, not about one row: every
// remaining id would fail identically, so the batch is abandoned rather than
// spending fifty doomed round-trips on it.
func TestVeeamSync_AbandonsTheBatchOnAClientWideFailure(t *testing.T) {
	server := testVeeamServer(t)
	first, second := uuid.New(), uuid.New()
	q := &fakeVeeamQueries{
		servers: []db.VeeamServer{server},
		// ListVeeamUnfinishedSessions orders newest first; the fake preserves
		// the order it is given.
		unfinished: []uuid.UUID{first, second},
	}
	c := &fakeVeeamClient{
		sessionErrByID: map[string]error{first.String(): veeam.ErrAuthFailed},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(c.sessionGets) != 1 {
		t.Errorf("re-read %v, want to stop after the first — the credential is broken for every id",
			c.sessionGets)
	}
	if len(q.deletedSess) != 0 {
		t.Errorf("deleted %v while the client was unusable", q.deletedSess)
	}
}

// The opposite of the case above: a per-session failure must NOT block the
// rows behind it. Newest-first ordering means one permanently-failing run
// would otherwise starve every older one forever.
func TestVeeamSync_APerSessionFailureDoesNotBlockTheRest(t *testing.T) {
	server := testVeeamServer(t)
	broken, healthy := uuid.New(), uuid.New()
	q := &fakeVeeamQueries{
		servers:    []db.VeeamServer{server},
		unfinished: []uuid.UUID{broken, healthy},
	}
	c := &fakeVeeamClient{
		sessionErrByID: map[string]error{
			broken.String(): &veeam.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"},
		},
		byID: map[string]veeam.Session{
			healthy.String(): {
				ID: healthy.String(), SessionType: veeam.PlatformBackupSessionType,
				PlatformName: "Proxmox", Name: "behind-the-broken-one", State: "Stopped",
				CreationTime: veeam.Timestamp{Time: time.Now().Add(-3 * time.Hour)},
			},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(q.sessions) != 1 || q.sessions[0].Name != "behind-the-broken-one" {
		t.Errorf("stored %+v, want the run queued behind the failing one", q.sessions)
	}
}

// The same guard the poll applies, for the same reason: creation_time is NOT
// NULL, feeds the watermark, and a zero value would be pruned on this very
// pass. A list envelope decoded as a single session yields exactly this.
func TestVeeamSync_SkipsAReReadSessionWithNoParseableTime(t *testing.T) {
	server := testVeeamServer(t)
	empty := uuid.New()
	q := &fakeVeeamQueries{
		servers:    []db.VeeamServer{server},
		unfinished: []uuid.UUID{empty},
	}
	c := &fakeVeeamClient{
		byID: map[string]veeam.Session{
			empty.String(): {ID: empty.String(), Name: "no-timestamp", State: "Working"},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())

	if len(q.sessions) != 0 {
		t.Errorf("stored %+v, want nothing — a zero creation_time corrupts the watermark", q.sessions)
	}
}

// A session pass that fails forever must stay visible. An earlier fix made
// only the inventory pass write the status column, which stopped the flapping
// but hid a permanently broken session poll entirely — the Runs tab would
// freeze while the server reported success every five minutes.
func TestVeeamSync_SessionFailureIsVisibleOnTheServerRow(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{sessionsErr: errors.New("/sessions is down")}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncSessions(context.Background())
	if q.note() == "" {
		t.Fatal("a failing session poll wrote nothing to the server row")
	}

	// A healthy inventory pass must not erase it — it can only clear its own.
	syncer.SyncInventory(context.Background())
	if q.note() == "" {
		t.Error("a healthy inventory pass erased the session pass's error")
	}
	// But it does stamp last_sync_at, because the inventory genuinely synced.
	if len(q.syncSuccesses) != 1 {
		t.Errorf("syncSuccesses = %+v, want the inventory pass to stamp", q.syncSuccesses)
	}

	// Once sessions recover, the row clears.
	c.sessionsErr = nil
	syncer.backoffMu.Lock()
	delete(syncer.backoff, backoffKey(server.ID, veeamPassSessions))
	syncer.backoffMu.Unlock()

	syncer.SyncSessions(context.Background())
	if q.note() != "" {
		t.Errorf("note = %q after both passes recovered, want empty", q.note())
	}
}

// The fan-out is bounded, not serial: a large install's object count would
// otherwise exceed the whole pass budget and every object past the cut-off
// would silently get no restore points.
func TestVeeamSync_RestorePointFanOutIsBounded(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}

	const objectCount = 40
	objects := make([]veeam.BackupObject, objectCount)
	points := map[string][]veeam.RestorePoint{}
	for i := range objects {
		id := uuid.NewString()
		objects[i] = veeam.BackupObject{
			ID: id, PlatformName: "Proxmox", Name: "guest", RestorePointsCount: 1,
		}
		points[id] = []veeam.RestorePoint{
			{ID: uuid.NewString(), CreationTime: veeam.Timestamp{Time: time.Now()}},
		}
	}
	c := &fakeVeeamClient{objects: objects, points: points}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	if len(c.pointCalls) != objectCount {
		t.Errorf("restore-point fetches = %d, want one per object (%d)", len(c.pointCalls), objectCount)
	}
	if len(q.restorePoints) != objectCount {
		t.Errorf("restore points stored = %d, want %d", len(q.restorePoints), objectCount)
	}
	if n := c.maxConcurrent(); n > veeamRestorePointWorkers {
		t.Errorf("peak concurrency = %d, want at most %d", n, veeamRestorePointWorkers)
	}
	if n := c.maxConcurrent(); n < 2 {
		t.Errorf("peak concurrency = %d — the fan-out is still serial", n)
	}
}

// A restore point with no usable timestamp must be skipped, exactly as a
// session is. creation_time is NOT NULL, and a zero time sorts last in every
// "newest point" query, which makes a protected guest look unprotected.
func TestVeeamSync_SkipsRestorePointsWithNoParseableTime(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		objects: []veeam.BackupObject{
			{ID: testObjectID, PlatformName: "Proxmox", Name: "web01", RestorePointsCount: 2},
		},
		points: map[string][]veeam.RestorePoint{
			testObjectID: {
				{ID: uuid.NewString(), Name: "unparseable"},
				{ID: uuid.NewString(), Name: "good", CreationTime: veeam.Timestamp{Time: time.Now()}},
			},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	if len(q.restorePoints) != 1 || q.restorePoints[0].Name != "good" {
		t.Errorf("stored %+v, want only the point with a usable timestamp", q.restorePoints)
	}
}

func TestVeeamSync_BackoffIsBounded(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	syncer := newVeeamTestSyncer(t, q, &fakeVeeamClient{})

	for range 40 {
		syncer.recordFailure(context.Background(), server, veeamPassInventory, errors.New("still down"))
	}

	syncer.backoffMu.Lock()
	state := syncer.backoff[backoffKey(server.ID, veeamPassInventory)]
	syncer.backoffMu.Unlock()

	// A server that comes back must be picked up without an operator
	// intervening, so the delay is capped rather than doubling forever.
	if wait := time.Until(state.nextTry); wait > veeamBackoffMax+time.Second {
		t.Errorf("backoff grew to %v, want it capped at %v", wait, veeamBackoffMax)
	}
}

// A credential that cannot be decrypted must produce an operator-readable
// message, and must not leak anything about the ciphertext.
func TestVeeamSync_UndecryptableCredential(t *testing.T) {
	server := testVeeamServer(t)
	server.PasswordEncrypted = "not-valid-ciphertext"
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	syncer := newVeeamTestSyncer(t, q, &fakeVeeamClient{})

	syncer.SyncInventory(context.Background())

	if len(q.syncErrors) != 1 {
		t.Fatalf("syncErrors = %+v, want the failure recorded", q.syncErrors)
	}
	msg := q.syncErrors[0].LastSyncError
	if msg == "" {
		t.Fatal("no error recorded for an undecryptable credential")
	}
	for _, leak := range []string{"not-valid-ciphertext", server.PasswordEncrypted} {
		if leak != "" && contains(msg, leak) {
			t.Errorf("last_sync_error leaked the stored value: %q", msg)
		}
	}
}

// last_sync_error is rendered in the UI and nothing upstream bounds the length
// of an error a remote server can produce.
func TestTruncateSyncError(t *testing.T) {
	short := "connection refused"
	if got := truncateSyncError(short); got != short {
		t.Errorf("a short error was altered: %q", got)
	}

	long := make([]rune, maxSyncErrorLen*3)
	for i := range long {
		long[i] = 'é' // multi-byte, so a naive byte slice would split it
	}
	got := truncateSyncError(string(long))
	if len([]rune(got)) != maxSyncErrorLen+1 { // +1 for the ellipsis
		t.Errorf("truncated to %d runes, want %d plus an ellipsis", len([]rune(got)), maxSyncErrorLen)
	}
	if !contains(got, "…") {
		t.Error("a truncated error should say it was cut")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestVeeamSync_InventoryCorrelates(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		objects: []veeam.BackupObject{
			{ID: testObjectID, ObjectID: "316e531d-55c0-4fef-adc2-f1bb9c4e1873", PlatformName: "Proxmox",
				PlatformID: testPlatformID, Name: "web01", Type: "VM"},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	if len(q.correlated) != 1 || q.correlated[0] != server.ID {
		t.Errorf("correlated = %v, want one call for %s", q.correlated, server.ID)
	}
}

func TestVeeamSync_CorrelationFailureDoesNotFailThePass(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{
		servers:      []db.VeeamServer{server},
		correlateErr: errors.New("deadlock detected"),
	}
	c := &fakeVeeamClient{
		objects: []veeam.BackupObject{
			{ID: testObjectID, ObjectID: "316e531d-55c0-4fef-adc2-f1bb9c4e1873", PlatformName: "Proxmox",
				PlatformID: testPlatformID, Name: "web01", Type: "VM"},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	// Correlation is an enrichment of an inventory that is already correct
	// without it. Failing the pass would put the server into backoff and stop
	// collecting the very data the correlation decorates.
	if len(q.syncSuccesses) != 1 {
		t.Errorf("syncSuccesses = %v, want the pass to still succeed", q.syncSuccesses)
	}
	if len(q.syncErrors) != 0 {
		t.Errorf("syncErrors = %+v, want none — a failed enrichment is not a failed sync", q.syncErrors)
	}
	if q.serverNote != "" {
		t.Errorf("last_sync_error = %q, want empty", q.serverNote)
	}
}

func TestVeeamSync_RecordsVeeamOwnGuests(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		proxies: []veeam.ProxyState{
			// The lab's real shape: two proxies the VBR server fills itself,
			// which are not guests anywhere, and three Proxmox appliances.
			{ID: uuid.NewString(), Name: "Backup Proxy", Type: "GeneralPurposeProxy", HostName: "This server", IsOnline: true},
			{ID: uuid.NewString(), Name: "VMware Backup Proxy", Type: "ViProxy", HostName: "This server", IsOnline: true},
			{ID: uuid.NewString(), Name: "veeam13-appliance01", Type: veeam.ProxmoxProxyType, HostName: "hv01.example.lan"},
			{ID: uuid.NewString(), Name: "Veeam13-appliance02", Type: veeam.ProxmoxProxyType, HostName: "hv02.example.lan"},
		},
		managed: []veeam.ManagedServer{
			{ID: uuid.NewString(), Name: "vbr01.example.lan", Type: "WindowsHost", Status: "Available", IsBackupServer: true},
			// A repository host is a managed server too, and is not Veeam's
			// own guest on the protected cluster.
			{ID: uuid.NewString(), Name: "nas01.example.lan", Type: "LinuxHost", Status: "Available"},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	byName := map[string]db.UpsertVeeamInfrastructureParams{}
	for _, row := range q.infrastructure {
		byName[row.Name] = row
	}
	// Only the PVE proxies and the backup server. A GeneralPurposeProxy or a
	// ViProxy runs on the VBR host, not on the Proxmox cluster, and excluding
	// a guest that happens to share one of those names from coverage would
	// hide a real machine's lack of backups.
	if len(byName) != 3 {
		t.Fatalf("infrastructure rows = %d (%v), want 3", len(byName), byName)
	}
	for _, name := range []string{"veeam13-appliance01", "Veeam13-appliance02"} {
		row, ok := byName[name]
		if !ok {
			t.Errorf("worker %q was not recorded", name)
			continue
		}
		if row.Role != veeamRoleWorker {
			t.Errorf("%q role = %q, want %q", name, row.Role, veeamRoleWorker)
		}
	}
	if row, ok := byName["vbr01.example.lan"]; !ok {
		t.Error("the VBR server was not recorded")
	} else if row.Role != veeamRoleBackupServer {
		t.Errorf("backup server role = %q, want %q", row.Role, veeamRoleBackupServer)
	}

	if len(q.staleInfrastructure) != 1 {
		t.Errorf("stale infrastructure sweeps = %d, want 1", len(q.staleInfrastructure))
	}
	if len(q.resolvedInfra) != 1 {
		t.Errorf("resolvedInfra = %v, want one call", q.resolvedInfra)
	}
}

func TestVeeamSync_UnreadableManagedServersStillRecordsWorkers(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		proxies: []veeam.ProxyState{
			{ID: uuid.NewString(), Name: "veeam13-appliance01", Type: veeam.ProxmoxProxyType},
		},
		managedErr: errors.New("insufficient rights"),
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	// The workers are the bulk of the value, and a VBR server that is not a
	// guest on any mapped cluster — the common case — contributes nothing
	// here anyway. Losing the whole pass over it would be a poor trade.
	if len(q.infrastructure) != 1 {
		t.Fatalf("infrastructure rows = %d, want the worker", len(q.infrastructure))
	}
	if len(q.syncSuccesses) != 1 {
		t.Errorf("syncSuccesses = %v, want the pass to succeed", q.syncSuccesses)
	}

	// And the sweep must NOT be allowed to touch the role whose listing it
	// could not read. A Veeam account with rights to the proxy listing but
	// not the managed-server one fails this way on every pass, and an
	// unscoped sweep would age the backup server out of the table inside the
	// grace window — putting its guest back in the coverage report as a false
	// alarm, with a clean last_sync_at and nothing to explain it.
	if len(q.staleInfrastructure) != 1 {
		t.Fatalf("stale sweeps = %d, want 1", len(q.staleInfrastructure))
	}
	if roles := q.staleInfrastructure[0].Roles; len(roles) != 1 || roles[0] != veeamRoleWorker {
		t.Errorf("sweep roles = %v, want only %q", roles, veeamRoleWorker)
	}
}

func TestVeeamSync_ProxyListingFailureWarnsButKeepsThePass(t *testing.T) {
	server := testVeeamServer(t)
	q := &fakeVeeamQueries{servers: []db.VeeamServer{server}}
	c := &fakeVeeamClient{
		proxiesErr: errors.New("connection reset"),
		objects: []veeam.BackupObject{
			{ID: testObjectID, ObjectID: "316e531d-55c0-4fef-adc2-f1bb9c4e1873", PlatformName: "Proxmox",
				PlatformID: testPlatformID, Name: "web01", Type: "VM"},
		},
	}
	syncer := newVeeamTestSyncer(t, q, c)

	syncer.SyncInventory(context.Background())

	// The rows it could not refresh are kept, so every worker appliance stays
	// excluded from coverage across the outage.
	for _, sweep := range q.staleInfrastructure {
		for _, role := range sweep.Roles {
			if role == veeamRoleWorker {
				t.Error("a failed proxy listing still swept the worker rows")
			}
		}
	}

	// The pass itself survives. Discarding it would throw away the
	// repositories, jobs and backup objects already written this pass, skip
	// the correlation that follows, and put the server into backoff — a steep
	// price for the least valuable listing on the server.
	if len(q.syncSuccesses) != 1 {
		t.Errorf("syncSuccesses = %v, want the pass to still succeed", q.syncSuccesses)
	}
	if len(q.correlated) != 1 {
		t.Errorf("correlated = %v, want the correlation to still run", q.correlated)
	}

	// But it is not silent: the caveat reaches last_sync_error beside a fresh
	// last_sync_at, because a persistent failure here means guests that are
	// Veeam's own are being reported as unprotected.
	if q.note() == "" {
		t.Error("a failed proxy listing left no warning on the server row")
	}
}

func TestInventoryTriggerCoalesces(t *testing.T) {
	trig := NewInventoryTrigger()

	// Three registrations in a row must not queue three passes: the next pass
	// picks up every server regardless, so the extras are pure duplicate load
	// on a VBR that is already being asked for a full inventory.
	trig.TriggerInventory()
	trig.TriggerInventory()
	trig.TriggerInventory()

	select {
	case <-trig.C():
	default:
		t.Fatal("trigger did not fire")
	}

	select {
	case <-trig.C():
		t.Fatal("trigger fired twice; three sends should coalesce to one")
	default:
	}
}

func TestInventoryTriggerDoesNotBlock(t *testing.T) {
	trig := NewInventoryTrigger()

	// Nobody is selecting on it. A send that blocked here would stall the
	// request goroutine that registered the server.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 5 {
			trig.TriggerInventory()
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("TriggerInventory blocked with no reader")
	}
}

func TestInventoryTriggerNilIsInert(t *testing.T) {
	var trig *InventoryTrigger

	// The collector can be disabled (VEEAM_SYNC_INTERVAL=0), and registering a
	// server must still work rather than panic.
	trig.TriggerInventory()

	// A nil channel blocks forever in a select, which is what makes the loop's
	// case need no nil branch of its own.
	if ch := trig.C(); ch != nil {
		t.Fatalf("nil trigger yielded a non-nil channel: %v", ch)
	}
}
