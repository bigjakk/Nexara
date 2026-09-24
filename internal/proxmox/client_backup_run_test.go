package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The fake below stands in for the three endpoints a backup job run touches —
// GET /cluster/backup/{id}, GET /nodes and POST /nodes/{node}/vzdump — and
// answers them in the shapes PVE does (see RunBackupJob for where each shape
// is read from).

const runJobID = "backup-1a2b3c4d"

// runJobPVE records every request and answers from its fixtures.
type runJobPVE struct {
	t *testing.T
	// job is the "data" GET /cluster/backup/{runJobID} answers with.
	job string
	// nodes is the "data" GET /nodes answers with.
	nodes string
	// replies maps a node to the raw "data" its vzdump answers 200 with.
	replies map[string]string
	// answers maps a node to an answer of any other shape — a status, a body,
	// a dropped connection. A node in neither map answers 500 with a message,
	// as a die in vzdump does.
	answers map[string]func(http.ResponseWriter)
	// stopListeningAfterNodes closes the listener once GET /nodes has been
	// answered, and that connection with it, so the vzdump POSTs cannot even
	// connect.
	stopListeningAfterNodes bool

	srv   *httptest.Server
	mu    sync.Mutex
	posts map[string][]url.Values // node → every form it received
	calls []string                // "METHOD path", every request in arrival order
}

func (p *runJobPVE) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.calls = append(p.calls, r.Method+" "+r.URL.Path)
	p.mu.Unlock()

	write := func(status int, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/api2/json/cluster/backup/"+runJobID:
		write(http.StatusOK, `{"data":`+p.job+`}`)
	case r.Method == http.MethodGet && path == "/api2/json/nodes":
		if p.stopListeningAfterNodes {
			w.Header().Set("Connection", "close")
			_ = p.srv.Listener.Close()
		}
		write(http.StatusOK, `{"data":`+p.nodes+`}`)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api2/json/nodes/") &&
		strings.HasSuffix(path, "/vzdump"):
		node := strings.TrimSuffix(strings.TrimPrefix(path, "/api2/json/nodes/"), "/vzdump")
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			p.t.Errorf("POST %s: Content-Type %q, want a form", path, ct)
		}
		if err := r.ParseForm(); err != nil {
			p.t.Errorf("POST %s: ParseForm: %v", path, err)
		}
		p.mu.Lock()
		if p.posts == nil {
			p.posts = map[string][]url.Values{}
		}
		p.posts[node] = append(p.posts[node], r.PostForm)
		p.mu.Unlock()
		if reply, ok := p.replies[node]; ok {
			write(http.StatusOK, `{"data":`+reply+`}`)
			return
		}
		if answer, ok := p.answers[node]; ok {
			answer(w)
			return
		}
		write(http.StatusInternalServerError, `{"data":null,"message":"fixture: vzdump refused on `+node+`\n"}`)
	default:
		p.t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		write(http.StatusNotImplemented, `{"data":null}`)
	}
}

// postedNodes returns the nodes that received a vzdump POST, sorted.
func (p *runJobPVE) postedNodes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.posts))
	for n := range p.posts {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// onlyPost returns the one form node received, failing unless it got exactly
// one.
func (p *runJobPVE) onlyPost(t *testing.T, node string) url.Values {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.posts[node]) != 1 {
		t.Fatalf("%s received %d vzdump POSTs, want exactly 1", node, len(p.posts[node]))
	}
	return p.posts[node][0]
}

func runJobClient(t *testing.T, p *runJobPVE) *Client {
	t.Helper()
	p.t = t
	p.srv = httptest.NewServer(p)
	t.Cleanup(p.srv.Close)
	return newTestClient(t, p.srv.URL)
}

// runJobCutOff answers 200 and resets the connection partway through the
// body: the headers have arrived, so the failure lands in the body read — the
// transport error that names both ends of the socket.
func runJobCutOff(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", "100")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"data":"UPID:`))
	w.(http.Flusher).Flush()
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		panic(err)
	}
	// Long enough for the client to have read the headers; a reset that
	// arrived first would fail the header read instead, which the callers
	// check does not happen.
	time.Sleep(200 * time.Millisecond)
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0) // close with a reset, not a FIN
	}
	_ = conn.Close()
}

// runJobFixture is a job as read_job returns it: jobs.cfg's section with its
// id injected, and the three property strings parsed into objects by
// decode_value. The JSON types are the ones PVE sends. A top-level boolean or
// integer is a number, because SectionConfig's check_value adds 0 to it. Inside
// a property string every value stays the text parse_property_string read —
// prune-backups' and performance's counts are strings — except a boolean,
// which parse_boolean turns into a number: fleecing's enabled.
// (TestPrintPropertyStringSpellsEveryValue covers a number where PVE sends
// text.)
//
// Every value is distinct, so a value landing under the wrong key cannot pass,
// and each property string has several keys, so one printed partially or in a
// different order cannot either.
const runJobFixture = `{
	"id": "` + runJobID + `",
	"type": "vzdump",
	"schedule": "sat 02:00",
	"enabled": 1,
	"repeat-missed": 1,
	"comment": "weekly full",
	"all": 1,
	"exclude": "105,107",
	"storage": "store01",
	"mode": "snapshot",
	"compress": "zstd",
	"mailnotification": "failure",
	"mailto": "admin@example.com",
	"notes-template": "{{guestname}} on {{node}}",
	"protected": 1,
	"bwlimit": 10240,
	"prune-backups": {"keep-last": "3", "keep-daily": "7", "keep-weekly": "4"},
	"performance": {"pbs-entries-max": "2097152", "max-workers": "8"},
	"fleecing": {"storage": "store02", "enabled": 1},
	"exclude-path": ["/tmp/?*", "/var/cache/?*"]
}`

// runJobFixtureForm is runJobFixture as run_backup_now posts it: the
// scheduling and descriptive keys gone, all as digits, each property string
// printed back with every key, sorted, and exclude-path as the key repeated.
var runJobFixtureForm = url.Values{
	"all":              {"1"},
	"exclude":          {"105,107"},
	"storage":          {"store01"},
	"mode":             {"snapshot"},
	"compress":         {"zstd"},
	"mailnotification": {"failure"},
	"mailto":           {"admin@example.com"},
	"notes-template":   {"{{guestname}} on {{node}}"},
	"protected":        {"1"},
	"bwlimit":          {"10240"},
	"prune-backups":    {"keep-daily=7,keep-last=3,keep-weekly=4"},
	"performance":      {"max-workers=8,pbs-entries-max=2097152"},
	"fleecing":         {"enabled=1,storage=store02"},
	"exclude-path":     {"/tmp/?*", "/var/cache/?*"},
}

const (
	runUPID01 = "UPID:pve-01:0000A1B2:00C0FFEE:66F1A2B3:vzdump::user@pam!test:"
	runUPID04 = "UPID:pve-04:0000B3C4:00BADA55:66F1A2B4:vzdump:101:user@pam!test:"
)

// runJobNodes is a cluster in every state GET /nodes can report, listed out of
// name order (PVE's own order comes from a hash) so the sort is exercised. The
// two nodes that are not online sort AFTER some that are, so a failure list
// that merely appended the request failures to the offline ones would come out
// in a different order from the sorted one.
const runJobNodes = `[
	{"node": "pve-04", "status": "online"},
	{"node": "pve-02", "status": "online"},
	{"node": "pve-06", "status": "online"},
	{"node": "pve-07", "status": "offline"},
	{"node": "pve-01", "status": "online"},
	{"node": "pve-05", "status": "unknown"},
	{"node": "pve-03", "status": "online"}
]`

// runFailureCheck is one expected entry in Failed or Unconfirmed: the node and
// a predicate naming why it is there.
type runFailureCheck struct {
	node, why string
}

var runFailureReasons = map[string]func(error) bool{
	"offline": func(err error) bool {
		var e *NodeNotOnlineError
		return errors.As(err, &e) && e.Status == "offline"
	},
	"unknown": func(err error) bool {
		var e *NodeNotOnlineError
		return errors.As(err, &e) && e.Status == "unknown"
	},
	"untrackable": func(err error) bool {
		return errors.Is(err, ErrInvalidResponse) && strings.Contains(err.Error(), "cannot be tracked")
	},
	"unrecognised": func(err error) bool {
		return errors.Is(err, ErrInvalidResponse) && strings.Contains(err.Error(), `"done"`)
	},
	"unsendable": func(err error) bool { return errors.Is(err, ErrInvalidInput) },
}

func checkRunFailures(t *testing.T, list string, got []BackupJobRunFailure, want []runFailureCheck) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %+v, want %d entries %v", list, got, len(want), want)
	}
	for i, w := range want {
		if got[i].Node != w.node || !runFailureReasons[w.why](got[i].Err) {
			t.Errorf("%s[%d] = {%q %v}, want %q %s", list, i, got[i].Node, got[i].Err, w.node, w.why)
		}
	}
}

// TestRunBackupJobPostsTheJobAsRunBackupNowDoes follows an unpinned job from
// the read to every vzdump POST: the exact form each online node receives, the
// nodes that receive nothing, and how each reply is classed.
func TestRunBackupJobPostsTheJobAsRunBackupNowDoes(t *testing.T) {
	pve := &runJobPVE{
		job:   runJobFixture,
		nodes: runJobNodes,
		replies: map[string]string{
			"pve-01": `"` + runUPID01 + `"`,
			"pve-02": `"OK"`,
			"pve-04": `"` + runUPID04 + `"`,
			// Neither a task nor "OK": whether a backup started is unknown.
			"pve-03": `"done"`,
			// A UPID-shaped reply that would leave its path segment when the
			// collector polls it: a worker probably forked, but it cannot be
			// tracked — unconfirmed, not a task.
			"pve-06": `"UPID:pve-06:0000C5D6:00000001:66F1A2B5:vzdump:../../status:user@pam!test:"`,
		},
		// pve-05 is unknown and pve-07 offline, so neither may be asked.
	}
	run, err := runJobClient(t, pve).RunBackupJob(context.Background(), runJobID)
	if err != nil {
		t.Fatalf("RunBackupJob: %v", err)
	}

	if got, want := pve.postedNodes(), []string{"pve-01", "pve-02", "pve-03", "pve-04", "pve-06"}; !slices.Equal(got, want) {
		t.Fatalf("vzdump was posted to %v, want the online nodes %v", got, want)
	}
	for _, node := range pve.postedNodes() {
		if got := pve.onlyPost(t, node); !reflect.DeepEqual(got, runJobFixtureForm) {
			t.Errorf("%s received\n\t%v\nwant\n\t%v", node, got, runJobFixtureForm)
		}
	}

	wantTasks := []BackupJobRunTask{{Node: "pve-01", UPID: runUPID01}, {Node: "pve-04", UPID: runUPID04}}
	if !reflect.DeepEqual(run.Tasks, wantTasks) {
		t.Errorf("Tasks = %+v, want %+v", run.Tasks, wantTasks)
	}
	if want := []string{"pve-02"}; !slices.Equal(run.Skipped, want) {
		t.Errorf("Skipped = %v, want %v", run.Skipped, want)
	}
	// Certain failures: the two nodes that were never asked.
	checkRunFailures(t, "Failed", run.Failed, []runFailureCheck{{"pve-05", "unknown"}, {"pve-07", "offline"}})
	// Asked, and the answer did not say whether a backup started.
	checkRunFailures(t, "Unconfirmed", run.Unconfirmed,
		[]runFailureCheck{{"pve-03", "unrecognised"}, {"pve-06", "untrackable"}})
	if run.Sent != 5 {
		t.Errorf("Sent = %d, want the 5 nodes that were asked", run.Sent)
	}
	if run.StopsRunningBackups {
		t.Error("StopsRunningBackups is set for a job without stop")
	}
}

// TestRunBackupJobSortsEachFailedRequest classifies one answer per case: a
// refusal means no worker was forked there, so the node "could not start";
// anything else leaves it unconfirmed, because a worker may be running (see
// vzdumpRefused for the source behind each status).
func TestRunBackupJobSortsEachFailedRequest(t *testing.T) {
	status := func(code int, body string) func(http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}
	}
	for _, tc := range []struct {
		name    string
		answer  func(http.ResponseWriter)
		refused bool
		// check, when set, asserts something further about the one error.
		check func(t *testing.T, err error, hostPort string)
	}{
		{"a parameter rejection", status(http.StatusBadRequest,
			`{"data":null,"errors":{"storage":"fixture: no such storage"}}`), true, nil},
		{"a permission refusal", status(http.StatusForbidden, `{"data":null}`), true, nil},
		{"a missing path", status(http.StatusNotFound, `{"data":null}`), true, nil},
		{"no handler at all", status(http.StatusNotImplemented, `{"data":null}`), true, nil},
		// A die can come after the fork: fork_worker's parent still takes the
		// task-list lock once the child is released.
		{"a die", status(http.StatusInternalServerError,
			`{"data":null,"message":"fixture: task list lock timed out\n"}`), false, nil},
		{"a proxy's bad gateway", status(http.StatusBadGateway, "<html>bad gateway</html>"), false, nil},
		// pveproxy could not connect to the node that runs it: AnyEvent::HTTP
		// had not written the request yet.
		{"pveproxy could not reach the node", status(595, ""), true, nil},
		{"pveproxy's own timeout", status(596, ""), false, nil},
		// 599 is raised after the request went too — "Invalid server
		// response", "Garbled response headers".
		{"pveproxy's other failure", status(599, ""), false, nil},
		{"a body cut off mid-read", runJobCutOff, false, func(t *testing.T, err error, hostPort string) {
			// The case the handler's fixed sentence exists for: the headers
			// arrived, the body read failed, and the transport's text names
			// the socket — so it must not travel on as it is.
			if errors.Is(err, ErrConnectionFailed) || !strings.Contains(err.Error(), "read response body") ||
				!strings.Contains(err.Error(), hostPort) {
				t.Errorf("err = %v, want the body-read failure, naming %s", err, hostPort)
			}
		}},
		{"a dropped connection", func(w http.ResponseWriter) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				panic(err)
			}
			_ = conn.Close()
		}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pve := &runJobPVE{
				job:     runJobFixture,
				nodes:   `[{"node": "pve-01", "status": "online"}]`,
				answers: map[string]func(http.ResponseWriter){"pve-01": tc.answer},
			}
			run, err := runJobClient(t, pve).RunBackupJob(context.Background(), runJobID)
			if err != nil {
				t.Fatalf("RunBackupJob: %v", err)
			}
			if got := pve.postedNodes(); !slices.Equal(got, []string{"pve-01"}) {
				t.Fatalf("vzdump was posted to %v, want pve-01", got)
			}
			list, other := run.Unconfirmed, run.Failed
			if tc.refused {
				list, other = run.Failed, run.Unconfirmed
			}
			if len(list) != 1 || list[0].Node != "pve-01" || list[0].Err == nil || len(other) != 0 ||
				len(run.Tasks) != 0 || len(run.Skipped) != 0 {
				t.Errorf("refused=%v: Failed = %+v, Unconfirmed = %+v, Tasks = %+v, Skipped = %v",
					tc.refused, run.Failed, run.Unconfirmed, run.Tasks, run.Skipped)
			}
			if run.Sent != 1 {
				t.Errorf("Sent = %d, want 1: the request went out", run.Sent)
			}
			if tc.check != nil && len(list) == 1 {
				tc.check(t, list[0].Err, pve.srv.Listener.Addr().String())
			}
		})
	}
}

// TestStartVzdumpCountsAFailedHandshakeAsNeverSent holds startVzdump's claim
// that "never sent" reaches past the dial: a TLS handshake refused by the
// fingerprint pin fails before the request is written, so it is never sent
// either — and the server confirms nothing arrived.
func TestStartVzdumpCountsAFailedHandshakeAsNeverSent(t *testing.T) {
	var arrived atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrived.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":"` + runUPID01 + `"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(ClientConfig{
		BaseURL:     srv.URL,
		TokenID:     "user@pam!test",
		TokenSecret: "secret-token-value",
		// A synthetic pin the stand-in's certificate cannot match.
		TLSFingerprint: "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.startVzdump(context.Background(), "pve-01", url.Values{"all": {"1"}})
	if !errors.Is(err, ErrRequestNotSent) || !errors.Is(err, ErrConnectionFailed) ||
		!strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Errorf("startVzdump = %v, want a handshake refused by the pin, reported as never sent", err)
	}
	if n := arrived.Load(); n != 0 {
		t.Errorf("%d requests arrived at the server behind a refused handshake", n)
	}
}

// TestRunBackupJobCountsANodeItCouldNotConnectToAsNeverSent: a request no
// connection could be made for never left this process, so that node started
// nothing, for certain, and nothing was sent — it is not "check the task list
// first", and it is not a request to audit.
func TestRunBackupJobCountsANodeItCouldNotConnectToAsNeverSent(t *testing.T) {
	pve := &runJobPVE{
		job:                     runJobFixture,
		nodes:                   `[{"node": "pve-01", "status": "online"}]`,
		replies:                 map[string]string{"pve-01": `"` + runUPID01 + `"`},
		stopListeningAfterNodes: true,
	}
	run, err := runJobClient(t, pve).RunBackupJob(context.Background(), runJobID)
	if err != nil {
		t.Fatalf("RunBackupJob: %v", err)
	}
	// The precondition: the POST really could not connect, rather than riding
	// a connection left over from the reads.
	if got := pve.postedNodes(); len(got) != 0 {
		t.Fatalf("vzdump was posted to %v; the listener was closed before any POST", got)
	}
	if len(run.Failed) != 1 || run.Failed[0].Node != "pve-01" ||
		!errors.Is(run.Failed[0].Err, ErrRequestNotSent) || !errors.Is(run.Failed[0].Err, ErrConnectionFailed) {
		t.Errorf("Failed = %+v, want pve-01 as a request that was never sent", run.Failed)
	}
	if len(run.Unconfirmed) != 0 || len(run.Tasks) != 0 || len(run.Skipped) != 0 {
		t.Errorf("Unconfirmed = %+v, Tasks = %+v, Skipped = %v, want none", run.Unconfirmed, run.Tasks, run.Skipped)
	}
	if run.Sent != 0 {
		t.Errorf("Sent = %d, want 0: nothing left this process", run.Sent)
	}
}

// TestRunBackupJobNeverSendsToANodeNameItCannotPutInAPath: GET /nodes is
// Proxmox's word, but a name from it still becomes a path segment, and
// startVzdump's guard is what keeps it there. Such a node is never asked, is a
// certain failure rather than an unconfirmed one, and is not counted as sent.
func TestRunBackupJobNeverSendsToANodeNameItCannotPutInAPath(t *testing.T) {
	pve := &runJobPVE{
		job: runJobFixture,
		nodes: `[
			{"node": "pve-0\n1", "status": "online"},
			{"node": "a/b", "status": "online"},
			{"node": "pve-01", "status": "online"}
		]`,
		replies: map[string]string{
			"pve-01":   `"` + runUPID01 + `"`,
			"a/b":      `"UPID:a:0:0:0:vzdump::user@pam!test:"`,
			"pve-0\n1": `"UPID:pve-0:0:0:0:vzdump::user@pam!test:"`,
		},
	}
	run, err := runJobClient(t, pve).RunBackupJob(context.Background(), runJobID)
	if err != nil {
		t.Fatalf("RunBackupJob: %v", err)
	}
	pve.mu.Lock()
	calls := slices.Clone(pve.calls)
	pve.mu.Unlock()
	for _, call := range calls {
		if strings.HasPrefix(call, "POST ") && !strings.HasSuffix(call, "/nodes/pve-01/vzdump") {
			t.Errorf("sent %s", call)
		}
	}
	if !reflect.DeepEqual(run.Tasks, []BackupJobRunTask{{Node: "pve-01", UPID: runUPID01}}) {
		t.Errorf("Tasks = %+v, want pve-01's alone", run.Tasks)
	}
	checkRunFailures(t, "Failed", run.Failed, []runFailureCheck{{"a/b", "unsendable"}, {"pve-0\n1", "unsendable"}})
	if len(run.Unconfirmed) != 0 {
		t.Errorf("Unconfirmed = %+v, want none", run.Unconfirmed)
	}
	if run.Sent != 1 {
		t.Errorf("Sent = %d, want 1", run.Sent)
	}
}

// TestRunBackupJobRunsAPinnedJobOnItsNodeAlone is the pinned half: the job's
// node is the only target, it is expressed as the path rather than sent in the
// body, and a pinned node that is not online refuses the whole run before
// anything is sent — which is what run_backup_now does.
func TestRunBackupJobRunsAPinnedJobOnItsNodeAlone(t *testing.T) {
	pinnedJob := strings.Replace(runJobFixture, `"type": "vzdump",`, `"type": "vzdump", "node": "pve-02",`, 1)
	if !strings.Contains(pinnedJob, `"node": "pve-02"`) {
		t.Fatal("the fixture edit did not land; the pinned cases would test an unpinned job")
	}
	nodes := `[
		{"node": "pve-01", "status": "online"},
		{"node": "pve-02", "status": "online"},
		{"node": "pve-03", "status": "offline"}
	]`
	replies := map[string]string{
		"pve-01": `"` + runUPID01 + `"`,
		"pve-02": `"UPID:pve-02:0000D7E8:00000002:66F1A2B6:vzdump::user@pam!test:"`,
	}

	t.Run("online", func(t *testing.T) {
		pve := &runJobPVE{job: pinnedJob, nodes: nodes, replies: replies}
		run, err := runJobClient(t, pve).RunBackupJob(context.Background(), runJobID)
		if err != nil {
			t.Fatalf("RunBackupJob: %v", err)
		}
		if got := pve.postedNodes(); !slices.Equal(got, []string{"pve-02"}) {
			t.Fatalf("vzdump was posted to %v, want the job's node alone", got)
		}
		// The exact form proves node is absent: vzdump's node is its path
		// parameter, and a body value is at best redundant.
		if got := pve.onlyPost(t, "pve-02"); !reflect.DeepEqual(got, runJobFixtureForm) {
			t.Errorf("pve-02 received\n\t%v\nwant\n\t%v", got, runJobFixtureForm)
		}
		if len(run.Tasks) != 1 || run.Tasks[0].Node != "pve-02" || len(run.Skipped) != 0 ||
			len(run.Failed) != 0 || len(run.Unconfirmed) != 0 || run.Sent != 1 {
			t.Errorf("run = %+v, want one task on pve-02 and nothing else", run)
		}
	})

	for _, tc := range []struct {
		name, node, wantStatus string
	}{
		{"offline", "pve-03", "offline"},
		{"not listed", "pve-09", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := strings.Replace(pinnedJob, `"node": "pve-02"`, `"node": "`+tc.node+`"`, 1)
			pve := &runJobPVE{job: job, nodes: nodes, replies: replies}
			run, err := runJobClient(t, pve).RunBackupJob(context.Background(), runJobID)
			var offline *NodeNotOnlineError
			if !errors.As(err, &offline) || offline.Node != tc.node || offline.Status != tc.wantStatus {
				t.Fatalf("RunBackupJob = (%+v, %v), want a NodeNotOnlineError for %s with status %q",
					run, err, tc.node, tc.wantStatus)
			}
			if got := pve.postedNodes(); len(got) != 0 {
				t.Errorf("vzdump was posted to %v; a pinned node that is not online refuses the whole run", got)
			}
		})
	}
}

// vzdumpParameters is every property of vzdump's $confdesc, transcribed from
// pve-guest-common src/PVE/VZDump/Common.pm — the parameters a job carries
// (PVE::VZDump::JobBase's options are exactly these plus enabled, schedule,
// comment and repeat-missed) and vzdump's own schema (PVE/API2/VZDump.pm adds
// only job-id and stdout, which a job cannot carry). Each has the JSON value
// read_job would return and the form value it must reach vzdump as.
//
// Distinct values, so one landing under another key cannot pass — except that
// a boolean has only two, and six share them.
var vzdumpParameters = []struct {
	key, value string
	want       []string // nil: the key must not be in the form
}{
	{"vmid", `"100,101"`, []string{"100,101"}},
	// The job's node is where the run goes, not what it sends: the path's
	// node is vzdump's (proxyto => 'node'), so a body copy is refused when
	// it differs.
	{"node", `"pve-01"`, nil},
	{"all", `1`, []string{"1"}},
	{"stdexcludes", `0`, []string{"0"}},
	{"compress", `"zstd"`, []string{"zstd"}},
	{"pigz", `3`, []string{"3"}},
	{"zstd", `5`, []string{"5"}},
	{"quiet", `1`, []string{"1"}},
	{"mode", `"suspend"`, []string{"suspend"}},
	{"exclude", `"102"`, []string{"102"}},
	{"exclude-path", `["/tmp/?*", "/var/log/?*"]`, []string{"/tmp/?*", "/var/log/?*"}},
	{"mailto", `"ops@example.com"`, []string{"ops@example.com"}},
	{"mailnotification", `"failure"`, []string{"failure"}},
	{"notification-mode", `"notification-system"`, []string{"notification-system"}},
	{"tmpdir", `"/var/tmp/vzdump"`, []string{"/var/tmp/vzdump"}},
	{"dumpdir", `"/mnt/dump"`, []string{"/mnt/dump"}},
	{"script", `"/usr/local/bin/hook.pl"`, []string{"/usr/local/bin/hook.pl"}},
	{"storage", `"store01"`, []string{"store01"}},
	{"stop", `1`, []string{"1"}},
	{"bwlimit", `20480`, []string{"20480"}},
	{"ionice", `6`, []string{"6"}},
	{"performance", `{"max-workers": "12", "pbs-entries-max": "4194304"}`,
		[]string{"max-workers=12,pbs-entries-max=4194304"}},
	{"fleecing", `{"enabled": 1, "storage": "store02"}`, []string{"enabled=1,storage=store02"}},
	{"lockwait", `170`, []string{"170"}},
	{"stopwait", `9`, []string{"9"}},
	{"prune-backups", `{"keep-last": "2", "keep-weekly": "5"}`, []string{"keep-last=2,keep-weekly=5"}},
	// remove defaults to 1 (prune after the backup), so dropping a job's 0
	// would prune on Run now what the schedule never prunes.
	{"remove", `0`, []string{"0"}},
	{"pool", `"pool01"`, []string{"pool01"}},
	{"notes-template", `"{{vmid}} {{guestname}}"`, []string{"{{vmid}} {{guestname}}"}},
	{"protected", `1`, []string{"1"}},
	{"pbs-change-detection-mode", `"metadata"`, []string{"metadata"}},
}

// TestRunBackupJobForwardsEveryVzdumpParameter is the other half of the
// deny-list decision: every parameter vzdump takes reaches it verbatim, one
// subtest each, so a key added to backupJobRunDropped — remove, pool — fails
// by name.
func TestRunBackupJobForwardsEveryVzdumpParameter(t *testing.T) {
	if len(vzdumpParameters) != 31 {
		t.Fatalf("the table has %d parameters; Common.pm's $confdesc has 31", len(vzdumpParameters))
	}
	fields := make([]string, 0, 2+len(vzdumpParameters))
	fields = append(fields, `"id": "`+runJobID+`"`, `"type": "vzdump"`)
	for _, p := range vzdumpParameters {
		fields = append(fields, `"`+p.key+`": `+p.value)
	}
	pve := &runJobPVE{
		job:     "{" + strings.Join(fields, ", ") + "}",
		nodes:   `[{"node": "pve-01", "status": "online"}]`,
		replies: map[string]string{"pve-01": `"` + runUPID01 + `"`},
	}
	run, err := runJobClient(t, pve).RunBackupJob(context.Background(), runJobID)
	if err != nil {
		t.Fatalf("RunBackupJob: %v", err)
	}
	form := pve.onlyPost(t, "pve-01")

	want := url.Values{}
	for _, p := range vzdumpParameters {
		t.Run(p.key, func(t *testing.T) {
			got, sent := form[p.key]
			if p.want == nil {
				if sent {
					t.Errorf("%s reached vzdump as %q", p.key, got)
				}
				return
			}
			if !slices.Equal(got, p.want) {
				t.Errorf("%s reached vzdump as %q (sent=%v), want %q", p.key, got, sent, p.want)
			}
		})
		if p.want != nil {
			want[p.key] = p.want
		}
	}
	// And nothing else: the job's id and type are not vzdump's.
	if !reflect.DeepEqual(form, want) {
		t.Errorf("form = %v\nwant %v", form, want)
	}
	if !run.StopsRunningBackups {
		t.Error("StopsRunningBackups is unset for a job with stop 1")
	}
}

// TestBackupJobRunDroppedIsRunBackupNowsList pins the deny-list to the ten
// keys run_backup_now deletes (pve-manager www/manager6/dc/Backup.js) — no
// more, which would silently drop a setting, and no fewer, which would send
// vzdump a key it refuses.
func TestBackupJobRunDroppedIsRunBackupNowsList(t *testing.T) {
	gui := []string{
		"enabled", "starttime", "dow", "id", "schedule", "type", "node", "comment", "next-run", "repeat-missed",
	}
	if got, want := slices.Sorted(slices.Values(backupJobRunDropped)), slices.Sorted(slices.Values(gui)); !slices.Equal(got, want) {
		t.Errorf("backupJobRunDropped = %v, want run_backup_now's %v", got, want)
	}
}

// TestBackupJobRunFormDropsWhatRunBackupNowDeletes checks the deny-list one
// key at a time, against a job that carries a real selection so each case
// differs from the baseline by that key alone.
func TestBackupJobRunFormDropsWhatRunBackupNowDeletes(t *testing.T) {
	for _, key := range []string{
		"enabled", "starttime", "dow", "id", "schedule", "type", "node", "comment", "next-run", "repeat-missed",
	} {
		t.Run(key, func(t *testing.T) {
			raw := `{"vmid": "100", "storage": "store01", "` + key + `": "x1"}`
			plan, err := backupJobRunForm(json.RawMessage(raw))
			if err != nil {
				t.Fatalf("backupJobRunForm: %v", err)
			}
			if _, sent := plan.form[key]; sent {
				t.Errorf("%s reached vzdump as %q; run_backup_now deletes it", key, plan.form[key])
			}
			if plan.form.Get("vmid") != "100" || plan.form.Get("storage") != "store01" {
				t.Errorf("the rest of the job did not survive: %v", plan.form)
			}
		})
	}
}

// TestBackupJobRunFormForwardsWhatItDoesNotKnow pins the deny-list decision
// from the other side: a key Nexara has never heard of is sent, so that vzdump
// — whose schema allows no additional properties — refuses it by name rather
// than a backup quietly running without it. An object under an unknown key is
// printed as the property string it would have been stored as.
func TestBackupJobRunFormForwardsWhatItDoesNotKnow(t *testing.T) {
	raw := `{"vmid": "100", "future-option": "on-demand", "future-format": {"zeta": 2, "alpha": "a1"}}`
	plan, err := backupJobRunForm(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("backupJobRunForm: %v", err)
	}
	want := url.Values{
		"vmid":          {"100"},
		"all":           {"0"},
		"future-option": {"on-demand"},
		"future-format": {"alpha=a1,zeta=2"},
	}
	if !reflect.DeepEqual(plan.form, want) {
		t.Errorf("form = %v, want %v", plan.form, want)
	}
}

// TestBackupJobRunFormSendsAllAsDigits covers the one key run_backup_now always
// sends: a boolean API parameter takes only "1" and "0", and the job may carry
// all as a number, a JSON boolean, a string, or not at all.
func TestBackupJobRunFormSendsAllAsDigits(t *testing.T) {
	for _, tc := range []struct {
		name, all, want string
	}{
		{"absent", ``, "0"},
		{"number 1", `, "all": 1`, "1"},
		{"number 0", `, "all": 0`, "0"},
		{"true", `, "all": true`, "1"},
		{"false", `, "all": false`, "0"},
		{"string 1", `, "all": "1"`, "1"},
		{"string yes", `, "all": "Yes"`, "1"},
		{"string 0", `, "all": "0"`, "0"},
		{"null", `, "all": null`, "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := backupJobRunForm(json.RawMessage(`{"pool": "pool01"` + tc.all + `}`))
			if err != nil {
				t.Fatalf("backupJobRunForm: %v", err)
			}
			if got := plan.form["all"]; !slices.Equal(got, []string{tc.want}) {
				t.Errorf("all = %q, want [%q]", got, tc.want)
			}
		})
	}
}

// TestBackupJobRunFormReadsTheStopFlag: stop is sent like any other parameter,
// and is also what tells the caller that a node answering "OK" may have had a
// running backup stopped.
func TestBackupJobRunFormReadsTheStopFlag(t *testing.T) {
	for _, tc := range []struct {
		name, stop string
		want       bool
	}{
		{"absent", ``, false},
		{"0", `, "stop": 0`, false},
		{"1", `, "stop": 1`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := backupJobRunForm(json.RawMessage(`{"vmid": "100"` + tc.stop + `}`))
			if err != nil {
				t.Fatalf("backupJobRunForm: %v", err)
			}
			if plan.stops != tc.want {
				t.Errorf("stops = %v, want %v", plan.stops, tc.want)
			}
		})
	}
}

// TestBackupJobRunFormRefusesWhatItCannotSpell holds the loud-failure line: a
// value with no form spelling stops the whole run rather than being dropped.
func TestBackupJobRunFormRefusesWhatItCannotSpell(t *testing.T) {
	for _, tc := range []struct {
		name, job string
	}{
		{"a property-string value with a comma", `{"vmid": "100", "fleecing": {"storage": "store01,x"}}`},
		{"a nested object in a property string", `{"vmid": "100", "performance": {"max-workers": {"n": 1}}}`},
		{"an array of objects", `{"vmid": "100", "exclude-path": [{"p": "/tmp"}]}`},
		{"a node that is not a string", `{"vmid": "100", "node": 7}`},
		{"no job at all", `null`},
		{"not an object", `["vmid"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if plan, err := backupJobRunForm(json.RawMessage(tc.job)); !errors.Is(err, ErrInvalidResponse) {
				t.Errorf("backupJobRunForm(%s) = (%v, %v), want an ErrInvalidResponse", tc.job, plan.form, err)
			}
		})
	}

	// And end to end: the refusal lands before a single node is asked.
	pve := &runJobPVE{
		job:     `{"vmid": "100", "fleecing": {"storage": "store01,x"}}`,
		nodes:   `[{"node": "pve-01", "status": "online"}]`,
		replies: map[string]string{"pve-01": `"` + runUPID01 + `"`},
	}
	if _, err := runJobClient(t, pve).RunBackupJob(context.Background(), runJobID); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("RunBackupJob = %v, want an ErrInvalidResponse", err)
	}
	if got := pve.postedNodes(); len(got) != 0 {
		t.Errorf("vzdump was posted to %v for a job that could not be spelled", got)
	}
}

// TestRunBackupJobRefusesAClusterWithNoNodes: with nothing to send to, the run
// neither posts nor claims there was nothing to back up.
func TestRunBackupJobRefusesAClusterWithNoNodes(t *testing.T) {
	pve := &runJobPVE{job: runJobFixture, nodes: `[]`}
	run, err := runJobClient(t, pve).RunBackupJob(context.Background(), runJobID)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("RunBackupJob = (%+v, %v), want an ErrInvalidResponse", run, err)
	}
	if got := pve.postedNodes(); len(got) != 0 {
		t.Errorf("vzdump was posted to %v", got)
	}
}

// TestPrintPropertyStringSpellsEveryValue covers the values a parsed property
// string can hold beyond the fixture's: a boolean as the digits, a number where
// PVE would send text, and a null or an empty value left out —
// parse_property_string would refuse "key=".
func TestPrintPropertyStringSpellsEveryValue(t *testing.T) {
	for _, tc := range []struct {
		name, obj, want string
	}{
		{"booleans as digits", `{"b-off": false, "a-on": true}`, "a-on=1,b-off=0"},
		{"an empty value left out", `{"storage": "", "enabled": 1}`, "enabled=1"},
		{"a null left out", `{"storage": null, "enabled": 1}`, "enabled=1"},
		{"numbers as sent", `{"keep-last": 3, "keep-daily": "07"}`, "keep-daily=07,keep-last=3"},
		{"nothing left", `{"storage": ""}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var obj map[string]any
			dec := json.NewDecoder(strings.NewReader(tc.obj))
			dec.UseNumber()
			if err := dec.Decode(&obj); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			got, err := printPropertyString(obj)
			if err != nil || got != tc.want {
				t.Errorf("printPropertyString(%s) = (%q, %v), want %q", tc.obj, got, err, tc.want)
			}
		})
	}

	// A property string that prints empty is not sent at all, rather than as
	// an empty value.
	plan, err := backupJobRunForm(json.RawMessage(`{"vmid": "100", "fleecing": {"storage": ""}}`))
	if err != nil {
		t.Fatalf("backupJobRunForm: %v", err)
	}
	if _, sent := plan.form["fleecing"]; sent {
		t.Errorf("fleecing reached vzdump as %q", plan.form["fleecing"])
	}
}

// TestRunBackupJobGuardsTheJobID keeps the id inside its one path segment. The
// route anchors it too; this is the client's own refusal, and nothing may reach
// the wire.
func TestRunBackupJobGuardsTheJobID(t *testing.T) {
	for _, id := range []string{"", ".", "..", "backup-1/../..", "a\nb"} {
		pve := &runJobPVE{job: runJobFixture, nodes: runJobNodes}
		_, err := runJobClient(t, pve).RunBackupJob(context.Background(), id)
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("RunBackupJob(%q) = %v, want ErrInvalidInput", id, err)
		}
		pve.mu.Lock()
		if len(pve.calls) != 0 {
			t.Errorf("RunBackupJob(%q) sent %v", id, pve.calls)
		}
		pve.mu.Unlock()
	}
}
