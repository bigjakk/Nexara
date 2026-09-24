package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// This file keeps the REAL POST .../backup-jobs/:job_id/run declaration, its
// permission, the real BackupHandler.RunBackupJob and the real proxmox.Client
// together, with stand-ins only at the two edges: Proxmox and the database. It
// is where "every task a run starts is tracked, on its own node" is proven —
// the per-function TrackTask guard (tracktask_guard_test.go) cannot count —
// along with the run's summary audit row and the exact answer.

const (
	backupRunEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	backupRunJobID  = "backup-1a2b3c4d"

	backupRunUPID01 = "UPID:pve-01:0000A1B2:00C0FFEE:66F1A2B3:vzdump:101:user@pam!test:"
	backupRunUPID04 = "UPID:pve-04:0000B3C4:00BADA55:66F1A2B4:vzdump::user@pam!test:"

	// backupRunRefusal is a vzdump parameter rejection, as PVE sends one: a
	// 400 with a per-field map — a certain "no backup started". Its message
	// carries a tab and ends in a newline, as a rejection carrying a die()
	// message does, and parseProxmoxError keeps both: the answer passes them
	// on, and the summary row, which every Viewer reads, must not.
	backupRunRefusal = `{"data":null,"errors":{"storage":"fixture: no such\tstorage\n"}}`
)

// backupRunForbidden is a 403 as pveproxy answers one for a failed permission
// check, and backupRunForbiddenMessage what mapProxmoxError makes of it:
// checkStatus keeps a 403's whole body as its text.
const (
	backupRunForbidden        = `{"data":null,"message":"Permission check failed (/storage/store01, Datastore.AllocateSpace)\n"}`
	backupRunForbiddenMessage = "Proxmox API: start backup on pve-02: forbidden: " + backupRunForbidden
)

// backupRunDoneMessage is what a reply of "done" renders as: the client's
// invalid-response error, through mapProxmoxError's catch-all.
const backupRunDoneMessage = `Proxmox operation failed: invalid response: vzdump on pve-02 answered "done", ` +
	`which is neither a task id nor "OK"`

// backupRunUnsendableMessage is the client's own refusal of the node name
// "a/b", which mapProxmoxError passes on as the caller's bad input.
const backupRunUnsendableMessage = `invalid input: node name "a/b" must not contain a path separator`

var errBackupRunUnexpectedQuery = errors.New("unexpected query")

// backupRunPVE stands in for the cluster: the job read, the node list, and one
// vzdump answer per node — a 200 reply from replies, or anything else from
// answers. A node in neither answers 500 with a message, as a die in vzdump
// does.
type backupRunPVE struct {
	t       *testing.T
	job     string
	nodes   string
	replies map[string]string
	answers map[string]func(http.ResponseWriter)

	// stopListeningAfterNodes closes the listener once GET /nodes has been
	// answered, and that connection with it, so no vzdump POST can connect.
	stopListeningAfterNodes bool

	srv    *httptest.Server
	mu     sync.Mutex
	posted []string
	// host is the stand-in's address, the host both ends of every socket
	// share here, which must never reach an answer or an audit row.
	host string
}

func (p *backupRunPVE) serve(t *testing.T) string {
	t.Helper()
	p.t = t
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(status int, body string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
		switch path := r.URL.Path; {
		case r.Method == http.MethodGet && path == "/api2/json/cluster/backup/"+backupRunJobID:
			write(http.StatusOK, `{"data":`+p.job+`}`)
		case r.Method == http.MethodGet && path == "/api2/json/nodes":
			if p.stopListeningAfterNodes {
				w.Header().Set("Connection", "close")
				_ = p.srv.Listener.Close()
			}
			write(http.StatusOK, `{"data":`+p.nodes+`}`)
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/vzdump"):
			node := strings.TrimSuffix(strings.TrimPrefix(path, "/api2/json/nodes/"), "/vzdump")
			p.mu.Lock()
			p.posted = append(p.posted, node)
			p.mu.Unlock()
			if reply, ok := p.replies[node]; ok {
				write(http.StatusOK, `{"data":`+reply+`}`)
				return
			}
			if answer, ok := p.answers[node]; ok {
				answer(w)
				return
			}
			write(http.StatusInternalServerError, `{"data":null,"message":"fixture: vzdump died on `+node+`\n"}`)
		default:
			p.t.Errorf("stand-in Proxmox: unexpected %s %s", r.Method, r.URL.RequestURI())
			write(http.StatusNotImplemented, `{"data":null}`)
		}
	}))
	p.srv = srv
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("stand-in URL: %v", err)
	}
	p.host = u.Hostname()
	return srv.URL
}

func (p *backupRunPVE) postedTo() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := slices.Clone(p.posted)
	slices.Sort(out)
	return out
}

// backupRunStatus answers with a status and a body.
func backupRunStatus(code int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
}

// backupRunCutOff answers 200 and resets the connection partway through the
// body. The transport's error for that names both ends of the socket —
// "read tcp 127.0.0.1:…->127.0.0.1:…: read: connection reset by peer" — and
// is the error kind the handler must not repeat (the proxmox package's
// TestRunBackupJobSortsEachFailedRequest checks the error is that one).
func backupRunCutOff(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", "100")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"data":"UPID:`))
	w.(http.Flusher).Flush()
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		panic(err)
	}
	// Long enough for the client to have read the headers, so the reset lands
	// in the body read.
	time.Sleep(200 * time.Millisecond)
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0) // close with a reset, not a FIN
	}
	_ = conn.Close()
}

// backupRunDropped closes the connection without an answer: the request
// arrived, and nothing says what came of it.
func backupRunDropped(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		panic(err)
	}
	_ = conn.Close()
}

type backupRunCacheQueries struct{ cluster db.Cluster }

func (q backupRunCacheQueries) GetCluster(_ context.Context, id uuid.UUID) (db.Cluster, error) {
	if id != q.cluster.ID {
		return db.Cluster{}, pgx.ErrNoRows
	}
	return q.cluster, nil
}

func (backupRunCacheQueries) GetPBSServer(context.Context, uuid.UUID) (db.PbsServer, error) {
	return db.PbsServer{}, pgx.ErrNoRows
}

func (backupRunCacheQueries) ListNodeEndpoints(context.Context, uuid.UUID) ([]db.ListNodeEndpointsRow, error) {
	return nil, nil
}

// backupRunDBTX records the audit inserts (Exec) and the task_history inserts
// (QueryRow) and fails anything else.
type backupRunDBTX struct {
	mu     sync.Mutex
	audits [][]any
	tasks  [][]any
	other  []string
}

func (d *backupRunDBTX) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !strings.Contains(sql, "INSERT INTO audit_log") {
		d.other = append(d.other, sql)
		return pgconn.CommandTag{}, errBackupRunUnexpectedQuery
	}
	d.audits = append(d.audits, args)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (d *backupRunDBTX) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.other = append(d.other, sql)
	return nil, errBackupRunUnexpectedQuery
}

func (d *backupRunDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.Contains(sql, "INTO task_history") {
		d.tasks = append(d.tasks, args)
	} else {
		d.other = append(d.other, sql)
	}
	// TrackTask discards the returned row, so an empty one is all it needs.
	return backupRunNoRow{}
}

type backupRunNoRow struct{}

func (backupRunNoRow) Scan(...any) error { return pgx.ErrNoRows }

// backupRunTaskRow is one task_history insert, read positionally from
// InsertTaskHistory's arguments: (cluster_id, user_id, upid, description,
// status, node, task_type).
type backupRunTaskRow struct {
	cluster                               uuid.UUID
	upid, description, status, node, kind string
}

// backupRunAuditRow is one audit insert, read positionally from
// InsertAuditLog's: (cluster_id, user_id, resource_type, resource_id, action,
// details).
type backupRunAuditRow struct {
	cluster                          pgtype.UUID
	resourceType, resourceID, action string
	details                          map[string]any
	raw                              string
}

// decodeBackupRunDetails decodes details the way every assertion here reads
// them: UseNumber, so a count reads back as its digits.
func decodeBackupRunDetails(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("not a JSON object (%s): %v", raw, err)
	}
	return out
}

// rows returns the task_history rows, and the audit rows split by action:
// TrackTask's per-task backup_job_run rows, and the run's summary rows.
func (d *backupRunDBTX) rows(t *testing.T) (tasks []backupRunTaskRow, taskAudits, summaries []backupRunAuditRow) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.other) != 0 {
		t.Fatalf("the handler sent statements it has no reason to: %q", d.other)
	}
	for _, args := range d.tasks {
		if len(args) != 7 {
			t.Fatalf("task_history insert took %d arguments, want InsertTaskHistory's 7", len(args))
		}
		row := backupRunTaskRow{}
		row.cluster, _ = args[0].(uuid.UUID)
		row.upid, _ = args[2].(string)
		row.description, _ = args[3].(string)
		row.status, _ = args[4].(string)
		row.node, _ = args[5].(string)
		row.kind, _ = args[6].(string)
		tasks = append(tasks, row)
	}
	for _, args := range d.audits {
		if len(args) != 6 {
			t.Fatalf("audit insert took %d arguments, want InsertAuditLog's 6", len(args))
		}
		row := backupRunAuditRow{}
		row.cluster, _ = args[0].(pgtype.UUID)
		row.resourceType, _ = args[2].(string)
		row.resourceID, _ = args[3].(string)
		row.action, _ = args[4].(string)
		raw, ok := args[5].(json.RawMessage)
		if !ok {
			t.Fatalf("audit details argument is %T, want json.RawMessage", args[5])
		}
		row.raw = string(raw)
		row.details = decodeBackupRunDetails(t, raw)
		switch row.action {
		case "backup_job_run":
			taskAudits = append(taskAudits, row)
		case "backup_job_run_requested":
			summaries = append(summaries, row)
		default:
			t.Fatalf("an audit row with action %q", row.action)
		}
	}
	// Sorted by node, so the assertions below do not depend on the order the
	// handler happened to record in.
	slices.SortFunc(tasks, func(a, b backupRunTaskRow) int { return strings.Compare(a.node, b.node) })
	slices.SortFunc(taskAudits, func(a, b backupRunAuditRow) int {
		an, _ := a.details["node"].(string)
		bn, _ := b.details["node"].(string)
		return strings.Compare(an, bn)
	})
	return tasks, taskAudits, summaries
}

// newBackupRunApp mounts the real run declaration, gated by its real
// permission, carrying the real handler wired to both stand-ins.
func newBackupRunApp(t *testing.T, pve *backupRunPVE) (*fiber.App, *backupRunDBTX) {
	t.Helper()
	baseURL := pve.serve(t)
	encrypted, err := crypto.Encrypt("token-secret-value", backupRunEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cache := proxmox.NewClientCache(backupRunCacheQueries{cluster: db.Cluster{
		ID:                   uuid.MustParse(testClusterID),
		Name:                 "cluster01",
		ApiUrl:               baseURL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
		IsActive:             true,
	}}, backupRunEncKey, nil, nil)

	dbtx := &backupRunDBTX{}
	e := declaredEndpoint(t, fiber.MethodPost, clusterScope+"/backup-jobs/:job_id/run")
	e.Handler = handlers.NewBackupHandler(db.New(dbtx), backupRunEncKey, nil).RunBackupJob

	// stubAuth sets the user TrackTask and AuditLog need — without one they
	// record nothing, and every "no rows" assertion below would pass for that
	// reason alone.
	authed := stubAuth(map[string]bool{"manage:backup": true})
	auth := func(c fiber.Ctx) error {
		handlers.SetProxmoxCacheLocal(c, cache)
		return authed(c)
	}
	return newRegistryApp(t, auth, e), dbtx
}

// runBackupJob sends the run and returns the status and the raw body.
func runBackupJob(t *testing.T, app *fiber.App) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, backupRoute(clusterScope+"/backup-jobs/:job_id/run"), nil)
	req.Header.Set("X-Test-User", "yes")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, body
}

// backupRunOneFailure is the expected summary of a run in which pve-01
// answered that it had nothing to back up and one other node failed: in errors
// when refused is set, in unconfirmed otherwise. Built with json.Marshal
// because the messages it is used for carry quotes and backslashes.
func backupRunOneFailure(node, message string, refused bool) string {
	one := []map[string]string{{"node": node, "message": message}}
	none := []map[string]string{}
	errs, unconfirmed := one, none
	if !refused {
		errs, unconfirmed = none, one
	}
	b, err := json.Marshal(map[string]any{
		"job_id":                backupRunJobID,
		"tasks":                 []any{},
		"skipped":               []string{"pve-01"},
		"errors":                errs,
		"unconfirmed":           unconfirmed,
		"stops_running_backups": false,
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// checkBackupRunSummary asserts the one summary row a run that sent anything
// writes: its resource, and details equal to want, key for key.
func checkBackupRunSummary(t *testing.T, summaries []backupRunAuditRow, want string) {
	t.Helper()
	if len(summaries) != 1 {
		t.Fatalf("the run wrote %d summary rows, want exactly 1", len(summaries))
	}
	got := summaries[0]
	clusterID := uuid.MustParse(testClusterID)
	if !got.cluster.Valid || uuid.UUID(got.cluster.Bytes) != clusterID ||
		got.resourceType != "backup" || got.resourceID != backupRunJobID {
		t.Errorf("summary row is (%v, %q, %q), want (%s, backup, %s)",
			got.cluster, got.resourceType, got.resourceID, clusterID, backupRunJobID)
	}
	if wantDetails := decodeBackupRunDetails(t, []byte(want)); !reflect.DeepEqual(got.details, wantDetails) {
		t.Errorf("summary details = %s\nwant %s", got.raw, want)
	}
}

// TestRunBackupJobTracksEveryTaskOnItsOwnNode is the reason the run was
// rebuilt: two nodes start tasks, and each is tracked — audit row,
// task_history row — under its own node and its own UPID, beside nodes that
// had nothing to do, were offline, refused, or did not answer. The run as a
// whole gets one summary row naming every node's outcome, and the answer is
// exact: every list present.
func TestRunBackupJobTracksEveryTaskOnItsOwnNode(t *testing.T) {
	pve := &backupRunPVE{
		job: `{"id":"` + backupRunJobID + `","type":"vzdump","schedule":"sat 02:00","all":1,"storage":"store01"}`,
		nodes: `[
			{"node":"pve-07","status":"online"},
			{"node":"pve-06","status":"online"},
			{"node":"pve-05","status":"online"},
			{"node":"pve-04","status":"online"},
			{"node":"pve-03","status":"offline"},
			{"node":"pve-02","status":"online"},
			{"node":"pve-01","status":"online"}
		]`,
		replies: map[string]string{
			"pve-01": `"` + backupRunUPID01 + `"`,
			"pve-02": `"OK"`,
			"pve-04": `"` + backupRunUPID04 + `"`,
		},
		answers: map[string]func(http.ResponseWriter){
			"pve-05": backupRunStatus(http.StatusBadRequest, backupRunRefusal),
			"pve-06": backupRunDropped,
			"pve-07": backupRunCutOff,
		},
	}
	app, dbtx := newBackupRunApp(t, pve)
	status, body := runBackupJob(t, app)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d (%s), want 200 — two tasks are running", status, body)
	}
	if got, want := pve.postedTo(), []string{"pve-01", "pve-02", "pve-04", "pve-05", "pve-06", "pve-07"}; !slices.Equal(got, want) {
		t.Fatalf("vzdump was posted to %v, want %v", got, want)
	}

	wantBody := `{"tasks":[{"node":"pve-01","upid":"` + backupRunUPID01 + `"},{"node":"pve-04","upid":"` +
		backupRunUPID04 + `"}],"skipped":["pve-02"],"errors":[` +
		`{"node":"pve-03","message":"node is not online (Proxmox reports it as offline)"},` +
		`{"node":"pve-05","message":"storage: fixture: no such\tstorage\n"}],` +
		`"unconfirmed":[{"node":"pve-06","message":"Failed to connect to Proxmox"},` +
		`{"node":"pve-07","message":"the answer from Proxmox could not be read"}],` +
		`"stops_running_backups":false}`
	if string(body) != wantBody {
		t.Errorf("answer = %s\nwant %s", body, wantBody)
	}

	tasks, taskAudits, summaries := dbtx.rows(t)
	clusterID := uuid.MustParse(testClusterID)
	wantTasks := []backupRunTaskRow{
		{clusterID, backupRunUPID01, "Run backup job " + backupRunJobID + " on pve-01", "running", "pve-01", "vzdump"},
		{clusterID, backupRunUPID04, "Run backup job " + backupRunJobID + " on pve-04", "running", "pve-04", "vzdump"},
	}
	if !reflect.DeepEqual(tasks, wantTasks) {
		t.Errorf("task_history rows = %+v\nwant %+v", tasks, wantTasks)
	}

	if len(taskAudits) != 2 {
		t.Fatalf("the run wrote %d task audit rows, want one per task started (2)", len(taskAudits))
	}
	for i, want := range []struct {
		node, upid string
		vmid       any
	}{
		// vzdump names the one guest a single-guest task backs up in the UPID's
		// id field, and TrackTask lifts it into the row; pve-04's task names
		// none, so its row carries none.
		{"pve-01", backupRunUPID01, json.Number("101")},
		{"pve-04", backupRunUPID04, nil},
	} {
		got := taskAudits[i]
		if !got.cluster.Valid || uuid.UUID(got.cluster.Bytes) != clusterID ||
			got.resourceType != "backup" || got.resourceID != backupRunJobID {
			t.Errorf("task audit row %d = (%v, %q, %q), want (%s, backup, %s)",
				i, got.cluster, got.resourceType, got.resourceID, clusterID, backupRunJobID)
		}
		wantDetails := map[string]any{"upid": want.upid, "node": want.node, "job_id": backupRunJobID}
		if want.vmid != nil {
			wantDetails["vmid"] = want.vmid
		}
		if !reflect.DeepEqual(got.details, wantDetails) {
			t.Errorf("task audit row %d details = %v, want %v", i, got.details, wantDetails)
		}
	}

	checkBackupRunSummary(t, summaries, `{
		"job_id": "`+backupRunJobID+`",
		"tasks": [{"node":"pve-01","upid":"`+backupRunUPID01+`"},{"node":"pve-04","upid":"`+backupRunUPID04+`"}],
		"skipped": ["pve-02"],
		"errors": [
			{"node":"pve-03","message":"node is not online (Proxmox reports it as offline)"},
			{"node":"pve-05","message":"storage: fixture: no such storage"}
		],
		"unconfirmed": [
			{"node":"pve-06","message":"Failed to connect to Proxmox"},
			{"node":"pve-07","message":"the answer from Proxmox could not be read"}
		],
		"stops_running_backups": false
	}`)

	// Both ends of the socket are in the transport errors the dropped and the
	// cut-off connections produced; neither may reach the answer or the row a
	// Viewer reads.
	if strings.Contains(string(body), pve.host) || strings.Contains(summaries[0].raw, pve.host) {
		t.Errorf("the stand-in's address %s leaked into the answer or the summary row", pve.host)
	}
}

// TestRunBackupJobAnswersEveryListEvenWhenEmpty: an all-guests job that
// starts a task on every node has nothing to put in the other lists, and each
// of them must still be [] — the SPA reads their lengths while backups run.
func TestRunBackupJobAnswersEveryListEvenWhenEmpty(t *testing.T) {
	pve := &backupRunPVE{
		job:     `{"id":"` + backupRunJobID + `","all":1}`,
		nodes:   `[{"node":"pve-01","status":"online"}]`,
		replies: map[string]string{"pve-01": `"` + backupRunUPID01 + `"`},
	}
	app, dbtx := newBackupRunApp(t, pve)
	status, body := runBackupJob(t, app)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d (%s), want 200", status, body)
	}
	want := `{"tasks":[{"node":"pve-01","upid":"` + backupRunUPID01 + `"}],"skipped":[],"errors":[],` +
		`"unconfirmed":[],"stops_running_backups":false}`
	if string(body) != want {
		t.Errorf("answer = %s\nwant %s", body, want)
	}
	_, _, summaries := dbtx.rows(t)
	checkBackupRunSummary(t, summaries, `{"job_id":"`+backupRunJobID+`","tasks":[{"node":"pve-01","upid":"`+
		backupRunUPID01+`"}],"skipped":[],"errors":[],"unconfirmed":[],"stops_running_backups":false}`)
}

// TestRunBackupJobWithNoTaskAnswersByWhatHappened covers every answer that
// confirms no task. Each says what happened in its own status and words, and
// each run that sent vzdump anything leaves exactly one summary row.
func TestRunBackupJobWithNoTaskAnswersByWhatHappened(t *testing.T) {
	const twoOnline = `[{"node":"pve-01","status":"online"},{"node":"pve-02","status":"online"}]`
	for _, tc := range []struct {
		name                    string
		job, nodes              string
		replies                 map[string]string
		answers                 map[string]func(http.ResponseWriter)
		stopListeningAfterNodes bool
		wantStatus              int
		wantPosted              []string
		// wantBody is the full body of a 200.
		wantBody string
		// wantInMessage and wantNotInMessage are fragments of the error
		// envelope's message.
		wantInMessage, wantNotInMessage []string
		// wantSummary is the summary row's details, or "" for no row at all.
		wantSummary string
	}{
		{
			// Every node answered that it holds none of the job's guests: a
			// 200, recorded, because Proxmox was asked.
			name:       "nothing to back up anywhere",
			job:        `{"id":"` + backupRunJobID + `","pool":"pool01"}`,
			nodes:      twoOnline,
			replies:    map[string]string{"pve-01": `"OK"`, "pve-02": `"OK"`},
			wantStatus: fiber.StatusOK,
			wantPosted: []string{"pve-01", "pve-02"},
			wantBody: `{"tasks":[],"skipped":["pve-01","pve-02"],"errors":[],"unconfirmed":[],` +
				`"stops_running_backups":false}`,
			wantSummary: `{"job_id":"` + backupRunJobID + `","tasks":[],"skipped":["pve-01","pve-02"],` +
				`"errors":[],"unconfirmed":[],"stops_running_backups":false}`,
		},
		{
			// The same, with stop set: vzdump stopped any backup running on
			// both nodes before it answered "OK", so the answer and the row
			// must not read as "nothing happened".
			name:       "nothing to back up, with stop set",
			job:        `{"id":"` + backupRunJobID + `","pool":"pool01","stop":1}`,
			nodes:      twoOnline,
			replies:    map[string]string{"pve-01": `"OK"`, "pve-02": `"OK"`},
			wantStatus: fiber.StatusOK,
			wantPosted: []string{"pve-01", "pve-02"},
			wantBody: `{"tasks":[],"skipped":["pve-01","pve-02"],"errors":[],"unconfirmed":[],` +
				`"stops_running_backups":true}`,
			wantSummary: `{"job_id":"` + backupRunJobID + `","tasks":[],"skipped":["pve-01","pve-02"],` +
				`"errors":[],"unconfirmed":[],"stops_running_backups":true}`,
		},
		{
			// Certain failures only: "started no backup" is true, and says so.
			name:       "a node refused and one is offline",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:      `[{"node":"pve-01","status":"online"},{"node":"pve-02","status":"online"},{"node":"pve-03","status":"offline"}]`,
			replies:    map[string]string{"pve-01": `"OK"`},
			answers:    map[string]func(http.ResponseWriter){"pve-02": backupRunStatus(http.StatusBadRequest, backupRunRefusal)},
			wantStatus: fiber.StatusBadGateway,
			wantPosted: []string{"pve-01", "pve-02"},
			wantInMessage: []string{
				"Backup job " + backupRunJobID + " started no backup.",
				"Could not start on pve-02: storage: fixture: no such\tstorage\n; " +
					"pve-03: node is not online (Proxmox reports it as offline)",
			},
			wantNotInMessage: []string{"check the task list", "stop set"},
			wantSummary: `{"job_id":"` + backupRunJobID + `","tasks":[],"skipped":["pve-01"],"errors":[` +
				`{"node":"pve-02","message":"storage: fixture: no such storage"},` +
				`{"node":"pve-03","message":"node is not online (Proxmox reports it as offline)"}],` +
				`"unconfirmed":[],"stops_running_backups":false}`,
		},
		{
			// With stop set, the node that answered "OK" had any running backup
			// stopped, and the message says so.
			name:       "a node refused, with stop set",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101","stop":1}`,
			nodes:      twoOnline,
			replies:    map[string]string{"pve-01": `"OK"`},
			answers:    map[string]func(http.ResponseWriter){"pve-02": backupRunStatus(http.StatusBadRequest, backupRunRefusal)},
			wantStatus: fiber.StatusBadGateway,
			wantPosted: []string{"pve-01", "pve-02"},
			wantInMessage: []string{
				"started no backup.",
				"The job has stop set, so vzdump stopped any backup already running on pve-01",
				// pve-02 refused, and a refusal can come after the stop.
				"With stop set, vzdump may also have stopped a backup already running on a node it " +
					"refused afterwards.",
			},
			wantSummary: `{"job_id":"` + backupRunJobID + `","tasks":[],"skipped":["pve-01"],"errors":[` +
				`{"node":"pve-02","message":"storage: fixture: no such storage"}],` +
				`"unconfirmed":[],"stops_running_backups":true}`,
		},
		{
			// The request reached pve-02 and the answer was lost: a backup may
			// be running there, so the message must not say none started — and
			// the transport error's address must not reach anyone.
			name:       "a node did not answer",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:      twoOnline,
			replies:    map[string]string{"pve-01": `"OK"`},
			answers:    map[string]func(http.ResponseWriter){"pve-02": backupRunDropped},
			wantStatus: fiber.StatusBadGateway,
			wantPosted: []string{"pve-01", "pve-02"},
			wantInMessage: []string{
				"did not confirm that any backup started — check the task list before running it again.",
				"Unconfirmed on pve-02: Failed to connect to Proxmox",
			},
			wantNotInMessage: []string{"started no backup", "Could not start"},
			wantSummary: `{"job_id":"` + backupRunJobID + `","tasks":[],"skipped":["pve-01"],"errors":[],` +
				`"unconfirmed":[{"node":"pve-02","message":"Failed to connect to Proxmox"}],` +
				`"stops_running_backups":false}`,
		},
		{
			// The headers arrived and the body did not: a backup may well be
			// running, and the transport's error names both ends of the
			// socket, so the node gets a fixed sentence instead.
			name:       "an answer cut off mid-body",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:      `[{"node":"pve-01","status":"online"}]`,
			answers:    map[string]func(http.ResponseWriter){"pve-01": backupRunCutOff},
			wantStatus: fiber.StatusBadGateway,
			wantPosted: []string{"pve-01"},
			wantInMessage: []string{
				"did not confirm that any backup started — check the task list before running it again.",
				"Unconfirmed on pve-01: the answer from Proxmox could not be read.",
			},
			wantSummary: `{"job_id":"` + backupRunJobID + `","tasks":[],"skipped":[],"errors":[],` +
				`"unconfirmed":[{"node":"pve-01","message":"the answer from Proxmox could not be read"}],` +
				`"stops_running_backups":false}`,
		},
		{
			// The POST could not even connect: nothing left Nexara, so nothing
			// started for certain, there is no task list to check, and there is
			// no action against Proxmox to record.
			name:                    "a node that could not be connected to",
			job:                     `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:                   `[{"node":"pve-01","status":"online"}]`,
			replies:                 map[string]string{"pve-01": `"` + backupRunUPID01 + `"`},
			stopListeningAfterNodes: true,
			wantStatus:              fiber.StatusBadGateway,
			wantPosted:              []string{},
			wantInMessage: []string{
				"Backup job " + backupRunJobID + " started no backup. Could not start on pve-01: Failed to connect to Proxmox",
			},
			wantNotInMessage: []string{"check the task list"},
		},
		{
			// pveproxy's own failure statuses carry their reason in the status
			// line alone, so the message says what is known rather than
			// nothing.
			name:       "a proxy status with no body",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:      `[{"node":"pve-01","status":"online"}]`,
			answers:    map[string]func(http.ResponseWriter){"pve-01": backupRunStatus(596, "")},
			wantStatus: fiber.StatusBadGateway,
			wantPosted: []string{"pve-01"},
			wantInMessage: []string{
				"Unconfirmed on pve-01: Proxmox answered with status 596 and no message",
			},
			wantSummary: `{"job_id":"` + backupRunJobID + `","tasks":[],"skipped":[],"errors":[],` +
				`"unconfirmed":[{"node":"pve-01","message":"Proxmox answered with status 596 and no message"}],` +
				`"stops_running_backups":false}`,
		},
		{
			// The offline node's name was never checked — nothing was sent to
			// it — so the summary row, which every Viewer reads, cleans it.
			name:       "an offline node whose name carries a control character",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:      `[{"node":"pve-0\u001b1","status":"offline"},{"node":"pve-01","status":"online"}]`,
			replies:    map[string]string{"pve-01": `"OK"`},
			wantStatus: fiber.StatusBadGateway,
			wantPosted: []string{"pve-01"},
			wantSummary: `{"job_id":"` + backupRunJobID + `","tasks":[],"skipped":["pve-01"],"errors":[` +
				`{"node":"pve-0 1","message":"node is not online (Proxmox reports it as offline)"}],` +
				`"unconfirmed":[],"stops_running_backups":false}`,
		},
		// One case per error kind backupJobRunFailureMessage lets through to
		// mapProxmoxError, each reaching the answer and the summary row as
		// that renders it, not as the fixed sentence for an unknown kind. A
		// 400 (the refusal fixture), a failed connection and a 596 are pinned
		// above.
		{
			// The likeliest real failure of a Run now: PVE's storage
			// permission check. checkStatus keeps a 403's body as its text.
			name:       "a node Proxmox refused with a 403",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:      twoOnline,
			replies:    map[string]string{"pve-01": `"OK"`},
			answers:    map[string]func(http.ResponseWriter){"pve-02": backupRunStatus(http.StatusForbidden, backupRunForbidden)},
			wantStatus: fiber.StatusBadGateway,
			wantPosted: []string{"pve-01", "pve-02"},
			wantInMessage: []string{
				"started no backup. Could not start on pve-02: " + backupRunForbiddenMessage,
			},
			wantSummary: backupRunOneFailure("pve-02", backupRunForbiddenMessage, true),
		},
		{
			name:          "a node that answered 404",
			job:           `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:         twoOnline,
			replies:       map[string]string{"pve-01": `"OK"`},
			answers:       map[string]func(http.ResponseWriter){"pve-02": backupRunStatus(http.StatusNotFound, `{"data":null}`)},
			wantStatus:    fiber.StatusBadGateway,
			wantPosted:    []string{"pve-01", "pve-02"},
			wantInMessage: []string{"started no backup. Could not start on pve-02: Resource not found on Proxmox."},
			wantSummary:   backupRunOneFailure("pve-02", "Resource not found on Proxmox", true),
		},
		{
			// Neither a task nor "OK": an invalid response, and unconfirmed.
			name:       "a node that answered neither a task nor OK",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:      twoOnline,
			replies:    map[string]string{"pve-01": `"OK"`, "pve-02": `"done"`},
			wantStatus: fiber.StatusBadGateway,
			wantPosted: []string{"pve-01", "pve-02"},
			wantInMessage: []string{
				"did not confirm that any backup started",
				"Unconfirmed on pve-02: " + backupRunDoneMessage,
			},
			wantSummary: backupRunOneFailure("pve-02", backupRunDoneMessage, false),
		},
		{
			// A name the client would not put in a path: nothing was sent to
			// it, so "the answer could not be read" would be false as well as
			// unhelpful. pve-01 was asked, so the run has a row.
			name:       "a node whose name cannot be sent",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:      `[{"node":"pve-01","status":"online"},{"node":"a/b","status":"online"}]`,
			replies:    map[string]string{"pve-01": `"OK"`, "a/b": `"` + backupRunUPID04 + `"`},
			wantStatus: fiber.StatusBadGateway,
			wantPosted: []string{"pve-01"},
			wantInMessage: []string{
				"started no backup. Could not start on a/b: " + backupRunUnsendableMessage,
			},
			wantSummary: backupRunOneFailure("a/b", backupRunUnsendableMessage, true),
		},
		{
			// Every node offline: nothing was sent, so there is nothing to
			// record — but it is still not a success.
			name:          "every node offline",
			job:           `{"id":"` + backupRunJobID + `","vmid":"101"}`,
			nodes:         `[{"node":"pve-01","status":"offline"}]`,
			wantStatus:    fiber.StatusBadGateway,
			wantPosted:    []string{},
			wantInMessage: []string{"started no backup. Could not start on pve-01: node is not online"},
		},
		{
			// Pinned to a node that is down: refused before anything is sent,
			// as the Proxmox GUI refuses it, and nothing is recorded.
			name:       "the job's node is offline",
			job:        `{"id":"` + backupRunJobID + `","vmid":"101","node":"pve-03"}`,
			nodes:      `[{"node":"pve-01","status":"online"},{"node":"pve-03","status":"offline"}]`,
			replies:    map[string]string{"pve-01": `"` + backupRunUPID01 + `"`, "pve-03": `"` + backupRunUPID04 + `"`},
			wantStatus: fiber.StatusConflict,
			wantPosted: []string{},
			wantInMessage: []string{
				"runs only on node pve-03, which is not online (Proxmox reports it as offline)",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pve := &backupRunPVE{job: tc.job, nodes: tc.nodes, replies: tc.replies, answers: tc.answers,
				stopListeningAfterNodes: tc.stopListeningAfterNodes}
			app, dbtx := newBackupRunApp(t, pve)
			status, body := runBackupJob(t, app)
			if status != tc.wantStatus {
				t.Fatalf("status = %d (%s), want %d", status, body, tc.wantStatus)
			}
			if got := pve.postedTo(); !slices.Equal(got, tc.wantPosted) {
				t.Errorf("vzdump was posted to %v, want %v", got, tc.wantPosted)
			}
			if tc.wantBody != "" {
				if string(body) != tc.wantBody {
					t.Errorf("answer = %s\nwant %s", body, tc.wantBody)
				}
			} else {
				var env ErrorResponse
				if err := json.Unmarshal(body, &env); err != nil {
					t.Fatalf("the answer is not an error envelope (%s): %v", body, err)
				}
				for _, frag := range tc.wantInMessage {
					if !strings.Contains(env.Message, frag) {
						t.Errorf("message %q does not say %q", env.Message, frag)
					}
				}
				for _, frag := range tc.wantNotInMessage {
					if strings.Contains(env.Message, frag) {
						t.Errorf("message %q says %q", env.Message, frag)
					}
				}
			}
			tasks, taskAudits, summaries := dbtx.rows(t)
			if len(tasks) != 0 || len(taskAudits) != 0 {
				t.Errorf("a run that confirmed no task tracked %+v, %+v", tasks, taskAudits)
			}
			if tc.wantSummary == "" {
				if len(summaries) != 0 {
					t.Errorf("a run that sent nothing wrote summary rows %+v", summaries)
				}
				return
			}
			checkBackupRunSummary(t, summaries, tc.wantSummary)
			if strings.Contains(string(body), pve.host) || strings.Contains(summaries[0].raw, pve.host) {
				t.Errorf("the stand-in's address %s leaked into the answer or the summary row", pve.host)
			}
		})
	}
}
