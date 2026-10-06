package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Shared scaffolding of the access, node-options and node-config route tests: the
// real declarations and handlers mounted over a stand-in Proxmox and a stand-in
// database, so a value can be followed from the request to the form Proxmox
// receives and the audit row every Viewer can read.

// The route stub (~550 declarations mounted on a Fiber app) takes 40-100 ms to build
// under -race, and a run that built one per use paid for 1,673 of them (65 s). Tests
// that read the registry, the route table or one declaration take this one; its
// declarations are copied out (Parameters cloned) and registered in registries of
// their own. A test that changes the Server or its registry, or sends requests
// through its app, builds its own with newRouteStubServer. Route limiters are shared
// with the declarations and live as long as the test binary, so a test that sends
// through one clears it (mappingProbe) or builds its own stub when the limiter is
// what it spends.
var (
	routeStubOnce      sync.Once
	routeStub          *Server
	routeStubEndpoints map[string]Endpoint
)

func sharedRouteStub(t *testing.T) *Server {
	t.Helper()
	routeStubOnce.Do(func() {
		s := newRouteStubServer(t)
		eps := map[string]Endpoint{}
		for _, e := range s.registry.Endpoints() {
			eps[e.Method+" "+e.Path] = e
		}
		routeStub, routeStubEndpoints = s, eps
	})
	if routeStub == nil {
		t.Fatal("the shared route stub failed to build in an earlier test")
	}
	return routeStub
}

// sharedEndpoints is the stub's declarations keyed "METHOD path".
func sharedEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	sharedRouteStub(t)
	return routeStubEndpoints
}

// declaredEndpoint is the production declaration for one route, and fails the test
// when the registry does not have it.
func declaredEndpoint(t *testing.T, method, path string) Endpoint {
	t.Helper()
	e, ok := sharedEndpoints(t)[method+" "+path]
	if !ok {
		t.Fatalf("%s %s is not declared in the registry", method, path)
	}
	e.Parameters = maps.Clone(e.Parameters)
	return e
}

// declaredEndpointsWhere is the declarations keep accepts, keyed "METHOD path".
func declaredEndpointsWhere(t *testing.T, keep func(Endpoint) bool) map[string]Endpoint {
	t.Helper()
	out := map[string]Endpoint{}
	for key, e := range sharedEndpoints(t) {
		if keep(e) {
			e.Parameters = maps.Clone(e.Parameters)
			out[key] = e
		}
	}
	return out
}

// underPath keeps the endpoints whose path starts with any of prefixes.
func underPath(prefixes ...string) func(Endpoint) bool {
	return func(e Endpoint) bool {
		return slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(e.Path, p) })
	}
}

// keyedIn keeps the endpoints whose "METHOD path" is a key of table.
func keyedIn[V any](table map[string]V) func(Endpoint) bool {
	return func(e Endpoint) bool {
		_, ok := table[e.Method+" "+e.Path]
		return ok
	}
}

// realRoute is a declared route and the real handler to mount in its place.
type realRoute struct {
	method, path string
	handler      Handler
}

// mountReal mounts the declarations of routes, each with its real handler, behind auth.
func mountReal(t *testing.T, auth fiber.Handler, routes ...realRoute) *fiber.App {
	t.Helper()
	es := make([]Endpoint, len(routes))
	for i, r := range routes {
		es[i] = declaredEndpoint(t, r.method, r.path)
		es[i].Handler = r.handler
	}
	return newRegistryApp(t, auth, es...)
}

// mountWith mounts e alone behind auth, with lookup answering node membership
// (nil: no lookup), after the app-level middleware mw.
func mountWith(auth fiber.Handler, lookup handlers.NodeLookup, e Endpoint, mw ...fiber.Handler) *fiber.App {
	reg := NewRegistry()
	reg.Register(e)
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	for _, m := range mw {
		app.Use(m)
	}
	mountRegistry(app, reg, auth, lookup)
	return app
}

// sweepHTTP is the HTTP request for a synthesized route request, signed in as the stub session.
func sweepHTTP(method string, req sweepRequest) *http.Request {
	var body io.Reader
	if len(req.body) > 0 {
		body = bytes.NewReader(req.body)
	}
	r := httptest.NewRequest(method, req.target, body)
	if len(req.body) > 0 {
		r.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}
	r.Header.Set("X-Test-User", "yes")
	return r
}

// mountedChains is the handler chain Fiber mounted for every route of s, by name,
// keyed "METHOD path": the only thing a guard can inspect of the permission and
// gate links, which is why they are attached per route and never through a Group.
func mountedChains(s *Server) map[string][]string {
	chains := map[string][]string{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		names := make([]string, 0, len(r.Handlers))
		for _, h := range r.Handlers {
			names = append(names, handlerName(h))
		}
		chains[r.Method+" "+normalizeRoutePath(r.Path)] = names
	}
	return chains
}

// grantsOf is a caller who may do exactly what its "action:resource" names say.
func grantsOf(perms ...string) map[string]bool {
	out := make(map[string]bool, len(perms))
	for _, p := range perms {
		out[p] = true
	}
	return out
}

// sendFull runs one request and returns what a caller receives. Its timeout is
// longer than app.Test's second: some requests carry half a megabyte, and the race
// detector on a busy machine must not turn that into a timeout that says nothing
// about the handler.
func sendFull(t *testing.T, app *fiber.App, req *http.Request) (int, http.Header, []byte) {
	t.Helper()
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 15 * time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header, body
}

func sendBody(t *testing.T, app *fiber.App, req *http.Request) (int, []byte) {
	t.Helper()
	status, _, body := sendFull(t, app, req)
	return status, body
}

// messageOf is the message of a response body's error envelope.
func messageOf(body []byte) string {
	var env ErrorResponse
	_ = json.Unmarshal(body, &env)
	return env.Message
}

// clipForFailure shortens a failure message so a refusal that echoes a 64 KiB value
// does not bury the line that says what went wrong.
func clipForFailure(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// accessUpdateEncKey is the AES-256 key the stand-in cluster row's token secret is
// encrypted with, and accessUpdateSelf the account Nexara authenticates as there.
const (
	accessUpdateEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	accessUpdateSelf   = "nexara@pve"
)

var errAccessUpdateUnexpected = errors.New("unexpected statement")

// accessUpdatePVERequest is one request the stand-in Proxmox received.
type accessUpdatePVERequest struct {
	method string
	path   string
	form   url.Values
}

// accessUpdatePVE stands in for the cluster's Proxmox API: it records every
// request and answers each with an empty success envelope, unless reply gave the
// path a body or refuse a status too.
type accessUpdatePVE struct {
	mu       sync.Mutex
	seen     []accessUpdatePVERequest
	replies  map[string]string
	statuses map[string]int
}

// reply makes the stand-in answer requests for path (as the server sees it
// decoded, /api2/json/access/users) with body in place of the empty envelope.
func (p *accessUpdatePVE) reply(path, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.replies == nil {
		p.replies = map[string]string{}
	}
	p.replies[path] = body
}

// refuse is reply with an HTTP status: a Proxmox that will not do what it was asked.
func (p *accessUpdatePVE) refuse(path string, status int, body string) {
	p.reply(path, body)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.statuses == nil {
		p.statuses = map[string]int{}
	}
	p.statuses[path] = status
}

func (p *accessUpdatePVE) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("stand-in Proxmox: ParseForm: %v", err)
		}
		p.mu.Lock()
		p.seen = append(p.seen, accessUpdatePVERequest{method: r.Method, path: r.URL.Path, form: r.PostForm})
		body, ok := p.replies[r.URL.Path]
		status := p.statuses[r.URL.Path]
		p.mu.Unlock()
		if !ok {
			body = `{"data":null}`
		}
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (p *accessUpdatePVE) requests() []accessUpdatePVERequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]accessUpdatePVERequest(nil), p.seen...)
}

// lines are the requests the stand-in received, as "METHOD path".
func (p *accessUpdatePVE) lines() []string {
	reqs := p.requests()
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.method + " " + r.path
	}
	return out
}

// accessUpdateDB stands in for the database behind the handlers: the one cluster
// row (read by GetCluster) and the audit log. Any other statement fails, so a
// handler that starts reading something else fails here instead of being handed a
// row that happens to satisfy it. The self-credential guard's read of the cluster
// row and the one that builds the Proxmox client are told apart by their order
// (failClusterRead, clusterReadCount): the guard's is the first when it runs.
type accessUpdateDB struct {
	mu      sync.Mutex
	cluster db.Cluster
	audits  [][]any
	// clusterReads counts the GetCluster reads made, and clusterFailures maps the
	// number of a read, the first being 1, to the error it fails with.
	clusterReads    int
	clusterFailures map[int]error
}

func (d *accessUpdateDB) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "-- name: GetCluster :one") && len(args) == 1 && args[0] == d.cluster.ID {
		d.mu.Lock()
		d.clusterReads++
		err := d.clusterFailures[d.clusterReads]
		d.mu.Unlock()
		return accessUpdateRow{cluster: d.cluster, err: err}
	}
	return accessUpdateRow{err: fmt.Errorf("%w: %.60s", errAccessUpdateUnexpected, sql)}
}

// failClusterRead makes the nth GetCluster read fail with err, as the database
// does for a cluster that is not there (pgx.ErrNoRows) or for a connection that has gone.
func (d *accessUpdateDB) failClusterRead(n int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.clusterFailures == nil {
		d.clusterFailures = map[int]error{}
	}
	d.clusterFailures[n] = err
}

func (d *accessUpdateDB) clusterReadCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.clusterReads
}

func (*accessUpdateDB) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("%w: %.60s", errAccessUpdateUnexpected, sql)
}

func (d *accessUpdateDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if !strings.Contains(sql, "INSERT INTO audit_log") {
		return pgconn.CommandTag{}, fmt.Errorf("%w: %.60s", errAccessUpdateUnexpected, sql)
	}
	d.mu.Lock()
	d.audits = append(d.audits, args)
	d.mu.Unlock()
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (d *accessUpdateDB) auditRows() [][]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([][]any(nil), d.audits...)
}

// oneAccessAuditRow returns the audit row a request wrote, and fails unless it wrote
// exactly one. The argument positions are InsertAuditLog's: cluster, user, resource
// type, resource id, action, details.
func oneAccessAuditRow(t *testing.T, rows [][]any) auditRowWant {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("the request wrote %d audit rows, want exactly 1", len(rows))
	}
	args := rows[0]
	if len(args) != 6 {
		t.Fatalf("the audit insert took %d arguments, want InsertAuditLog's 6", len(args))
	}
	details, ok := args[5].(json.RawMessage)
	if !ok {
		t.Fatalf("the audit details argument is %T, want json.RawMessage", args[5])
	}
	resourceType, _ := args[2].(string)
	resourceID, _ := args[3].(string)
	action, _ := args[4].(string)
	return auditRowWant{resourceType, resourceID, action, string(details)}
}

// accessUpdateRow replays the cluster row through pgx.Row positionally, in the
// struct's field order, which is the order GetCluster scans in.
type accessUpdateRow struct {
	cluster db.Cluster
	err     error
}

func (r accessUpdateRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	row := reflect.ValueOf(r.cluster)
	if len(dest) != row.NumField() {
		return fmt.Errorf("scan got %d destinations, want %d", len(dest), row.NumField())
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		if ptr.Kind() != reflect.Pointer || ptr.Elem().Type() != row.Field(i).Type() {
			return fmt.Errorf("scan destination %d is %T, want *%s", i, d, row.Field(i).Type())
		}
		ptr.Elem().Set(row.Field(i))
	}
	return nil
}

// newClusterStore is the stand-in database holding one cluster row: the API at
// apiURL, authenticated as accessUpdateSelf!api with a secret encrypted under key
// (accessUpdateEncKey when empty), identified as clusterID (testClusterID when empty).
func newClusterStore(t *testing.T, apiURL, key, clusterID string) *accessUpdateDB {
	t.Helper()
	secret, err := crypto.Encrypt("token-secret-value", cmp.Or(key, accessUpdateEncKey))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return &accessUpdateDB{cluster: db.Cluster{
		ID:                   uuid.MustParse(cmp.Or(clusterID, testClusterID)),
		Name:                 "cluster01",
		ApiUrl:               apiURL,
		TokenID:              accessUpdateSelf + "!api",
		TokenSecretEncrypted: secret,
		IsActive:             true,
	}}
}

// newAccessStandIns is what a route test mounts the real AccessHandler on: the
// stand-in Proxmox and the stand-in database holding the cluster that points at it.
func newAccessStandIns(t *testing.T) (*accessUpdatePVE, *accessUpdateDB, *handlers.AccessHandler) {
	t.Helper()
	pve := &accessUpdatePVE{}
	store := newClusterStore(t, pve.serve(t), "", "")
	return pve, store, handlers.NewAccessHandler(db.New(store), accessUpdateEncKey, nil)
}

// newAccessApp mounts the real AccessHandler routes that wire picks over the
// access stand-ins, behind a caller holding grants.
func newAccessApp(t *testing.T, grants map[string]bool, wire func(h *handlers.AccessHandler) []realRoute) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	pve, store, h := newAccessStandIns(t)
	return mountReal(t, stubAuth(grants), wire(h)...), pve, store
}

// sameForm compares two forms with a missing map and an empty one equal.
func sameForm(got, want url.Values) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

// auditWant is what a request to a route that edits Proxmox and audits it must do.
type auditWant struct {
	// status is the answer's status; 0 is "any error", for a Proxmox that refused
	// (which error the house mappers pick is theirs to decide).
	status int
	// message and body are fragments the error message and the raw body must carry.
	message, body []string
	// method and path are the one request that must reach Proxmox, with form; an
	// empty path is a refusal made before anything is sent, which writes no audit row.
	method, path string
	form         url.Values
	// audit is the one row that must be written; empty, none (Proxmox refused).
	audit auditRowWant
	// never are values the row's details must not carry, whatever key they sit under.
	never []string
}

// auditRowWant is an audit row as stored: its resource type, id, action and exact details.
type auditRowWant struct{ resourceType, resourceID, action, details string }

func auditRow(resourceType, resourceID, action, details string) auditRowWant {
	return auditRowWant{resourceType, resourceID, action, details}
}

// requireAudited sends req and holds the answer, what reached Proxmox and the audit
// row to want. What Proxmox received is established first: it is what makes the
// row's silence about a field mean something.
func requireAudited(t *testing.T, app *fiber.App, pve *accessUpdatePVE, store *accessUpdateDB, req *http.Request, want auditWant) {
	t.Helper()
	status, raw := sendBody(t, app, req)
	msg := messageOf(raw)
	switch {
	case want.status == 0 && status < fiber.StatusBadRequest:
		t.Fatalf("Proxmox refused and the caller got %d (%q), want an error", status, msg)
	case want.status != 0 && status != want.status:
		t.Fatalf("status = %d (%s), want %d", status, clipForFailure(string(raw), 300), want.status)
	}
	for _, f := range want.message {
		if !strings.Contains(msg, f) {
			t.Errorf("the message %q does not carry %q", msg, f)
		}
	}
	for _, f := range want.body {
		if !strings.Contains(string(raw), f) {
			t.Errorf("the response %s does not carry %q", raw, f)
		}
	}

	sent, rows := pve.requests(), store.auditRows()
	if want.path == "" {
		if len(sent) != 0 || len(rows) != 0 {
			t.Errorf("a refusal made before Proxmox: %d request(s) %+v and %d audit row(s) followed", len(sent), sent, len(rows))
		}
		return
	}
	if len(sent) != 1 {
		t.Fatalf("the handler made %d Proxmox calls, want exactly 1: %+v", len(sent), sent)
	}
	if sent[0].method != want.method || sent[0].path != want.path {
		t.Fatalf("the handler sent %s %s, want %s %s", sent[0].method, sent[0].path, want.method, want.path)
	}
	if !sameForm(sent[0].form, want.form) {
		t.Errorf("Proxmox received the form %v, want %v", sent[0].form, want.form)
	}
	if want.audit == (auditRowWant{}) {
		if len(rows) != 0 {
			t.Errorf("no audit row may be written for a save that did not happen, got %d", len(rows))
		}
		return
	}
	row := oneAccessAuditRow(t, rows)
	if row != want.audit {
		t.Errorf("the audit row is %+v, want %+v", row, want.audit)
	}
	if got := rows[0][0]; got != handlers.ClusterUUID(uuid.MustParse(testClusterID)) {
		t.Errorf("the audit row names the cluster %v, want the one the route belongs to", got)
	}
	for _, v := range want.never {
		if strings.Contains(row.details, v) {
			t.Errorf("the audit details %s carry %q, which view:audit would show every Viewer", row.details, v)
		}
	}
}
