package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// A PBS storage's client encryption key reaches Nexara two ways: the operator
// pastes one into params.encryption-key, or asks for "autogen" and Proxmox
// answers with the key it generated. Either way it is the whole secret — the
// key file Proxmox writes has no passphrase — and the operator decision is that
// Nexara never keeps it: not in the database, not in an audit row (view:audit
// is granted to every Viewer by default), not in an event, not in a log line,
// not in an error. The only place it may appear is the response to the one
// request that generated it.
//
// These tests drive the real Create and Update handlers end to end and look
// for the key everywhere it could land.

// storageKeyCanary is the key material in every fixture key: letters and
// hyphens only, so no rendering — JSON escaping, a quoted slog value — can
// disguise it from a plain substring search.
const storageKeyCanary = "CANARY-handler-pbs-key-material"

// storageKeyFile is a key file in the shape PBSPlugin reads back from
// /etc/pve/priv/storage/<storage>.enc. The fingerprint is synthetic.
func storageKeyFile(material string) string {
	return `{"kdf":null,"created":"2026-01-01T00:00:00+00:00","modified":"2026-01-01T00:00:00+00:00",` +
		`"data":"` + material + `","fingerprint":"aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:` +
		`aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"}`
}

var (
	storageKeyGenerated = storageKeyFile(storageKeyCanary + "-generated")
	storageKeySupplied  = storageKeyFile(storageKeyCanary + "-supplied")
	// storageKeyRefused is a key this stand-in Proxmox refuses, the way
	// PBSPlugin refuses one it cannot decode.
	storageKeyRefused = storageKeyFile(storageKeyCanary + "-refused")
)

// storageKeyCall is one request the stand-in Proxmox received, with the
// answer it gave.
type storageKeyCall struct {
	method string
	path   string
	form   url.Values
	answer string
}

// storageKeyProxmox stands in for pve-storage's POST /storage and PUT
// /storage/{storage} on a pbs storage, answering as API2/Storage/Config.pm and
// PBSPlugin's hooks do: {storage, type, config}, where config carries the
// generated key for autogen, an ECHO of a supplied key, and is {} on an update
// that leaves the key alone.
//
// With notPBS set it answers as a Proxmox before pve-storage 8.3.5 does for
// any other built-in storage type: Config.pm extracted encryption-key from a
// write to every type, and no built-in hook but PBSPlugin's reads it, so it is
// dropped without a word and the answer carries no config.
type storageKeyProxmox struct {
	notPBS bool
	mu     sync.Mutex
	calls  []storageKeyCall
}

func (p *storageKeyProxmox) since(n int) []storageKeyCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]storageKeyCall(nil), p.calls[n:]...)
}

func (p *storageKeyProxmox) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *storageKeyProxmox) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(body))

	status := http.StatusOK
	var answer string
	if key := form.Get("encryption-key"); key == storageKeyRefused {
		// PBSPlugin's own die for a key it cannot use, verbatim. Like every
		// refusal in its hooks it names no part of the value.
		status = http.StatusInternalServerError
		answer = `{"data":null,"message":"update storage failed: Value does not seems like a valid, ` +
			`JSON formatted encryption key!\n"}`
	} else {
		data := map[string]any{"storage": "store01", "type": "pbs"}
		switch {
		case p.notPBS:
			data["type"] = "dir"
		case key == proxmox.PBSEncryptionKeyAutogen:
			data["config"] = map[string]string{"encryption-key": storageKeyGenerated}
		case form.Has("encryption-key"):
			data["config"] = map[string]string{"encryption-key": key}
		case r.Method == http.MethodPut:
			data["config"] = map[string]string{}
		}
		raw, _ := json.Marshal(map[string]any{"data": data})
		answer = string(raw)
	}

	p.mu.Lock()
	p.calls = append(p.calls, storageKeyCall{method: r.Method, path: r.URL.Path, form: form, answer: answer})
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(answer))
}

// errStorageKeyAuditInsert fails every audit insert, which is what makes
// AuditLogAs write its "audit log insert failed" line. That line is the log
// capture's positive control: a real production log line on the request path,
// so "the key is not in the log" is asserted of a log that demonstrably
// records this request.
var errStorageKeyAuditInsert = errors.New("audit insert refused by the test")

// storageKeyDBTX answers GetStoragePool with one pool, records every
// statement, and fails every Exec with errStorageKeyAuditInsert. The audit
// insert is still recorded, arguments and all, before it fails.
type storageKeyDBTX struct {
	pool  db.StoragePool
	mu    sync.Mutex
	calls []capturedQuery
}

func (d *storageKeyDBTX) record(sql string, args []any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, capturedQuery{sql: sql, args: args})
}

func (d *storageKeyDBTX) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.record(sql, args)
	return pgconn.CommandTag{}, errStorageKeyAuditInsert
}

func (d *storageKeyDBTX) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	d.record(sql, args)
	return nil, errCaptured
}

func (d *storageKeyDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	d.record(sql, args)
	if strings.Contains(sql, "FROM storage_pools") {
		return storagePoolRow{pool: d.pool}
	}
	return failRow{err: errCaptured}
}

func (d *storageKeyDBTX) since(n int) []capturedQuery {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]capturedQuery(nil), d.calls[n:]...)
}

func (d *storageKeyDBTX) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

// storagePoolRow replays a db.StoragePool through pgx.Row, assigning
// positionally in the generated struct's field order — the order GetStoragePool's
// SELECT * scans in. A column added in the middle reports a type mismatch
// rather than shifting a value into the wrong field.
type storagePoolRow struct{ pool db.StoragePool }

func (r storagePoolRow) Scan(dest ...any) error {
	row := reflect.ValueOf(r.pool)
	if len(dest) != row.NumField() {
		return fmt.Errorf("scan got %d destinations, want %d", len(dest), row.NumField())
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		if ptr.Kind() != reflect.Pointer || ptr.Elem().Type() != row.Field(i).Type() {
			return fmt.Errorf("scan destination %d is %T, want *%s — the select column order changed",
				i, d, row.Field(i).Type())
		}
		ptr.Elem().Set(row.Field(i))
	}
	return nil
}

// lockedLog is the buffer the log capture writes into. The handler logs from
// Fiber's serving goroutine while the test reads from its own.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(b)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// captureProductionLog makes the default logger, for the rest of the test, the
// one cmd/nexara/main.go installs — slog's JSON handler — at DEBUG, the lowest
// level production can run at, so a key logged at any level is caught. slog.SetDefault
// also points the standard log package at that handler, so a log.Printf lands
// in the same buffer. It must stay sequential, for the reasons captureSlog
// gives in vms_test.go.
func captureProductionLog(t *testing.T) *lockedLog {
	t.Helper()
	out := &lockedLog{}
	savedLogger, savedWriter, savedFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(savedLogger)
		// Restoring a logger that uses slog's default handler does not undo
		// the redirect SetDefault made above, so undo it by hand.
		log.SetOutput(savedWriter)
		log.SetFlags(savedFlags)
	})
	return out
}

// storageCreateMirror mirrors createStorageParams in
// internal/api/registry_storage.go; package api imports this package, so the
// declaration cannot be imported here.
func storageCreateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"storage":    {Type: apischema.String, Format: "storage-id"},
		"type":       {Type: apischema.String, Enum: slices.Clone(StorageTypes)},
		"params":     {Type: apischema.Object, Optional: true},
	})
}

// storageUpdateMirror mirrors updateStorageParams and storageRowParams.
func storageUpdateMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"storage_id": {Type: apischema.String, Format: "uuid"},
		"params":     {Type: apischema.Object, Optional: true},
		"delete":     {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(1024)},
	})
}

// storageKeyHarness is one wired-up storage write pipeline: a real Fiber app
// carrying the real StorageHandler, a real proxmox.Client from a real
// ClientCache pointed at the stand-in Proxmox, a real events.Publisher over
// miniredis built the way production builds it (with the default logger), and
// the production-shaped log capture.
type storageKeyHarness struct {
	app       *fiber.App
	clusterID uuid.UUID
	poolID    uuid.UUID
	px        *storageKeyProxmox
	dbtx      *storageKeyDBTX
	rdb       *redis.Client
	pubsub    *redis.PubSub
	logs      *lockedLog
}

func newStorageKeyHarness(t *testing.T) *storageKeyHarness {
	t.Helper()
	return newStorageKeyHarnessFor(t, "pbs")
}

// newStorageKeyHarnessFor is newStorageKeyHarness around a pool of the given
// storage type; for anything but pbs, the stand-in Proxmox drops a key the way
// an older one does.
func newStorageKeyHarnessFor(t *testing.T, poolType string) *storageKeyHarness {
	t.Helper()

	// First, so every logger built below — the client cache's and the
	// publisher's, which both take slog.Default() — writes into it.
	logs := captureProductionLog(t)

	px := &storageKeyProxmox{notPBS: poolType != "pbs"}
	srv := httptest.NewServer(http.HandlerFunc(px.serve))
	t.Cleanup(srv.Close)

	clusterID := uuid.New()
	encrypted, err := crypto.Encrypt("token-secret-value", pathParamEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cache := proxmox.NewClientCache(pathParamCacheQueries{cluster: db.Cluster{
		ID:                   clusterID,
		Name:                 "cluster01",
		ApiUrl:               srv.URL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
		IsActive:             true,
	}}, pathParamEncKey, nil, nil)

	rdb := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	pubsub := rdb.PSubscribe(context.Background(), "nexara:*")
	t.Cleanup(func() { _ = pubsub.Close() })
	if _, err := pubsub.Receive(context.Background()); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	eventPub := events.NewPublisher(rdb, slog.Default())

	pool := db.StoragePool{
		ID:        uuid.New(),
		ClusterID: clusterID,
		NodeID:    uuid.New(),
		Storage:   "store01",
		Type:      poolType,
		Content:   "backup",
		Active:    true,
		Enabled:   true,
		Shared:    true,
	}
	dbtx := &storageKeyDBTX{pool: pool}
	handler := NewStorageHandler(db.New(dbtx), pathParamEncKey, eventPub)

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		SetProxmoxCacheLocal(c, cache)
		// Without an actor AuditLog returns at its first line, and every audit
		// assertion below would run against nothing.
		c.Locals("user_id", uuid.New())
		return c.Next()
	})
	app.Post("/api/v1/clusters/:cluster_id/storage",
		withRequestParams(t, storageCreateMirror(t), []string{"cluster_id"}, handler.Create))
	app.Put("/api/v1/clusters/:cluster_id/storage/:storage_id",
		withRequestParams(t, storageUpdateMirror(t), []string{"cluster_id", "storage_id"}, handler.Update))

	return &storageKeyHarness{
		app: app, clusterID: clusterID, poolID: pool.ID,
		px: px, dbtx: dbtx, rdb: rdb, pubsub: pubsub, logs: logs,
	}
}

// storageKeyExchange is everything one request produced, gathered from every
// place a key could have landed.
type storageKeyExchange struct {
	status       int
	cacheControl string
	body         string
	response     map[string]any
	proxmox      []storageKeyCall
	audits       []db.InsertAuditLogParams
	queries      []capturedQuery
	events       []string
	log          string
}

// do sends one storage write and gathers what it produced.
func (h *storageKeyHarness) do(t *testing.T, method, body string) storageKeyExchange {
	t.Helper()

	target := "/api/v1/clusters/" + h.clusterID.String() + "/storage"
	if method == http.MethodPut {
		target += "/" + h.poolID.String()
	}
	pxBefore, dbBefore, logBefore := h.px.count(), h.dbtx.count(), len(h.logs.String())

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	ex := storageKeyExchange{status: resp.StatusCode, cacheControl: resp.Header.Get("Cache-Control"), body: string(raw)}
	if err := json.Unmarshal(raw, &ex.response); err != nil {
		t.Fatalf("the response is not a JSON object: %v (%s)", err, raw)
	}
	ex.proxmox = h.px.since(pxBefore)
	ex.queries = h.dbtx.since(dbBefore)
	ex.audits = (&captureDBTX{calls: ex.queries}).auditInserts(t)
	ex.events = h.drainEvents(t)
	ex.log = h.logs.String()[logBefore:]
	return ex
}

// drainEvents returns every event published since the last drain.
//
// It publishes a sentinel of its own and reads up to it. Redis hands a
// subscriber its messages in publish order, and everything the request
// published went out before its response did — so reaching the sentinel means
// every one of those has been read, with no sleep to guess how long that takes.
func (h *storageKeyHarness) drainEvents(t *testing.T) []string {
	t.Helper()
	sentinel := "sentinel-" + uuid.NewString()
	if err := h.rdb.Publish(context.Background(), "nexara:test-sentinel", sentinel).Err(); err != nil {
		t.Fatalf("publish sentinel: %v", err)
	}
	var got []string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg, ok := <-h.pubsub.Channel():
			if !ok {
				t.Fatal("the event channel closed before the sentinel arrived")
			}
			if msg.Payload == sentinel {
				return got
			}
			got = append(got, msg.Payload)
		case <-deadline:
			t.Fatal("the sentinel never arrived, so the events gathered may be incomplete")
		}
	}
}

// responseKeys is the sorted key set of a response body.
func responseKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestStorageWriteHandsTheGeneratedKeyOnlyToItsOwnResponse pins the one place
// the key is allowed to appear: the response to the request that asked
// Proxmox to generate it — and only that response.
//
// The supplied-key rows are what "derived from what the caller sent" is
// about. Proxmox echoes a supplied key in exactly the field it returns a
// generated one in, so the precondition on each asserts that the echo was
// really in Proxmox's answer — a handler reading the answer instead of the
// request would then hand it back, and this test would say so.
func TestStorageWriteHandsTheGeneratedKeyOnlyToItsOwnResponse(t *testing.T) {
	h := newStorageKeyHarness(t)

	tests := []struct {
		name       string
		method     string
		body       string
		wantStatus int
		wantKey    string
	}{
		{"create, autogen", http.MethodPost,
			`{"storage":"store01","type":"pbs","params":{"encryption-key":"autogen"}}`,
			http.StatusCreated, storageKeyGenerated},
		{"create, supplied key", http.MethodPost,
			`{"storage":"store01","type":"pbs","params":{"encryption-key":` + jsonString(storageKeySupplied) + `}}`,
			http.StatusCreated, ""},
		{"create, no key", http.MethodPost,
			`{"storage":"store01","type":"pbs","params":{"datastore":"datastore01"}}`,
			http.StatusCreated, ""},
		{"update, autogen", http.MethodPut,
			`{"params":{"encryption-key":"autogen"}}`,
			http.StatusOK, storageKeyGenerated},
		{"update, supplied key", http.MethodPut,
			`{"params":{"encryption-key":` + jsonString(storageKeySupplied) + `}}`,
			http.StatusOK, ""},
		{"update, key untouched", http.MethodPut,
			`{"params":{"content":"backup"}}`,
			http.StatusOK, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := h.do(t, tt.method, tt.body)
			if ex.status != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", ex.status, ex.body, tt.wantStatus)
			}
			if len(ex.proxmox) != 1 {
				t.Fatalf("Proxmox received %d requests, want 1", len(ex.proxmox))
			}
			if strings.Contains(tt.name, "supplied") &&
				!strings.Contains(ex.proxmox[0].answer, storageKeyCanary+"-supplied") {
				t.Fatalf("precondition: Proxmox's answer did not echo the supplied key: %s", ex.proxmox[0].answer)
			}

			wantKeys := []string{"status", "storage"}
			if tt.wantKey != "" {
				wantKeys = []string{"generated_encryption_key", "status", "storage"}
			}
			if got := responseKeys(ex.response); !slices.Equal(got, wantKeys) {
				t.Errorf("response keys = %v, want exactly %v (body %s)", got, wantKeys, ex.body)
			}
			if got, _ := ex.response["generated_encryption_key"].(string); got != tt.wantKey {
				t.Errorf("generated_encryption_key = %q, want %q", got, tt.wantKey)
			}
			if got, _ := ex.response["storage"].(string); got != "store01" {
				t.Errorf("storage = %q, want store01", got)
			}
			// Only the response that carries the key needs to stay out of
			// every cache, and only it is marked.
			wantCache := ""
			if tt.wantKey != "" {
				wantCache = "no-store"
			}
			if ex.cacheControl != wantCache {
				t.Errorf("Cache-Control = %q, want %q", ex.cacheControl, wantCache)
			}
		})
	}
}

// TestStorageKeyNeverReachesAuditEventsOrLogs looks for the key in every sink
// a storage write feeds besides its own response: the audit row (every
// argument, not only details — and details are what the syslog forwarder
// sends on as well), every event the request published, and every log line
// written while it ran.
//
// Every row that succeeds has a positive control on each sink, so that "the
// key is absent" is never true merely because nothing was recorded: the audit
// row exists and says, in words, what happened to the key; the audit_entry
// event arrived; and the log holds AuditLogAs's insert-failure line for this
// very request (the stand-in database refuses the insert on purpose). And on
// every row the key really passed through: Proxmox was sent it, or the
// response carried it.
//
// The refused row covers the error path: Proxmox rejecting a supplied key must
// not bring it back in the error the caller sees, nor leave an audit row. It
// writes no audit row, so nothing on that request logs or publishes: its event
// and log checks rest on the same capture the other rows show to be live, and
// prove only that the failure added nothing.
func TestStorageKeyNeverReachesAuditEventsOrLogs(t *testing.T) {
	h := newStorageKeyHarness(t)

	tests := []struct {
		name        string
		method      string
		body        string
		wantStatus  int
		wantChange  string // the audit row's encryption_key; "" means no audit row at all
		keyInFlight func(storageKeyExchange) bool
	}{
		{
			name:       "create, autogen",
			method:     http.MethodPost,
			body:       `{"storage":"store01","type":"pbs","params":{"encryption-key":"autogen"}}`,
			wantStatus: http.StatusCreated,
			wantChange: "generated",
			keyInFlight: func(ex storageKeyExchange) bool {
				return strings.Contains(ex.body, storageKeyCanary)
			},
		},
		{
			name:       "create, supplied key",
			method:     http.MethodPost,
			body:       `{"storage":"store01","type":"pbs","params":{"encryption-key":` + jsonString(storageKeySupplied) + `}}`,
			wantStatus: http.StatusCreated,
			wantChange: "supplied",
			keyInFlight: func(ex storageKeyExchange) bool {
				return len(ex.proxmox) == 1 && strings.Contains(ex.proxmox[0].form.Get("encryption-key"), storageKeyCanary)
			},
		},
		{
			name:       "update, autogen",
			method:     http.MethodPut,
			body:       `{"params":{"encryption-key":"autogen"}}`,
			wantStatus: http.StatusOK,
			wantChange: "generated",
			keyInFlight: func(ex storageKeyExchange) bool {
				return strings.Contains(ex.body, storageKeyCanary)
			},
		},
		{
			name:       "update, supplied key",
			method:     http.MethodPut,
			body:       `{"params":{"encryption-key":` + jsonString(storageKeySupplied) + `}}`,
			wantStatus: http.StatusOK,
			wantChange: "supplied",
			keyInFlight: func(ex storageKeyExchange) bool {
				return len(ex.proxmox) == 1 && strings.Contains(ex.proxmox[0].form.Get("encryption-key"), storageKeyCanary)
			},
		},
		{
			name:       "update, supplied key Proxmox refuses",
			method:     http.MethodPut,
			body:       `{"params":{"encryption-key":` + jsonString(storageKeyRefused) + `}}`,
			wantStatus: http.StatusBadGateway,
			keyInFlight: func(ex storageKeyExchange) bool {
				return len(ex.proxmox) == 1 && strings.Contains(ex.proxmox[0].form.Get("encryption-key"), storageKeyCanary)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := h.do(t, tt.method, tt.body)
			if ex.status != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", ex.status, ex.body, tt.wantStatus)
			}
			if !tt.keyInFlight(ex) {
				t.Fatal("precondition: the key never passed through this request, so its absence below proves nothing")
			}

			// The audit row.
			if tt.wantChange == "" {
				if len(ex.audits) != 0 {
					t.Errorf("a refused write left %d audit rows, want none", len(ex.audits))
				}
				if msg, _ := ex.response["message"].(string); strings.Contains(msg, storageKeyCanary) {
					t.Errorf("the error the caller sees carries the key: %q", msg)
				}
			} else {
				if len(ex.audits) != 1 {
					t.Fatalf("the write left %d audit rows, want exactly 1 — with none, the audit "+
						"assertions below are vacuous", len(ex.audits))
				}
				var details map[string]any
				if err := json.Unmarshal(ex.audits[0].Details, &details); err != nil {
					t.Fatalf("audit details are not a JSON object: %v (%s)", err, ex.audits[0].Details)
				}
				if details["encryption_key"] != tt.wantChange {
					t.Errorf("audit details = %s, want encryption_key %q — the row should say what "+
						"happened to the key, in words", ex.audits[0].Details, tt.wantChange)
				}
			}
			for _, q := range ex.queries {
				for i, arg := range q.args {
					if strings.Contains(fmt.Sprint(arg), storageKeyCanary) || argBytesContain(arg, storageKeyCanary) {
						t.Errorf("database argument %d of %q carries the key: %v", i, firstLine(q.sql), arg)
					}
				}
			}

			// The events.
			sawAuditEntry := false
			for _, payload := range ex.events {
				if strings.Contains(payload, storageKeyCanary) {
					t.Errorf("an event payload carries the key: %s", payload)
				}
				var ev events.Event
				if json.Unmarshal([]byte(payload), &ev) == nil && ev.Kind == events.KindAuditEntry {
					sawAuditEntry = true
				}
			}
			if tt.wantChange != "" && !sawAuditEntry {
				t.Errorf("no audit_entry event among %d published — the event assertion above is vacuous",
					len(ex.events))
			}

			// The log.
			if strings.Contains(ex.log, storageKeyCanary) {
				t.Errorf("the log carries the key:\n%s", ex.log)
			}
			if tt.wantChange != "" && !strings.Contains(ex.log, "audit log insert failed") {
				t.Errorf("the log holds no line from this request, so the key's absence from it proves "+
					"nothing:\n%s", ex.log)
			}
		})
	}
}

// argBytesContain searches a byte-slice argument (a json.RawMessage detail
// blob, say), whose fmt rendering is a list of numbers rather than its text.
func argBytesContain(arg any, needle string) bool {
	switch v := arg.(type) {
	case []byte:
		return bytes.Contains(v, []byte(needle))
	case json.RawMessage:
		return bytes.Contains(v, []byte(needle))
	}
	return false
}

// firstLine trims a query to its first line for a failure message.
func firstLine(sql string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(sql), "\n")
	return line
}

// jsonString renders s as a JSON string literal for splicing into a request body.
func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// TestStorageUpdateForwardsTheDeleteListAndRefusesAContradiction drives the
// update route's delete list end to end: what reaches Proxmox, what is refused
// before anything does, and what the audit row says.
//
// Any setting name is forwarded, as the released handler forwarded its list
// untouched — which ones may be cleared is Proxmox's call. What is refused is a
// setting both written and cleared, and a delete smuggled in among the
// settings. Every refused row has a twin that differs only in the refused part
// and does reach Proxmox, so each refusal is shown to come from the rule it
// names.
func TestStorageUpdateForwardsTheDeleteListAndRefusesAContradiction(t *testing.T) {
	h := newStorageKeyHarness(t)

	t.Run("removing the key sends delete=encryption-key and nothing else", func(t *testing.T) {
		ex := h.do(t, http.MethodPut, `{"delete":"encryption-key"}`)
		if ex.status != http.StatusOK {
			t.Fatalf("status = %d (%s), want 200", ex.status, ex.body)
		}
		if len(ex.proxmox) != 1 {
			t.Fatalf("Proxmox received %d requests, want 1", len(ex.proxmox))
		}
		if want := (url.Values{"delete": {"encryption-key"}}); !reflect.DeepEqual(ex.proxmox[0].form, want) {
			t.Errorf("Proxmox was sent %v, want exactly %v", ex.proxmox[0].form, want)
		}
		if _, ok := ex.response["generated_encryption_key"]; ok {
			t.Errorf("a removal returned a key: %s", ex.body)
		}
		if len(ex.audits) != 1 || !strings.Contains(string(ex.audits[0].Details), `"encryption_key":"removed"`) {
			t.Errorf("audit rows = %+v, want one whose details say the key was removed", ex.audits)
		}
	})

	// A setting Nexara's own dialog never clears, alone and in a list mixing
	// every separator Proxmox's own lists use. Neither may leave a trace in
	// the audit row: only a key change does.
	for _, tt := range []struct {
		list string
		want string
	}{
		{"prune-backups", "prune-backups"},
		{"prune-backups;max-protected-backups namespace,nodes", "prune-backups,max-protected-backups,namespace,nodes"},
	} {
		t.Run("the list "+strconv.Quote(tt.list)+" is forwarded name for name", func(t *testing.T) {
			ex := h.do(t, http.MethodPut, `{"delete":`+jsonString(tt.list)+`}`)
			if ex.status != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", ex.status, ex.body)
			}
			if len(ex.proxmox) != 1 {
				t.Fatalf("Proxmox received %d requests, want 1", len(ex.proxmox))
			}
			if want := (url.Values{"delete": {tt.want}}); !reflect.DeepEqual(ex.proxmox[0].form, want) {
				t.Errorf("Proxmox was sent %v, want exactly %v", ex.proxmox[0].form, want)
			}
			if len(ex.audits) != 1 || strings.Contains(string(ex.audits[0].Details), "encryption_key") {
				t.Errorf("audit rows = %+v, want one that says nothing of a key", ex.audits)
			}
		})
	}

	// Proxmox's own lists split on commas, semicolons and whitespace alike
	// (split_list, pve-common src/PVE/ParseUtils.pm), and this route passed
	// the list straight through before it was checked. The audit row reads
	// the list the same way: the key's removal is recorded however the list
	// that names it is separated.
	for _, list := range []string{" nodes, ,encryption-key ", "nodes;encryption-key", "nodes encryption-key"} {
		t.Run("the list "+strconv.Quote(list)+" names both settings", func(t *testing.T) {
			ex := h.do(t, http.MethodPut, `{"delete":`+jsonString(list)+`}`)
			if ex.status != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", ex.status, ex.body)
			}
			if len(ex.proxmox) != 1 {
				t.Fatalf("Proxmox received %d requests, want 1", len(ex.proxmox))
			}
			if got := ex.proxmox[0].form.Get("delete"); got != "nodes,encryption-key" {
				t.Errorf("Proxmox was sent delete=%q, want nodes,encryption-key", got)
			}
			if len(ex.audits) != 1 || !strings.Contains(string(ex.audits[0].Details), `"encryption_key":"removed"`) {
				t.Errorf("audit rows = %+v, want one whose details say the key was removed", ex.audits)
			}
		})
	}

	refusals := []struct {
		name string
		body string
		twin string
	}{
		{"a key both generated and removed",
			`{"params":{"encryption-key":"autogen"},"delete":"encryption-key"}`,
			`{"params":{"encryption-key":"autogen"}}`},
		{"a key both generated and removed, among other settings",
			`{"params":{"encryption-key":"autogen"},"delete":"prune-backups;encryption-key"}`,
			`{"delete":"prune-backups;encryption-key"}`},
		{"a delete smuggled in among the settings",
			`{"params":{"delete":"password","content":"backup"}}`,
			`{"params":{"content":"backup"}}`},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			twin := h.do(t, http.MethodPut, tt.twin)
			if twin.status != http.StatusOK || len(twin.proxmox) != 1 {
				t.Fatalf("precondition: the twin %s answered %d after %d Proxmox requests, want 200 after 1",
					tt.twin, twin.status, len(twin.proxmox))
			}

			ex := h.do(t, http.MethodPut, tt.body)
			if ex.status != http.StatusBadRequest {
				t.Fatalf("status = %d (%s), want 400", ex.status, ex.body)
			}
			if len(ex.proxmox) != 0 {
				t.Errorf("Proxmox received %v, want no request at all", ex.proxmox)
			}
			if len(ex.audits) != 0 {
				t.Errorf("a refused update left %d audit rows", len(ex.audits))
			}
		})
	}
}

// TestStorageAuditClaimsAKeyChangeOnlyForPBS keeps the audit row honest about a
// storage that has no key to change.
//
// Before pve-storage 8.3.5 Proxmox accepted encryption-key on a write to any
// storage type and dropped it without a word — and a current one still does
// for a plugin that declares no sensitive-properties — so "generated" in the
// row of a dir storage would record something that never happened. The
// precondition on each row is that Proxmox really was sent autogen — the one
// input that would make the row claim it.
func TestStorageAuditClaimsAKeyChangeOnlyForPBS(t *testing.T) {
	h := newStorageKeyHarnessFor(t, "dir")

	for _, tt := range []struct {
		name       string
		method     string
		body       string
		wantStatus int
	}{
		{"create", http.MethodPost,
			`{"storage":"store01","type":"dir","params":{"path":"/mnt/store01","encryption-key":"autogen"}}`,
			http.StatusCreated},
		{"update", http.MethodPut, `{"params":{"encryption-key":"autogen"}}`, http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ex := h.do(t, tt.method, tt.body)
			if ex.status != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", ex.status, ex.body, tt.wantStatus)
			}
			if len(ex.proxmox) != 1 || ex.proxmox[0].form.Get("encryption-key") != proxmox.PBSEncryptionKeyAutogen {
				t.Fatalf("precondition: Proxmox was not sent encryption-key=autogen: %+v", ex.proxmox)
			}
			if len(ex.audits) != 1 {
				t.Fatalf("the write left %d audit rows, want exactly 1", len(ex.audits))
			}
			if strings.Contains(string(ex.audits[0].Details), "encryption_key") {
				t.Errorf("audit details = %s; a %s storage has no key, so the row must not say one changed",
					ex.audits[0].Details, "dir")
			}
		})
	}
}
