package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The two PBS task routes — GET /pbs-servers/:pbs_id/tasks/:upid and its /log
// sibling — carry the same three-encoder problem the Ceph pool and IP set entry
// routes carried, and carry it for a value that can NEVER avoid it.
//
// A UPID is colon-separated by construction, and a colon is not legal unencoded
// in a path segment, so encodeURIComponent always sends "UPID%3A…". Fiber runs
// with UnescapePath at its default of false and hands the handler that text
// unchanged; PBSClient.GetTaskLog then url.PathEscape-s it into the outbound
// path, which escapes the "%" a second time. PBS is asked for a task literally
// named "UPID%3A…", which no PBS has ever recorded — so every task-log fetch and
// every task-status poll missed, for every task, on every server.
//
// The assertion below is on the RAW request target the far side receives
// (r.RequestURI). r.URL.Path is already decoded, where "%253A" and "%3A" both
// read back as ":" and the whole defect is invisible.

// pbsUPIDEncKey is the AES-256 key the fake PBS server row's token is encrypted
// with. Fixed, and a test fixture only.
const pbsUPIDEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// pbsUPIDSample is a PBS UPID in the shape PBS itself emits: the literal
// "UPID", the node, three hex fields, the start time, the worker type, the
// worker id, and the user@realm that started it. Nine colons and an "@", every
// one of which encodeURIComponent percent-encodes.
const pbsUPIDSample = "UPID:pbs-01:0000ABCD:00012345:00000000:66F00000:garbage_collection:datastore01:root@pam:"

// pbsUPIDCapture records the raw request target of every call the handler makes
// to the stand-in PBS server.
type pbsUPIDCapture struct {
	mu      sync.Mutex
	targets []string
}

func (p *pbsUPIDCapture) record(target string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = append(p.targets, target)
}

func (p *pbsUPIDCapture) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = nil
}

// only returns the single target the request produced, failing if the handler
// made no PBS call or more than one.
func (p *pbsUPIDCapture) only(t *testing.T) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.targets) != 1 {
		t.Fatalf("the handler made %d PBS calls, want exactly 1: %q", len(p.targets), p.targets)
	}
	return p.targets[0]
}

// seen returns the targets recorded since the last reset.
func (p *pbsUPIDCapture) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}

// newPBSUPIDCaptureServer stands up a server that answers both task endpoints
// with a PBS-shaped envelope and records what it was asked for.
//
// http.HandlerFunc rather than http.ServeMux, for the reason the sibling suite
// records: ServeMux cleans and redirects paths, so a target carrying an encoded
// slash would come back as a 301 to the cleaned form — normalising away the
// very thing under test.
func newPBSUPIDCaptureServer(t *testing.T) (string, *pbsUPIDCapture) {
	t.Helper()
	capture := &pbsUPIDCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r.RequestURI)
		w.Header().Set("Content-Type", "application/json")
		// The shape has to match what the client decodes into, or a passing
		// wire assertion would be followed by a decode failure and a 500.
		// Split on "?" rather than reading r.URL.Path: the log call carries a
		// query string, and Path is the decoded form this suite never trusts.
		target, _, _ := strings.Cut(r.RequestURI, "?")
		if strings.HasSuffix(target, "/log") {
			_, _ = w.Write([]byte(`{"data":[{"n":1,"t":"starting garbage collection"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"upid":"","status":"stopped","exitstatus":"OK"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, capture
}

// errPBSUPIDUnexpectedQuery is what the stand-in DBTX answers for anything
// other than the PBS server read. Nothing else should run: these two routes are
// reads, so neither audits nor publishes.
var errPBSUPIDUnexpectedQuery = errors.New("unexpected query")

// pbsUPIDDBTX answers GetPBSServer with one standalone server row and fails
// everything else, so a handler that starts reading something else fails loudly
// here rather than being handed a row that happens to satisfy it.
// The field name differs from pbsServerRow's deliberately: with both
// spelled "server" the two structs are convertible, and staticcheck reads
// the literal below as a conversion waiting to happen (S1016).
type pbsUPIDDBTX struct{ row db.PbsServer }

func (pbsUPIDDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errPBSUPIDUnexpectedQuery
}

func (pbsUPIDDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errPBSUPIDUnexpectedQuery
}

func (d pbsUPIDDBTX) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if !strings.Contains(sql, "FROM pbs_servers") {
		return failRow{err: errPBSUPIDUnexpectedQuery}
	}
	return pbsServerRow{server: d.row}
}

// pbsServerRow replays a db.PbsServer through pgx.Row, assigning positionally
// in the generated struct's field order — which is the order the generated
// query selects. A column added in the middle makes this report a type mismatch
// rather than silently shifting a value into the wrong field.
type pbsServerRow struct{ server db.PbsServer }

func (r pbsServerRow) Scan(dest ...any) error {
	row := reflect.ValueOf(r.server)
	if len(dest) != row.NumField() {
		return fmt.Errorf("scan got %d destinations, want %d", len(dest), row.NumField())
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		if ptr.Kind() != reflect.Pointer {
			return fmt.Errorf("scan destination %d is %T, not a pointer", i, d)
		}
		if ptr.Elem().Type() != row.Field(i).Type() {
			return fmt.Errorf("scan destination %d is *%s, want *%s — the select column order changed",
				i, ptr.Elem().Type(), row.Field(i).Type())
		}
		ptr.Elem().Set(row.Field(i))
	}
	return nil
}

// pbsUPIDHarness is one wired-up request pipeline: a real Fiber app carrying the
// real BackupHandler, a real proxmox.PBSClient built the way production builds
// it, and a capture server on the far end.
type pbsUPIDHarness struct {
	app     *fiber.App
	pbsID   uuid.UUID
	capture *pbsUPIDCapture
}

// pbsTaskUPIDMirror mirrors the :pbs_id and :upid parameters the two task
// routes declare in internal/api/registry_backup.go. pbs_id comes from the
// shared standard option rather than being retyped, so what these cases are
// validated against is the route's real vocabulary.
//
// The :upid pattern is pbsTaskUPIDParam's, anchor and all: a leading
// alphanumeric, which a UPID satisfies in both its raw and its percent-encoded
// form. Mirrored rather than imported because package api imports this package,
// not the other way round.
func pbsTaskUPIDMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"pbs_id": apischema.StdOption("pbs-id"),
		"upid": {
			Type:      apischema.String,
			Pattern:   `^[A-Za-z0-9]`,
			MinLength: apischema.Ptr(1),
			MaxLength: apischema.Ptr(512),
		},
	})
}

// newPBSUPIDHarness builds the pipeline and mounts both task routes on it.
func newPBSUPIDHarness(t *testing.T) *pbsUPIDHarness {
	t.Helper()

	baseURL, capture := newPBSUPIDCaptureServer(t)
	pbsID := uuid.New()

	encrypted, err := crypto.Encrypt("token-secret-value", pbsUPIDEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// fiber.New() with no Config is exactly production's UnescapePath: the
	// server never sets it (see buildFiberConfig in internal/api/server.go), so
	// the default of false is what runs here and there alike.
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uuid.New())
		c.Locals("role", "admin")
		return c.Next()
	})
	// requirePBSPerm runs on both routes and needs an engine on the request;
	// the stub grants everything to an admin, which keeps these cases about the
	// encoding rather than about RBAC.
	installStubEngineMiddleware(app)

	// ClusterID left invalid on purpose: a standalone PBS server takes
	// requirePBSPerm's instance-wide branch, so the harness needs no cluster row.
	queries := db.New(pbsUPIDDBTX{row: db.PbsServer{
		ID:                   pbsID,
		Name:                 "pbs01",
		ApiUrl:               baseURL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
	}})
	handler := NewBackupHandler(queries, pbsUPIDEncKey, nil)

	mirror := pbsTaskUPIDMirror(t)
	app.Get("/api/v1/pbs-servers/:pbs_id/tasks/:upid",
		withRequestParams(t, mirror, []string{"pbs_id", "upid"}, handler.GetTaskStatus))
	app.Get("/api/v1/pbs-servers/:pbs_id/tasks/:upid/log",
		withRequestParams(t, mirror, []string{"pbs_id", "upid"}, handler.GetTaskLog))

	return &pbsUPIDHarness{app: app, pbsID: pbsID, capture: capture}
}

// send drives one request and returns the raw target the PBS side saw.
//
// target is checked against the request's own escaped path before it goes
// anywhere: httptest.NewRequest parses the string, net/url rewrites some
// targets on the way in, and only URL.EscapedPath says what will actually be
// written into the request line. A case that silently tests a different target
// than it spells is a false green.
func (h *pbsUPIDHarness) send(t *testing.T, target string) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, nil)
	if got := req.URL.EscapedPath(); got != target {
		t.Fatalf("the request would go out as %q, not the %q this case means to send — "+
			"net/url rewrote the target, so the case is testing something else", got, target)
	}

	h.capture.reset()
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status = %d (%s), want a success — the request never reached PBS",
			target, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return h.capture.only(t)
}

func pbsTaskStatusTarget(pbsID, segment string) string {
	return "/api/v1/pbs-servers/" + pbsID + "/tasks/" + segment
}

func pbsTaskLogTarget(pbsID, segment string) string {
	return "/api/v1/pbs-servers/" + pbsID + "/tasks/" + segment + "/log"
}

// browserEncodeURIComponent reproduces the browser function the SPA calls on a UPID
// before it puts one in a path (frontend/src/features/backup/api/backup-queries.ts).
//
// url.PathEscape is NOT the same function and cannot stand in for it: PathEscape
// leaves ":" and "@" unescaped because they are legal in a path segment, which
// is exactly the two characters a UPID is full of. Using it here would send a
// target with no escapes in it at all and the defect would not reproduce.
func browserEncodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// TestPBSTaskRoutesReachPBSAsTheTaskTheCallerMeant is the end-to-end proof for
// both PBS task routes.
//
// Each case names a UPID, encodes it the way the SPA does, drives the real
// router into the real handler, and asserts on the bytes the PBS side receives.
// The want is url.PathEscape of the UPID because that is what the client emits
// once it holds the real value — colons and "@" left alone, which is precisely
// what PBS's own UI sends.
func TestPBSTaskRoutesReachPBSAsTheTaskTheCallerMeant(t *testing.T) {
	h := newPBSUPIDHarness(t)

	upids := []string{
		pbsUPIDSample,
		// Verify and a prune job on a plain datastore: different worker
		// types, the same nine colons, and — on the second — a different
		// realm, so the "@" is exercised twice over. (A prune job's worker id
		// is the bare store name; a manual prune's is "<store>:<ns>", which
		// escapes.)
		"UPID:pbs-01:00001234:0000ABCD:00000000:66F00001:verify:datastore01:root@pam:",
		"UPID:pbs-01:00009ABC:0000CAFE:00000000:66F00003:prunejob:datastore01:backup@pbs:",
		// The shapes most PBS tasks actually carry. PBS writes the worker id
		// through escape_id (proxmox-schema src/upid.rs), which turns every
		// byte outside [A-Za-z0-9_.], and a leading ".", into "\xNN": a
		// backup's "<store>:<type>/<id>"
		// becomes "datastore01\x3avm-100", a verification job's
		// "<store>:<job id>" escapes its colon and the id's dash, and a GC on
		// a dashed datastore escapes the dash. encodeURIComponent sends each
		// backslash as "%5C", the handler decodes it back, and the client has
		// to pass it — refusing a backslash answered 400 for all three.
		`UPID:pbs-01:0000ABCD:00012345:0000001D:66F00004:backup:datastore01\x3avm-100:root@pam:`,
		`UPID:pbs-01:0000ABCE:00012346:0000001E:66F00005:verificationjob:datastore01\x3av\x2d0001:root@pam:`,
		`UPID:pbs-01:0000ABCF:00012347:0000001F:66F00006:garbage_collection:datastore\x2d01:root@pam:`,
	}

	for _, upid := range upids {
		segment := browserEncodeURIComponent(upid)

		t.Run("status/"+upid, func(t *testing.T) {
			got := h.send(t, pbsTaskStatusTarget(h.pbsID.String(), segment))
			want := "/api2/json/nodes/localhost/tasks/" + url.PathEscape(upid) + "/status"
			if got != want {
				t.Errorf("PBS was asked for %q,\n                 want %q\n"+
					"— the task whose status is reported is not the task named", got, want)
			}
		})

		t.Run("log/"+upid, func(t *testing.T) {
			got := h.send(t, pbsTaskLogTarget(h.pbsID.String(), segment))
			want := "/api2/json/nodes/localhost/tasks/" + url.PathEscape(upid) + "/log?start=0&limit=5000"
			if got != want {
				t.Errorf("PBS was asked for %q,\n                 want %q\n"+
					"— the task whose log is fetched is not the task named", got, want)
			}
		})
	}
}

// TestPBSTaskUPIDDecodeRefusesWhatItNowMakesReachable is the other half of the
// decode.
//
// Before it, "A%2F.." named a task literally called "A%2F.." and no guard
// needed to see a slash; after it the same request really does carry one, and
// the guard in PBSClient is what refuses it. A bare "..", and "%2E%2E", never
// get that far: the route's declared pattern — a leading alphanumeric — turns
// both away first. So every case here starts with a letter and satisfies that
// pattern, which is the point: the declaration matches the value AS IT
// ARRIVES and cannot see through an escape, so for these it is not the anchor
// of record.
//
// Nothing may reach PBS in any of them. That is defence in depth rather than
// the last line: PBS splits the raw path on "/" before it decodes anything, so
// it would not have traversed on these either (validatePBSTaskUPID records the
// upstream source). What the refusal buys is a clear local 400 for a value no
// PBS minted, and a guard that keeps holding behind a proxy that normalises
// paths — including one that reads a backslash as a separator, which is why a
// dot piece between backslashes is refused while a backslash itself, which PBS
// puts in most real UPIDs, is not.
func TestPBSTaskUPIDDecodeRefusesWhatItNowMakesReachable(t *testing.T) {
	h := newPBSUPIDHarness(t)

	tests := []struct {
		name string
		// segment is the last path segment, written exactly as it goes on the
		// wire.
		segment string
	}{
		// "A/../../status" once decoded: a slash no real PBS UPID contains,
		// since escape_id writes every "/" in a worker id as "-".
		{"traversal through an encoded slash", "A%2F..%2F..%2Fstatus"},
		{"a single encoded separator", "A%2Fb"},
		{"a dot piece between encoded backslashes", "A%5C..%5Cstatus"},
		// A control byte no catalogued pattern can see: the rule matches the
		// value as it arrives, where "%0A" is three ordinary characters.
		{"encoded newline", "A%0Ab"},
		{"encoded NUL", "A%00b"},
		// A malformed escape is refused rather than guessed at: the contract is
		// that a path parameter is percent-encoded, so "%zz" is a bad request.
		{"malformed escape", "A%zzb"},
	}

	for _, tt := range tests {
		for _, route := range []struct {
			kind   string
			target func(pbsID, segment string) string
		}{
			{"status", pbsTaskStatusTarget},
			{"log", pbsTaskLogTarget},
		} {
			t.Run(tt.name+"/"+route.kind, func(t *testing.T) {
				h.capture.reset()

				resp, err := h.app.Test(pbsUPIDRawRequest(route.target(h.pbsID.String(), tt.segment)))
				if err != nil {
					t.Fatalf("app.Test: %v", err)
				}
				defer func() { _ = resp.Body.Close() }()
				// 400 specifically, not "any error": a 404 would mean the route
				// stopped matching, which would refuse every case here for a
				// reason that has nothing to do with the guard under test.
				if resp.StatusCode != fiber.StatusBadRequest {
					t.Errorf("status = %d, want 400 — the value must be refused by a guard, not by the router",
						resp.StatusCode)
				}
				if sent := h.capture.seen(); len(sent) != 0 {
					t.Errorf("the request reached PBS as %q; it must be refused before it is sent", sent)
				}
			})
		}
	}

	// The control, without which a mistyped target above would refuse every
	// case for the wrong reason and the whole table would still pass.
	t.Run("control: a real UPID still gets through", func(t *testing.T) {
		segment := browserEncodeURIComponent(pbsUPIDSample)
		if got := h.send(t, pbsTaskStatusTarget(h.pbsID.String(), segment)); got == "" {
			t.Error("the control status request made no PBS call")
		}
		if got := h.send(t, pbsTaskLogTarget(h.pbsID.String(), segment)); got == "" {
			t.Error("the control log request made no PBS call")
		}
	})
}

// pbsUPIDRawRequest builds a request whose target goes out on the wire byte for
// byte.
//
// httptest.NewRequest cannot express the malformed-escape case: it runs
// url.Parse on the target and PANICS on "A%zzb" — "invalid URL escape". Setting
// URL.Opaque is what gets past that, because URL.RequestURI returns Opaque
// verbatim when it is set, and httputil.DumpRequest (which is how fiber's
// App.Test produces the request line) asks for exactly that. A real client can
// send such a target and fasthttp accepts it, so the case has to be reachable
// here too.
func pbsUPIDRawRequest(target string) *http.Request {
	return &http.Request{
		Method:     http.MethodGet,
		URL:        &url.URL{Opaque: target},
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Host:       "nexara.example.com",
	}
}
