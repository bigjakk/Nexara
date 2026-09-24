package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/valyala/fasthttp"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/config"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// uploadGrants is a permission engine that grants exactly the action:resource
// pairs it holds, at every scope. UploadFile reads it from c.Locals as
// authRequired would have left it.
type uploadGrants map[string]bool

func (g uploadGrants) HasPermission(_ context.Context, _ uuid.UUID, action, resource, _ string, _ uuid.UUID) (bool, error) {
	return g[action+":"+resource], nil
}

func (g uploadGrants) HasGlobalPermission(_ context.Context, _ uuid.UUID, action, resource string) (bool, error) {
	return g[action+":"+resource], nil
}

func (g uploadGrants) LoadUserPermissions(context.Context, uuid.UUID) (*auth.UserPermissions, error) {
	return &auth.UserPermissions{}, nil
}

// errUnexpectedStatement is what uploadRows answers any statement it does not
// expect with.
var errUnexpectedStatement = errors.New("a statement the upload test does not expect")

// uploadRows answers the three reads UploadFile makes before it reads the
// stream — its storage pool, the pool's cluster (for the Proxmox client) and
// its node — by the name sqlc gives each query, and nothing else.
type uploadRows map[string]any

func (r uploadRows) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errUnexpectedStatement
}

func (r uploadRows) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errUnexpectedStatement
}

func (r uploadRows) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	for name, row := range r {
		if strings.Contains(sql, "-- name: "+name+" :one") {
			return structRow{row}
		}
	}
	return failedRow{fmt.Errorf("%w: %.60q", errUnexpectedStatement, sql)}
}

// structRow replays a generated row struct through pgx.Row, assigning in the
// struct's field order — the order the generated query scans in. A
// destination of the wrong type fails the scan instead of shifting a value
// into the wrong field.
type structRow struct{ v any }

func (r structRow) Scan(dest ...any) error {
	row := reflect.ValueOf(r.v)
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

type failedRow struct{ err error }

func (r failedRow) Scan(...any) error { return r.err }

// TestRefusedUploadClosesItsConnectionMidStream drives the real UploadFile —
// the one handler that reads the body's stream itself — through the real
// upload declaration, as a caller holding manage:vm_import and not
// manage:storage, uploading an ISO. The handler reads the multipart stream
// part by part and refuses the file part with 403 when it reaches it, with
// the rest of the body unread: the refusal has to close the connection, and
// the request hidden past the part fasthttp read ahead must not be served.
//
// It is what a later change to UploadFile — releasing the stream on the way
// out, say — would break without failing anything else: a released stream
// reads as a body read to its end. The precondition is the same app without
// closeConnectionsLeftMidBody, where the hidden request is served.
//
// Authentication is a stand-in that leaves in c.Locals what authRequired
// would — a user, and a permission engine — and the three rows UploadFile
// looks up come from a stand-in database; everything from the declaration's
// extraction to the handler's refusal is the production code. The 403 comes
// before any call to Proxmox.
func TestRefusedUploadClosesItsConnectionMidStream(t *testing.T) {
	clusterID, nodeID, poolID := uuid.New(), uuid.New(), uuid.New()
	secret, err := crypto.Encrypt("token-secret-value", sweepEncryptionKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	rows := uploadRows{
		"GetStoragePool": db.StoragePool{ID: poolID, ClusterID: clusterID, NodeID: nodeID, Storage: "store01",
			Type: "dir", Content: "iso,vztmpl", Active: true, Enabled: true},
		"GetCluster": db.Cluster{ID: clusterID, Name: "cluster01", ApiUrl: "https://192.0.2.10:8006",
			TokenID: "user@pam!test", TokenSecretEncrypted: secret, IsActive: true},
		"GetNode": db.Node{ID: nodeID, ClusterID: clusterID, Name: "pve-01"},
	}

	serve := func(t *testing.T, guarded bool) (string, *atomic.Int32) {
		t.Helper()
		var wrap func(*fiber.App)
		if guarded {
			wrap = withBodyTimeout(bodyReadTimeout)
		}
		return serveUploadDeclaration(t, rows, uploadGrants{"manage:vm_import": true}, wrap)
	}

	// The multipart body: the content and filesize fields, then the file part's
	// head, all well inside what the multipart reader takes in its first read
	// of the stream; then padding to exactly the read-ahead, so the request
	// hidden after it is the first thing left on the connection; then the rest
	// of the file.
	const boundary = "nexara-upload-boundary"
	preamble := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"content\"\r\n\r\niso\r\n" +
		"--" + boundary + "\r\nContent-Disposition: form-data; name=\"filesize\"\r\n\r\n65536\r\n" +
		"--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"linux01.iso\"\r\n" +
		"Content-Type: application/octet-stream\r\n\r\n"
	body := preamble + strings.Repeat("x", bodyPrefetch-len(preamble)) + hiddenRequest +
		strings.Repeat("z", 4<<10) + "\r\n--" + boundary + "--\r\n"
	target := "/api/v1/clusters/" + clusterID.String() + "/storage/" + poolID.String() + "/upload"
	raw := requestHead(fiber.MethodPost, target, "Content-Type: multipart/form-data; boundary="+boundary) +
		fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body)) + body

	refused := func(t *testing.T, ans wireAnswer) {
		t.Helper()
		if ans.status != fiber.StatusForbidden || ans.envelope.Error != "forbidden" ||
			ans.envelope.Message != "Insufficient permissions" {
			t.Fatalf("status %d %+v, want the handler's own 403 for an ISO from a caller without manage:storage",
				ans.status, ans.envelope)
		}
	}

	addr, hits := serve(t, false)
	ans := exchange(t, addr, raw)
	refused(t, ans)
	if ans.next == nil || ans.next.StatusCode != fiber.StatusNoContent || hits.Load() != 1 {
		t.Fatalf("precondition: without closeConnectionsLeftMidBody the request hidden in the refused upload "+
			"should have been served (next answer %v, closed %v, probe reached %d time(s))", ans.next, ans.closed,
			hits.Load())
	}

	addr, hits = serve(t, true)
	ans = exchange(t, addr, raw)
	refused(t, ans)
	if !ans.connClose || !ans.closed || ans.next != nil {
		t.Errorf("Connection: close %v, closed %v, next answer %v: want the connection closed after the 403",
			ans.connClose, ans.closed, ans.next)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the request hidden in the refused upload was served %d time(s)", n)
	}
}

// serveUploadDeclaration serves the storage endpoints as registerStorageEndpoints
// declares them — UploadFile among them — on a bare app with the server's
// Fiber config, with wrap, when it is not nil, installed around Fiber's
// handler, and hiddenRequestProbe mounted. Authentication is a stand-in that
// leaves in c.Locals what authRequired would: a user, and grants as the
// permission engine. rows are the database rows the handlers may look up.
func serveUploadDeclaration(t *testing.T, rows uploadRows, grants uploadGrants, wrap func(*fiber.App)) (string, *atomic.Int32) {
	t.Helper()
	app := fiber.New(buildFiberConfig(&config.Config{}))
	if wrap != nil {
		wrap(app)
	}
	reg := NewRegistry()
	registerStorageEndpoints(reg, handlers.NewStorageHandler(db.New(rows), sweepEncryptionKey, nil))
	userID := uuid.New()
	mountRegistry(app, reg, func(c fiber.Ctx) error {
		c.Locals("user_id", userID)
		c.Locals("rbac_engine", grants)
		return c.Next()
	})
	hits := new(atomic.Int32)
	app.Get(hiddenRequestProbe, func(c fiber.Ctx) error {
		hits.Add(1)
		return c.SendStatus(fiber.StatusNoContent)
	})
	return serveAppOnLoopback(t, app), hits
}

// TestSlowJSONBodyToTheUploadRouteIsCutAtTheBodyDeadline holds the upload
// route to the body deadline for every body its declaration reads before
// UploadFile checks a single grant. The route is Deferred, so bodyValues reads
// a body of any type isJSONContentType accepts, up to 64 KiB, first — a +json
// type such as multipart/form-data+json among them, although UploadFile would
// take it for multipart. Here a caller holding no grant declares one, sends
// the first 8 KiB and stops. The read has to be cut at the deadline — not
// before, not long after — and the connection closed.
//
// The precondition twin of each row arms no deadline, as the upload's
// exemption did for these bodies before it was held to what it is for, and
// there the read is still waiting three deadlines on: the request reaches a
// read of its body, not a refusal.
func TestSlowJSONBodyToTheUploadRouteIsCutAtTheBodyDeadline(t *testing.T) {
	const bodyTimeout, slack = 500 * time.Millisecond, 2 * time.Second
	noDeadline := func(app *fiber.App) {
		srv := app.Server()
		next := srv.Handler
		srv.Handler = func(ctx *fasthttp.RequestCtx) {
			_ = ctx.Conn().SetReadDeadline(time.Time{})
			next(ctx)
		}
	}
	for _, contentType := range []string{
		fiber.MIMEApplicationJSON,
		"multipart/form-data+json; boundary=x",
		"Multipart/Related+JSON",
	} {
		t.Run(contentType, func(t *testing.T) {
			target := "/api/v1/clusters/" + uuid.New().String() + "/storage/" + uuid.New().String() + "/upload"
			raw := requestHead(fiber.MethodPost, target, "Content-Type: "+contentType) +
				"Content-Length: 65536\r\n\r\n" + strings.Repeat(" ", bodyPrefetch)

			addr, _ := serveUploadDeclaration(t, uploadRows{}, uploadGrants{}, noDeadline)
			conn, br := dialAndSend(t, addr, raw)
			if err := conn.SetReadDeadline(time.Now().Add(3 * bodyTimeout)); err != nil {
				t.Fatalf("set read deadline: %v", err)
			}
			var netErr net.Error
			if _, err := br.ReadByte(); !errors.As(err, &netErr) || !netErr.Timeout() {
				t.Fatalf("precondition: without a body deadline the connection said something, or ended, within %v "+
					"(%v); the request does not reach a read of its body", 3*bodyTimeout, err)
			}

			addr, _ = serveUploadDeclaration(t, uploadRows{}, uploadGrants{}, withBodyTimeout(bodyTimeout))
			sent := time.Now()
			conn, br = dialAndSend(t, addr, raw)
			ans := readAnswer(t, conn, br, fiber.MethodPost)
			elapsed := time.Since(sent)
			if ans.status != fiber.StatusBadRequest || ans.envelope.Message != "request body is not valid JSON" {
				t.Fatalf("status %d (%s), want bodyValues' 400 for a body the deadline cut", ans.status, ans.body)
			}
			if elapsed < bodyTimeout || elapsed > bodyTimeout+slack {
				t.Errorf("answered %v after the request went out, want the read cut at the %v body deadline", elapsed,
					bodyTimeout)
			}
			if !ans.connClose {
				t.Error("the answer does not carry Connection: close")
			}
			if next, closed := whatFollows(t, conn, br); !closed || next != nil {
				t.Errorf("closed %v, next answer %v: want the connection closed after the answer", closed, next)
			}
		})
	}
}

// TestUploadFileStreamsWhatTheSharedMultipartTestAccepts holds UploadFile to
// handlers.ParseMultipartContentType, the test the upload exemption asks as
// well (isStreamedMultipart). Driven through the real declaration, as a caller
// holding manage:storage, with an empty body — which bodyValues reads nothing
// of, whatever its type — UploadFile has to refuse a Content-Type as not
// multipart exactly when the shared test does. A handler that went back to a
// test of its own, stricter or looser, answers one of these differently.
func TestUploadFileStreamsWhatTheSharedMultipartTestAccepts(t *testing.T) {
	clusterID, nodeID, poolID := uuid.New(), uuid.New(), uuid.New()
	secret, err := crypto.Encrypt("token-secret-value", sweepEncryptionKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	rows := uploadRows{
		"GetStoragePool": db.StoragePool{ID: poolID, ClusterID: clusterID, NodeID: nodeID, Storage: "store01",
			Type: "dir", Content: "iso,vztmpl", Active: true, Enabled: true},
		"GetCluster": db.Cluster{ID: clusterID, Name: "cluster01", ApiUrl: "https://192.0.2.10:8006",
			TokenID: "user@pam!test", TokenSecretEncrypted: secret, IsActive: true},
		"GetNode": db.Node{ID: nodeID, ClusterID: clusterID, Name: "pve-01"},
	}
	addr, _ := serveUploadDeclaration(t, rows, uploadGrants{"manage:storage": true}, withBodyTimeout(bodyReadTimeout))
	target := "/api/v1/clusters/" + clusterID.String() + "/storage/" + poolID.String() + "/upload"

	const notMultipart = "Expected multipart form data"
	streamed, refused := 0, 0
	for _, contentType := range []string{
		"multipart/form-data; boundary=x", "Multipart/Mixed; boundary=x", "MULTIPART/RELATED; boundary=x",
		"multipart/form-data", "multipart/form-data+json; boundary=x", "multipart/", "multipart",
		"multipart/form-data; boundary", "text/plain", "application/octet-stream", fiber.MIMEApplicationJSON, "",
	} {
		var lines []string
		if contentType != "" {
			lines = append(lines, "Content-Type: "+contentType)
		}
		conn, br := dialAndSend(t, addr, requestHead(fiber.MethodPost, target, lines...)+"Content-Length: 0\r\n\r\n")
		ans := readAnswer(t, conn, br, fiber.MethodPost)
		_, multipart := handlers.ParseMultipartContentType(contentType)
		answeredNotMultipart := ans.status == fiber.StatusBadRequest && ans.envelope.Message == notMultipart
		if answeredNotMultipart == multipart {
			t.Errorf("Content-Type %q: UploadFile answered %d %q; the shared test says multipart %v", contentType,
				ans.status, ans.envelope.Message, multipart)
		}
		if multipart {
			streamed++
		} else {
			refused++
		}
	}
	if streamed == 0 || refused == 0 {
		t.Fatalf("precondition: %d Content-Types multipart and %d not; the rows have to reach both", streamed, refused)
	}
}
