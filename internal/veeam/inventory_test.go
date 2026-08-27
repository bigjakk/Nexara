package veeam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// serveFixtureList makes the fake VBR answer path with the rows from a
// captured listing, honouring limit/skip so the client's pagination walk is
// actually exercised rather than short-circuited by a single full page.
func (f *fakeVBR) serveFixtureList(path, fixtureName string) {
	var body struct {
		Data       []json.RawMessage `json:"data"`
		Pagination pagination        `json:"pagination"`
	}
	if err := json.Unmarshal(fixture(f.t, fixtureName), &body); err != nil {
		f.t.Fatalf("decode %s: %v", fixtureName, err)
	}
	f.lists[path] = body.Data
}

func TestRepositories_ConvertsFloatGBToBytes(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.serveFixtureList("/api/v1/backupInfrastructure/repositories/states",
		"backupinfrastructure_repositories_states.json")
	c := newTestClient(t, srv, "administrator")

	repos, err := c.Repositories(context.Background())
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	if len(repos) != 3 {
		t.Fatalf("got %d repositories, want 3", len(repos))
	}

	// The API reports capacity as floating-point GB. Storing that verbatim
	// would make every downstream size wrong by a factor of 2^30.
	var found bool
	for _, r := range repos {
		if r.Name != "example-bucket" {
			continue
		}
		found = true
		if r.CapacityGB != 3072.0 {
			t.Errorf("CapacityGB = %v, want 3072", r.CapacityGB)
		}
		if got, want := r.CapacityBytes(), int64(3072)*1024*1024*1024; got != want {
			t.Errorf("CapacityBytes = %d, want %d", got, want)
		}
		if got, want := r.FreeBytes(), int64(2104)*1024*1024*1024; got != want {
			t.Errorf("FreeBytes = %d, want %d", got, want)
		}
		if !r.IsOnline {
			t.Error("IsOnline = false, want true")
		}
	}
	if !found {
		t.Error("the S3 repository was not in the listing")
	}
}

func TestRepositories_NegativeAndZeroCapacityClampToZero(t *testing.T) {
	r := Repository{CapacityGB: -1, FreeGB: 0}
	if got := r.CapacityBytes(); got != 0 {
		t.Errorf("CapacityBytes for a negative GB = %d, want 0", got)
	}
	if got := r.FreeBytes(); got != 0 {
		t.Errorf("FreeBytes for zero GB = %d, want 0", got)
	}
}

// Proxmox jobs are absent from /api/v1/jobs entirely, which is why the sync
// reads /jobs/states. This pins that both fixtures still say so.
func TestJobStates_ProxmoxJobsOnlyAppearInStates(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.serveFixtureList("/api/v1/jobs/states", "jobs_states.json")
	f.serveFixtureList("/api/v1/jobs", "jobs_list.json")
	c := newTestClient(t, srv, "administrator")

	states, err := c.JobStates(context.Background())
	if err != nil {
		t.Fatalf("JobStates: %v", err)
	}

	var proxmox int
	for _, j := range states {
		if j.IsProxmox() {
			proxmox++
		}
	}
	if proxmox != 12 {
		t.Errorf("Proxmox jobs in /jobs/states = %d, want 12", proxmox)
	}

	// And the plain listing has none — the reason /jobs is never polled.
	var plain []JobState
	plain, err = listPaged[JobState](context.Background(), c, "/api/v1/jobs", nil, pageSize)
	if err != nil {
		t.Fatalf("list /api/v1/jobs: %v", err)
	}
	for _, j := range plain {
		if j.IsProxmox() {
			t.Fatal("/api/v1/jobs returned a Proxmox job — the fixture no longer encodes the gap this design works around")
		}
	}
}

func TestJobStates_DecodesProgressAndSchedule(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.serveFixtureList("/api/v1/jobs/states", "jobs_states.json")
	c := newTestClient(t, srv, "administrator")

	states, err := c.JobStates(context.Background())
	if err != nil {
		t.Fatalf("JobStates: %v", err)
	}

	var job *JobState
	for i := range states {
		if states[i].Name == "Onsite_Daily_Appliance" {
			job = &states[i]
			break
		}
	}
	if job == nil {
		t.Fatal("Onsite_Daily_Appliance not found")
	}

	if job.Type != ProxmoxJobType {
		t.Errorf("Type = %q, want %q", job.Type, ProxmoxJobType)
	}
	if job.LastResult != "Warning" {
		t.Errorf("LastResult = %q", job.LastResult)
	}
	// lastRun carries an offset and sub-second precision; nextRun carries an
	// offset and none. Both must decode.
	if job.LastRun.Or().IsZero() {
		t.Error("LastRun did not decode")
	}
	if job.NextRun.Or().IsZero() {
		t.Error("NextRun did not decode")
	}
	if job.RepositoryName != "repo-nas-01" {
		t.Errorf("RepositoryName = %q", job.RepositoryName)
	}
	if job.ObjectsCount != 1 {
		t.Errorf("ObjectsCount = %d, want 1", job.ObjectsCount)
	}
	if job.SessionID == "" {
		t.Error("SessionID empty — the link to the last run is missing")
	}
}

func TestBackupObjects_ObjectIDIsTheSMBIOSUUID(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.serveFixtureList("/api/v1/backupObjects", "backupobjects_list.json")
	c := newTestClient(t, srv, "administrator")

	objects, err := c.BackupObjects(context.Background())
	if err != nil {
		t.Fatalf("BackupObjects: %v", err)
	}

	var proxmox []BackupObject
	for _, o := range objects {
		if o.IsProxmox() {
			proxmox = append(proxmox, o)
		}
	}
	if len(proxmox) != 27 {
		t.Fatalf("Proxmox objects = %d, want 27", len(proxmox))
	}

	// One row per (guest × backup): the same guest name appears more than
	// once, which is why any per-guest rollup has to aggregate.
	names := make(map[string]int)
	ids := make(map[string]struct{})
	for _, o := range proxmox {
		names[o.Name]++
		ids[o.ObjectID] = struct{}{}
		if o.ObjectID == "" {
			t.Errorf("%s has no objectId — guest correlation would fall back to a name match", o.Name)
		}
		if o.ID == o.ObjectID {
			t.Errorf("%s: row id and objectId are the same value; they are different identities", o.Name)
		}
	}
	// Fewer distinct guests than rows: the listing repeats a guest once per
	// backup, which is why the collector folds it.
	if len(ids) >= len(proxmox) {
		t.Errorf("distinct objectIds (%d) should be fewer than rows (%d) — duplicates are the whole reason the collector folds",
			len(ids), len(proxmox))
	}
	// And the ROW id repeats too, not just objectId — the fact that makes
	// folding on it correct.
	rowIDs := map[string]struct{}{}
	for _, o := range proxmox {
		rowIDs[o.ID] = struct{}{}
	}
	if len(rowIDs) >= len(proxmox) {
		t.Errorf("distinct row ids (%d) should be fewer than rows (%d) — id is the guest's identity, not the row's",
			len(rowIDs), len(proxmox))
	}

	var duplicated bool
	for _, n := range names {
		if n > 1 {
			duplicated = true
		}
	}
	if !duplicated {
		t.Error("no guest appears under more than one backup; the fixture no longer encodes the duplicate-name case")
	}
}

func TestRestorePointsForObject(t *testing.T) {
	f, srv := newFakeVBR(t)
	const objectID = "3aad74b4-8013-4b41-b266-d21b6d88cc21"
	f.serveFixtureList("/api/v1/backupObjects/"+objectID+"/restorePoints", "restorepoints_list.json")
	c := newTestClient(t, srv, "administrator")

	points, err := c.RestorePointsForObject(context.Background(), objectID)
	if err != nil {
		t.Fatalf("RestorePointsForObject: %v", err)
	}
	if len(points) == 0 {
		t.Fatal("no restore points decoded")
	}

	var flr, clean int
	for _, p := range points {
		if p.CreationTime.IsZero() {
			t.Errorf("%s: creationTime did not decode", p.ID)
		}
		if p.SupportsFLR() {
			flr++
		}
		if p.MalwareStatus == "Clean" {
			clean++
		}
	}
	// File-level restore is the only restore Proxmox supports on 13.1, and
	// malware status rides along free with every point.
	if flr == 0 {
		t.Error("no point advertised StartFlrRestore")
	}
	if clean == 0 {
		t.Error("no point carried a malware status")
	}
}

func TestRestorePointsForObject_RejectsEmptyID(t *testing.T) {
	_, srv := newFakeVBR(t)
	c := newTestClient(t, srv, "administrator")

	if _, err := c.RestorePointsForObject(context.Background(), ""); err == nil {
		t.Fatal("empty objectID was accepted; it would build a request for the collection path")
	}
}

// The session poll MUST narrow server-side. ConfigurationResynchronize is 109
// of every 200 rows on a real server, so an unfiltered poll never reaches a
// backup run.
func TestSessions_FiltersServerSide(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.serveFixtureList("/api/v1/sessions", "sessions_list.json")
	c := newTestClient(t, srv, "administrator")

	since := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	if _, err := c.Sessions(context.Background(), since, 50); err != nil {
		t.Fatalf("Sessions: %v", err)
	}

	q := f.lastQuery("/api/v1/sessions")
	if got := q.Get("typeFilter"); got != PlatformBackupSessionType {
		t.Errorf("typeFilter = %q, want %q", got, PlatformBackupSessionType)
	}
	if got := q.Get("createdAfterFilter"); got != "2026-08-25T00:00:00Z" {
		t.Errorf("createdAfterFilter = %q, want the watermark in RFC3339 UTC", got)
	}
	if got := q.Get("orderColumn"); got != "CreationTime" {
		t.Errorf("orderColumn = %q, want CreationTime", got)
	}
	if got := q.Get("limit"); got != "50" {
		t.Errorf("limit = %q, want 50", got)
	}
}

func TestSessions_OmitsFilterForZeroWatermark(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.serveFixtureList("/api/v1/sessions", "sessions_list.json")
	c := newTestClient(t, srv, "administrator")

	if _, err := c.Sessions(context.Background(), time.Time{}, 10); err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if got := f.lastQuery("/api/v1/sessions").Get("createdAfterFilter"); got != "" {
		t.Errorf("createdAfterFilter = %q, want it absent on a first sync", got)
	}
}

func TestSessions_DecodesProxmoxRun(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.serveFixtureList("/api/v1/sessions", "sessions_list.json")
	c := newTestClient(t, srv, "administrator")

	sessions, err := c.Sessions(context.Background(), time.Time{}, pageSize)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}

	var run *Session
	for i := range sessions {
		if sessions[i].IsProxmoxBackup() {
			run = &sessions[i]
			break
		}
	}
	if run == nil {
		t.Fatal("no Proxmox backup session decoded")
	}

	// platformId is what makes a session cluster-scopable, and the only
	// bridge from a job to its platform.
	if run.PlatformID == "" {
		t.Error("PlatformID empty — job platform derivation has nothing to work from")
	}
	if run.JobID == "" {
		t.Error("JobID empty — the session cannot be attributed to a job")
	}
	if run.CreationTime.IsZero() {
		t.Error("CreationTime did not decode; the watermark would never advance")
	}
	if run.Progress.Bottleneck == "" {
		t.Error("bottleneck missing")
	}
}

// A short page ends the walk; a full page continues it. Without both, a
// listing either stops early or spins.
func TestListPaged_WalksUntilShortPage(t *testing.T) {
	f, srv := newFakeVBR(t)
	// 250 rows over a 200-row page size: one full page then a partial one.
	rows := make([]json.RawMessage, 250)
	for i := range rows {
		rows[i] = json.RawMessage(fmt.Sprintf(`{"id":"row-%d"}`, i))
	}
	f.lists["/api/v1/backupObjects"] = rows
	c := newTestClient(t, srv, "administrator")

	objects, err := c.BackupObjects(context.Background())
	if err != nil {
		t.Fatalf("BackupObjects: %v", err)
	}
	if len(objects) != 250 {
		t.Fatalf("got %d rows, want 250", len(objects))
	}
	if n := f.pathCalls("/api/v1/backupObjects"); n != 2 {
		t.Errorf("pages fetched = %d, want 2", n)
	}
}

// A server that reports a full page forever must not spin the collector.
func TestListPaged_StopsAtMaxPages(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.repeatFullPages = true
	c := newTestClient(t, srv, "administrator")

	objects, err := c.BackupObjects(context.Background())
	if err != nil {
		t.Fatalf("BackupObjects: %v", err)
	}
	if n := f.pathCalls("/api/v1/backupObjects"); n != maxPages {
		t.Errorf("pages fetched = %d, want the maxPages cap of %d", n, maxPages)
	}
	if len(objects) != maxInventoryRows {
		t.Errorf("rows = %d, want %d", len(objects), maxInventoryRows)
	}
}

// maxRows is a TOTAL, not a page size. Treating it as a page size is how a
// "newest 500 sessions" poll became a 40,000-row walk that outran its own
// timeout and therefore stored nothing at all, leaving the watermark stuck.
func TestSessions_MaxRowsBoundsTheWholeWalk(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.repeatFullPages = true
	c := newTestClient(t, srv, "administrator")

	sessions, err := c.Sessions(context.Background(), time.Time{}, 500)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 500 {
		t.Errorf("rows = %d, want the walk bounded at the requested 500", len(sessions))
	}
	// 500 rows at a 200-row page size is three requests, not two hundred.
	if n := f.pathCalls("/api/v1/sessions"); n != 3 {
		t.Errorf("pages fetched = %d, want 3", n)
	}
}

// A cap below one page must shrink the page too, not silently fetch 200.
func TestListPaged_SmallMaxRowsShrinksThePage(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.repeatFullPages = true
	c := newTestClient(t, srv, "administrator")

	sessions, err := c.Sessions(context.Background(), time.Time{}, 5)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 5 {
		t.Errorf("rows = %d, want 5", len(sessions))
	}
	if got := f.lastQuery("/api/v1/sessions").Get("limit"); got != "5" {
		t.Errorf("page limit = %q, want 5", got)
	}
}

func TestTimestamp_DecodesEveryLayoutTheAPIEmits(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string // RFC3339 in UTC, or "" for the zero time
	}{
		{"offset with nanos", `"2026-08-24T22:00:29.381149-07:00"`, "2026-08-25T05:00:29Z"},
		{"offset without nanos", `"2026-08-25T12:57:50-07:00"`, "2026-08-25T19:57:50Z"},
		{"zulu", `"2027-04-27T00:00:00Z"`, "2027-04-27T00:00:00Z"},
		// The backups listing emits this form. A plain time.Time field fails
		// the whole decode on it.
		{"no offset at all", `"2026-02-14T22:37:33"`, "2026-02-14T22:37:33Z"},
		{"date only", `"2026-02-14"`, "2026-02-14T00:00:00Z"},
		{"json null", `null`, ""},
		{"empty string", `""`, ""},
		// Garbage decodes to zero rather than failing: losing one field beats
		// losing the listing it came in.
		{"unparseable", `"not a time"`, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ts Timestamp
			if err := json.Unmarshal([]byte(tc.raw), &ts); err != nil {
				t.Fatalf("Unmarshal(%s) errored: %v", tc.raw, err)
			}
			if tc.want == "" {
				if !ts.IsZero() {
					t.Errorf("got %v, want the zero time", ts.Time)
				}
				return
			}
			if got := ts.UTC().Format(time.RFC3339); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTimestamp_RoundTripsThroughJSON(t *testing.T) {
	var ts Timestamp
	if err := json.Unmarshal([]byte(`"2026-08-25T12:57:50-07:00"`), &ts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	encoded, err := json.Marshal(ts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Timestamp
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !back.Equal(ts.Time) {
		t.Errorf("round trip changed the instant: %v -> %v", ts.Time, back.Time)
	}

	zero, err := json.Marshal(Timestamp{})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if string(zero) != "null" {
		t.Errorf("zero Timestamp marshalled as %s, want null", zero)
	}
}

// paginate slices rows for a limit/skip request the way the server does.
func paginate(rows []json.RawMessage, q url.Values) ([]json.RawMessage, int) {
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = pageSize
	}
	skip, _ := strconv.Atoi(q.Get("skip"))
	if skip > len(rows) {
		skip = len(rows)
	}
	end := min(skip+limit, len(rows))
	return rows[skip:end], len(rows)
}

func writeListPage(w http.ResponseWriter, rows []json.RawMessage, total, skip, limit int) {
	if rows == nil {
		rows = []json.RawMessage{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": rows,
		"pagination": map[string]int{
			"total": total, "count": len(rows), "skip": skip, "limit": limit,
		},
	})
}
