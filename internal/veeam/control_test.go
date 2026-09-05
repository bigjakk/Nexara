package veeam

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const (
	testJobID     = "3f4b1f0e-6d1e-4d2a-9f13-1c1a2b3c4d5e"
	testSessionID = "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
)

// sessionBody is what a start or stop returns inline — the Veeam analogue of
// capturing a Proxmox UPID, with everything the session row needs.
func sessionBody(state string) string {
	return `{
		"id": "` + testSessionID + `",
		"jobId": "` + testJobID + `",
		"name": "Daily-Backup-Win",
		"sessionType": "PlatformBackupJob",
		"platformName": "Proxmox",
		"platformId": "01208ee8-47fe-4ea8-8727-5115874da1ad",
		"state": "` + state + `",
		"creationTime": "2026-08-27T09:15:00.000Z",
		"initiatedBy": "ad\\jdoe"
	}`
}

func TestStartJob_ReturnsSessionInline(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.respond("POST /api/v1/jobs/"+testJobID+"/start", http.StatusCreated, sessionBody("Starting"))
	c := newTestClient(t, srv, `ad\jdoe`)

	run, err := c.StartJob(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	if run.Session == nil {
		t.Fatal("StartJob returned no session for a 201")
	}
	if run.NoObjects {
		t.Error("a 201 with a session was reported as NoObjects")
	}
	if run.Session.ID != testSessionID {
		t.Errorf("session ID = %q, want %q", run.Session.ID, testSessionID)
	}
	if run.Session.State != "Starting" {
		t.Errorf("session State = %q, want Starting", run.Session.State)
	}
	// The whole point of persisting from the 201: the platform arrives with
	// the session, so the row is cluster-attributable before any poll runs.
	if run.Session.PlatformID != "01208ee8-47fe-4ea8-8727-5115874da1ad" {
		t.Errorf("session PlatformID = %q, want the Proxmox platform", run.Session.PlatformID)
	}
}

// VBR rejects a bodiless control POST that does not send Content-Length: 0.
// net/http sends it for POST with a nil body — this pins that, because a
// future edit attaching an empty io.Reader would silently switch the request
// to chunked encoding and the failure would only show up against a real
// server.
func TestJobControl_SendsZeroContentLength(t *testing.T) {
	f, srv := newFakeVBR(t)
	key := "POST /api/v1/jobs/" + testJobID + "/enable"
	f.respond(key, http.StatusNoContent, "")
	c := newTestClient(t, srv, `ad\jdoe`)

	if err := c.EnableJob(context.Background(), testJobID); err != nil {
		t.Fatalf("EnableJob: %v", err)
	}
	if got := f.contentLength(key); got != "0" {
		t.Errorf("Content-Length = %q, want %q", got, "0")
	}
}

// 204 is a documented success with no session: the job had no objects to
// process. It must not be an error, and it must not be a fabricated session
// either — a zero-valued one would be stored against an empty uuid.
func TestStartJob_NoObjectsReturnsNoSession(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.respond("POST /api/v1/jobs/"+testJobID+"/start", http.StatusNoContent, "")
	c := newTestClient(t, srv, `ad\jdoe`)

	run, err := c.StartJob(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("StartJob on an empty job: %v", err)
	}
	if run.Session != nil {
		t.Errorf("StartJob returned session %+v for a 204, want nil", run.Session)
	}
	// NoObjects is what lets the handler say "the job had nothing to back up"
	// rather than the vaguer "no run to track" it uses for a body it could not
	// read. Only the documented 204 sets it.
	if !run.NoObjects {
		t.Error("a 204 was not reported as NoObjects; the handler cannot then explain why no run exists")
	}
}

// A 2xx is ACCEPTED, whatever the body turns out to hold.
//
// Once Veeam has answered 2xx it has taken the request, and no decoding
// problem afterwards can make that untrue. Erroring here would tell an
// operator that a job which is now running did not start — and invite the
// retry that starts it twice; for a stop it would also skip the
// nexara_stopped flag and raise the false failure alert the whole phase
// exists to prevent. The unusable session is dropped instead, and the poll
// loop picks the run up on its next pass.
func TestJobActions_A2xxIsAcceptedEvenWithAnUnusableBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"session carries no id", `{"state":"Starting"}`},
		{"session id is not a uuid", `{"id":"not-a-uuid","state":"Starting"}`},
		{"body is not a session at all", `["surprise"]`},
		{"body is empty", ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, srv := newFakeVBR(t)
			f.respond("POST /api/v1/jobs/"+testJobID+"/start", http.StatusCreated, tt.body)
			c := newTestClient(t, srv, `ad\jdoe`)

			run, err := c.StartJob(context.Background(), testJobID)
			if err != nil {
				t.Fatalf("a 201 was reported as a failure: %v", err)
			}
			if run.Session != nil {
				t.Errorf("an unusable body produced session %+v, want none", run.Session)
			}
			// NOT NoObjects: that is a specific claim about the job's
			// contents, and a body we could not read says nothing about them.
			if run.NoObjects {
				t.Error("an unreadable body was reported as the job having nothing to back up")
			}
		})
	}
}

func TestStopJob_ReturnsStoppingSession(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.respond("POST /api/v1/jobs/"+testJobID+"/stop", http.StatusCreated, sessionBody("Stopping"))
	c := newTestClient(t, srv, `ad\jdoe`)

	run, err := c.StopJob(context.Background(), testJobID)
	if err != nil {
		t.Fatalf("StopJob: %v", err)
	}
	if run.Session == nil || run.Session.State != "Stopping" {
		t.Fatalf("StopJob session = %+v, want state Stopping", run.Session)
	}
}

// Stopping a job that is not running is a 400 with a specific message, and it
// must surface as an error rather than being folded into a success — "stopped"
// reported for a job still running somewhere is the wrong answer to give an
// operator.
func TestStopJob_NotRunningIsAnError(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.respond("POST /api/v1/jobs/"+testJobID+"/stop", http.StatusBadRequest,
		`{"errorCode":"InvalidOperation","message":"The job is not currently running.","status":400}`)
	c := newTestClient(t, srv, `ad\jdoe`)

	_, err := c.StopJob(context.Background(), testJobID)
	if err == nil {
		t.Fatal("StopJob on a stopped job returned no error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("StopJob error = %T (%v), want *APIError so the handler can map 400 to 409", err, err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("APIError.StatusCode = %d, want 400", apiErr.StatusCode)
	}
	// NOT the Proxmox-platform 400. Both are 400s from the same endpoint
	// family and only the message tells them apart, so a client that mapped
	// every 400 to ErrPlatformUnsupported would report a running-state
	// problem as an unsupported platform.
	if errors.Is(err, ErrPlatformUnsupported) {
		t.Error("a not-running 400 was classified as ErrPlatformUnsupported")
	}
}

func TestJobActions_HitTheRightPaths(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) error
		path string
	}{
		{"enable", func(c *Client) error { return c.EnableJob(context.Background(), testJobID) },
			"/api/v1/jobs/" + testJobID + "/enable"},
		{"disable", func(c *Client) error { return c.DisableJob(context.Background(), testJobID) },
			"/api/v1/jobs/" + testJobID + "/disable"},
		{"session stop", func(c *Client) error { return c.StopSession(context.Background(), testSessionID) },
			"/api/v1/sessions/" + testSessionID + "/stop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, srv := newFakeVBR(t)
			f.respond("POST "+tt.path, http.StatusOK, `{}`)
			c := newTestClient(t, srv, `ad\jdoe`)

			if err := tt.call(c); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			var found bool
			for _, req := range f.seen() {
				if req == "POST "+tt.path {
					found = true
				}
			}
			if !found {
				t.Errorf("no POST to %s; saw %v", tt.path, f.seen())
			}
		})
	}
}

// Every id reaching these methods arrives through an HTTP handler, so a
// non-uuid must be refused before it is concatenated into a request path
// rather than sent to the server to reject.
func TestControl_RejectsNonUUIDIDs(t *testing.T) {
	f, srv := newFakeVBR(t)
	c := newTestClient(t, srv, `ad\jdoe`)
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{"start", func() error { _, e := c.StartJob(ctx, "../../etc/passwd"); return e }},
		{"stop", func() error { _, e := c.StopJob(ctx, "not-a-uuid"); return e }},
		{"enable", func() error { return c.EnableJob(ctx, "") }},
		{"disable", func() error { return c.DisableJob(ctx, "1") }},
		{"session stop", func() error { return c.StopSession(ctx, "../sessions") }},
		{"session logs", func() error { _, e := c.SessionLogs(ctx, "nope"); return e }},
	}
	for _, tt := range tests {
		if err := tt.call(); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", tt.name, err)
		}
	}

	// The refusal is LOCAL: nothing reached the server at all, not even a
	// token grant. An id that is checked only by the far end has already been
	// concatenated into a path by the time anyone objects.
	if seen := f.seen(); len(seen) != 0 {
		t.Errorf("a rejected id still produced requests: %v", seen)
	}
}

// requireUUID normalises the spelling, so two forms of one id cannot become
// two ids and no separator can survive into the path.
func TestRequireUUID_NormalisesSpelling(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{testJobID, testJobID},
		{strings.ToUpper(testJobID), testJobID},
		{"{" + testJobID + "}", testJobID},
		{"urn:uuid:" + testJobID, testJobID},
	}
	for _, tt := range tests {
		got, err := requireUUID("job id", tt.in)
		if err != nil {
			t.Errorf("requireUUID(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("requireUUID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSessionLogs_ReadsRecords(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.respond("GET /api/v1/sessions/"+testSessionID+"/logs", http.StatusOK, `{
		"totalRecords": 2,
		"records": [
			{"id": 2, "status": "Warning", "title": "Job finished with warning",
			 "startTime": "2026-08-27T09:20:00.000Z", "updateTime": "2026-08-27T09:20:00.000Z"},
			{"id": 1, "status": "Succeeded", "title": "Primary bottleneck: Source", "description": ""}
		]
	}`)
	c := newTestClient(t, srv, `ad\jdoe`)

	records, err := c.SessionLogs(context.Background(), testSessionID)
	if err != nil {
		t.Fatalf("SessionLogs: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].Title != "Job finished with warning" || records[0].Status != "Warning" {
		t.Errorf("first record = %+v, want the warning line", records[0])
	}
	// The response is {totalRecords, records}, NOT the {data, pagination}
	// envelope every listing uses — decoding it as one silently yields zero
	// records.
	if records[1].ID != 1 {
		t.Errorf("second record ID = %d, want 1", records[1].ID)
	}
}

// An empty log is the NORMAL result for a session that was stopped: Veeam
// keeps no records for a killed run. It must read as "no records", never as an
// error, or the UI would report a failure on every cancellation.
func TestSessionLogs_EmptyIsNotAnError(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.respond("GET /api/v1/sessions/"+testSessionID+"/logs", http.StatusOK, `{"totalRecords":0,"records":[]}`)
	c := newTestClient(t, srv, `ad\jdoe`)

	records, err := c.SessionLogs(context.Background(), testSessionID)
	if err != nil {
		t.Fatalf("SessionLogs on a killed session: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("got %d records, want 0", len(records))
	}
}

// The per-guest breakdown, and the two things about it that are easy to get
// wrong: it is the PLAIN endpoint (unlike /jobs and /proxies, whose Proxmox
// rows live only under /states), and its rows carry a platformId of their own.
func TestTaskSessions_DecodesPerGuestOutcomes(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.setList("/api/v1/sessions/"+testSessionID+"/taskSessions", []string{`{
		"id": "7b1c2d3e-4f50-4a61-b273-8495a6b7c8d9",
		"type": "Backup",
		"name": "dc01.example.com",
		"sessionType": "EndpointBackup",
		"sessionId": "` + testSessionID + `",
		"platformId": "01208ee8-47fe-4ea8-8727-5115874da1ad",
		"platformType": "Proxmox",
		"state": "Stopped",
		"algorithm": "Increment",
		"result": {"result": "Failed", "isCanceled": false,
		           "message": "Session failed, check session log for details."},
		"progress": {"duration": "00:02:28", "transferredSize": 0},
		"creationTime": "2026-08-26T23:00:31.125853",
		"endTime": "2026-08-26T23:02:59.50859"
	}`, `{
		"id": "8c2d3e4f-5061-4b72-c384-95a6b7c8d9e0",
		"type": "Backup",
		"name": "linux01.example.com",
		"sessionType": "EndpointBackup",
		"sessionId": "` + testSessionID + `",
		"platformId": "01208ee8-47fe-4ea8-8727-5115874da1ad",
		"state": "Stopped",
		"result": {"result": "Success", "isCanceled": false, "message": ""},
		"progress": {"duration": "00:04:11", "transferredSize": 1048576},
		"creationTime": "2026-08-26T23:00:31.125853"
	}`})
	c := newTestClient(t, srv, `ad\jdoe`)

	tasks, err := c.TaskSessions(context.Background(), testSessionID)
	if err != nil {
		t.Fatalf("TaskSessions: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}

	failed := tasks[0]
	if failed.Name != "dc01.example.com" {
		t.Errorf("name = %q", failed.Name)
	}
	// The guest's OWN result, which is the whole point: the run reports one
	// outcome and its guests can each have a different one.
	if failed.Result.Result != "Failed" {
		t.Errorf("result = %q, want Failed", failed.Result.Result)
	}
	if failed.Result.Message == "" {
		t.Error("the per-guest message was dropped; it is the reason THIS guest failed")
	}
	// Cluster-attributable without a join back through its session.
	if failed.PlatformID != "01208ee8-47fe-4ea8-8727-5115874da1ad" {
		t.Errorf("platformId = %q, want the Proxmox platform", failed.PlatformID)
	}
	if !failed.IsBackup() {
		t.Error("a Backup task did not report as one")
	}
	if failed.Progress.Duration != "00:02:28" {
		t.Errorf("duration = %q", failed.Progress.Duration)
	}
	if tasks[1].Result.Result != "Success" {
		t.Errorf("second task result = %q, want Success", tasks[1].Result.Result)
	}
}

// A running session returns zero rows — verified against a live run. It is not
// an error and must not be reported as one; the caller distinguishes it from a
// finished run with no detail using the run's own state.
func TestTaskSessions_EmptyForARunningSession(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.setList("/api/v1/sessions/"+testSessionID+"/taskSessions", nil)
	c := newTestClient(t, srv, `ad\jdoe`)

	tasks, err := c.TaskSessions(context.Background(), testSessionID)
	if err != nil {
		t.Fatalf("TaskSessions on a running run: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0", len(tasks))
	}
}

// Restore, Antivirus and Replica rows share this endpoint and are not a
// guest's outcome in a backup run.
func TestTaskSession_IsBackup(t *testing.T) {
	for _, tt := range []struct {
		typ  string
		want bool
	}{{"Backup", true}, {"Restore", false}, {"Antivirus", false}, {"Replica", false}, {"", false}} {
		if got := (TaskSession{Type: tt.typ}).IsBackup(); got != tt.want {
			t.Errorf("IsBackup(%q) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}
