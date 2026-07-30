package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// testSettingsUserID is the caller identity the settings test app injects.
// Fixed rather than random so the user-scope assertions can prove the row is
// keyed on the caller's own ID — the property that makes "user" scope safe to
// leave ungated.
var testSettingsUserID = uuid.MustParse("11111111-2222-3333-4444-555555555555")

// newSettingsTestApp wires the real settings handlers with a nil *db.Queries,
// for cases expected to return from the scope gate before touching the
// database. That nil is itself part of the assertion — a request that slips
// past the gate panics instead of silently passing.
func newSettingsTestApp(t *testing.T) *fiber.App {
	t.Helper()
	return newSettingsApp(t, nil)
}

func newSettingsApp(t *testing.T, queries *db.Queries) *fiber.App {
	t.Helper()

	handler := NewSettingsHandler(queries, t.TempDir())

	app := fiber.New(fiber.Config{
		ErrorHandler: testErrorHandler,
	})

	app.Use(func(c fiber.Ctx) error {
		if role := c.Get("X-Test-Role"); role != "" {
			c.Locals("role", role)
			c.Locals("user_id", testSettingsUserID)
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	// Probe route: exercises settingScopeID directly so the paths that would
	// otherwise reach the database (an admin writing global, a user writing
	// their own scope) can still be asserted without one. An absent key query
	// param stands in for the bulk listing, which passes "".
	app.Get("/scope-probe", func(c fiber.Ctx) error {
		scopeID, err := settingScopeID(c, c.Query("key"), c.Query("scope"), c.Query("write") == "true")
		if err != nil {
			return err
		}
		out := "null"
		if scopeID.Valid {
			out = uuid.UUID(scopeID.Bytes).String()
		}
		return c.JSON(fiber.Map{"scope_id": out})
	})

	app.Get("/settings", handler.ListSettings)
	app.Get("/settings/:key", handler.GetSetting)
	app.Put("/settings/:key", handler.UpsertSetting)
	app.Delete("/settings/:key", handler.DeleteSetting)

	return app
}

// TestSettingScopeID covers the scope policy every settings handler shares:
// which scopes exist, who may write them, and which row each one keys on.
//
// "cluster" is rejected even for an admin. The settings table's CHECK
// constraint permits it, but no reader or writer uses it and these endpoints
// carry no cluster ID, so the row would key on scope_id = NULL — a second
// shared namespace, not per-cluster storage. It previously bypassed the write
// gate entirely.
func TestSettingScopeID(t *testing.T) {
	app := newSettingsTestApp(t)

	tests := []struct {
		name        string
		scope       string
		write       bool
		role        string
		wantStatus  int
		wantScopeID string
	}{
		{"global read as viewer", "global", false, "viewer", http.StatusOK, "null"},
		{"global read as admin", "global", false, "admin", http.StatusOK, "null"},
		{"global write as admin", "global", true, "admin", http.StatusOK, "null"},
		{"global write as viewer", "global", true, "viewer", http.StatusForbidden, ""},

		{"user read as viewer", "user", false, "viewer", http.StatusOK, testSettingsUserID.String()},
		{"user write as viewer", "user", true, "viewer", http.StatusOK, testSettingsUserID.String()},
		{"user write as admin", "user", true, "admin", http.StatusOK, testSettingsUserID.String()},
		{"user write unauthenticated", "user", true, "", http.StatusUnauthorized, ""},

		{"cluster read as admin", "cluster", false, "admin", http.StatusBadRequest, ""},
		{"cluster write as admin", "cluster", true, "admin", http.StatusBadRequest, ""},
		{"cluster write as viewer", "cluster", true, "viewer", http.StatusBadRequest, ""},

		{"unknown scope", "node", true, "admin", http.StatusBadRequest, ""},
		{"empty scope", "", true, "admin", http.StatusBadRequest, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/scope-probe?scope=" + tt.scope
			if tt.write {
				url += "&write=true"
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			if tt.role != "" {
				req.Header.Set("X-Test-Role", tt.role)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, tt.wantStatus, body)
			}
			if tt.wantScopeID == "" {
				return
			}

			var got struct {
				ScopeID string `json:"scope_id"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode body %s: %v", body, err)
			}
			if got.ScopeID != tt.wantScopeID {
				t.Errorf("scope_id = %q, want %q", got.ScopeID, tt.wantScopeID)
			}
		})
	}
}

// TestSettingScopesNoUngatedSharedNamespace pins the invariant the whole gate
// rests on: a scope may be a shared namespace (scope_id NULL, every user reads
// the same row) OR ungated for writes, never both. "cluster" was exactly that
// forbidden combination. The policy lives in a map, so a future entry added
// with a zero-value settingScope would silently reintroduce the hole — this
// catches it at build time rather than in review.
func TestSettingScopesNoUngatedSharedNamespace(t *testing.T) {
	for name, sc := range settingScopes {
		if !sc.perUser && !sc.adminOnly {
			t.Errorf("scope %q is a shared namespace with an ungated write — "+
				"give it adminOnly (gate the write) or perUser (key the row on the caller)", name)
		}
	}
}

// errCaptured is returned by captureDBTX once it has recorded the query args;
// the handler then fails out, which is fine — the assertion is on the args.
var errCaptured = errors.New("args captured")

// captureDBTX is a db.DBTX that records the arguments of the query a handler
// sends and then fails. It lets the tests assert on the scope and scope_id that
// actually reach Postgres — which the deny-path cases can never observe —
// without standing up a database.
//
// args staying nil is itself an assertion: it means the handler returned before
// issuing any query.
type captureDBTX struct {
	args []any
	// rows, when non-nil, is replayed to Query instead of failing — for the
	// handlers that filter a result set rather than just issuing the query.
	rows []db.Setting
}

func (c *captureDBTX) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	c.args = args
	return pgconn.CommandTag{}, nil
}

func (c *captureDBTX) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	c.args = args
	if c.rows == nil {
		return nil, errCaptured
	}
	return &settingRows{rows: c.rows}, nil
}

func (c *captureDBTX) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	c.args = args
	return errRow{}
}

type errRow struct{}

func (errRow) Scan(...any) error { return errCaptured }

// settingRows replays a fixed []db.Setting through pgx.Rows so a handler that
// reads a result set — rather than merely issuing the query — can be exercised
// without a database. Scan assigns positionally in the column order the
// generated settings queries select, and reports a mismatch rather than
// silently filling zero values if that order ever changes.
type settingRows struct {
	rows []db.Setting
	i    int
	err  error
}

func (r *settingRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}

func (r *settingRows) Scan(dest ...any) error {
	s := r.rows[r.i-1]
	cols := []any{s.ID, s.Key, s.Value, s.Scope, s.ScopeID, s.CreatedAt, s.UpdatedAt}
	if len(dest) != len(cols) {
		r.err = fmt.Errorf("scan got %d destinations, want %d", len(dest), len(cols))
		return r.err
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		if ptr.Kind() != reflect.Pointer {
			r.err = fmt.Errorf("scan destination %d is %T, not a pointer", i, d)
			return r.err
		}
		val := reflect.ValueOf(cols[i])
		if ptr.Elem().Type() != val.Type() {
			r.err = fmt.Errorf("scan destination %d is *%s, want *%s — the select column order changed",
				i, ptr.Elem().Type(), val.Type())
			return r.err
		}
		ptr.Elem().Set(val)
	}
	return nil
}

func (r *settingRows) Err() error                                 { return r.err }
func (*settingRows) Close()                                       {}
func (*settingRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*settingRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*settingRows) Values() ([]any, error)                       { return nil, errCaptured }
func (*settingRows) RawValues() [][]byte                          { return nil }
func (*settingRows) Conn() *pgx.Conn                              { return nil }

// newTestSetting builds a settings row with the fixed timestamps and a random
// ID; only key, scope and value matter to the assertions.
func newTestSetting(scope, key, value string) db.Setting {
	return db.Setting{
		ID:        uuid.New(),
		Key:       key,
		Value:     json.RawMessage(value),
		Scope:     scope,
		CreatedAt: time.Unix(0, 0).UTC(),
		UpdatedAt: time.Unix(0, 0).UTC(),
	}
}

// TestSettingsHandlersKeyRowsOnCaller asserts each handler forwards the gate's
// scope_id to the query rather than dropping it. Without this, a handler could
// call settingScopeID, discard the result, and pass a zero pgtype.UUID — every
// user's setting collapsing into one shared NULL-keyed row, which is the same
// bug class as the cluster scope this change removes. Every deny-path test
// would still pass.
//
// Reaching the query at all is the second assertion: it proves the read paths
// pass write=false. Flipping either one to true would 403 every non-admin SPA
// user without failing any deny-path case.
func TestSettingsHandlersKeyRowsOnCaller(t *testing.T) {
	caller := testSettingsUserID.String()

	tests := []struct {
		name        string
		method      string
		url         string
		body        string
		role        string
		scopeArg    int // index of Scope in the generated query args
		wantScope   string
		wantScopeID string // "" means SQL NULL
	}{
		// ListSettingsByScopeParams{Scope, ScopeID}
		{"list global as viewer", http.MethodGet, "/settings?scope=global", "", "viewer", 0, "global", ""},
		{"list user as viewer", http.MethodGet, "/settings?scope=user", "", "viewer", 0, "user", caller},
		// GetSettingParams{Key, Scope, ScopeID}
		{"get user as viewer", http.MethodGet, "/settings/k", "", "viewer", 1, "user", caller},
		{"get global as viewer", http.MethodGet, "/settings/k?scope=global", "", "viewer", 1, "global", ""},
		// UpsertSettingParams{Key, Value, Scope, ScopeID}
		{"put user as viewer", http.MethodPut, "/settings/k", `{"value":1}`, "viewer", 2, "user", caller},
		{"put global as admin", http.MethodPut, "/settings/k", `{"value":1,"scope":"global"}`, "admin", 2, "global", ""},
		// DeleteSettingParams{Key, Scope, ScopeID}
		{"delete user as viewer", http.MethodDelete, "/settings/k", "", "viewer", 1, "user", caller},
		{"delete global as admin", http.MethodDelete, "/settings/k?scope=global", "", "admin", 1, "global", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &captureDBTX{}
			app := newSettingsApp(t, db.New(capture))

			var req *http.Request
			if tt.body == "" {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			} else {
				req = httptest.NewRequest(tt.method, tt.url, bytes.NewBufferString(tt.body))
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("X-Test-Role", tt.role)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if capture.args == nil {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("query never reached (status %d) — the scope gate rejected a request it should allow, body: %s",
					resp.StatusCode, body)
			}
			if len(capture.args) <= tt.scopeArg+1 {
				t.Fatalf("query got %d args, want at least %d", len(capture.args), tt.scopeArg+2)
			}

			gotScope, ok := capture.args[tt.scopeArg].(string)
			if !ok {
				t.Fatalf("arg %d = %#v, want a scope string", tt.scopeArg, capture.args[tt.scopeArg])
			}
			if gotScope != tt.wantScope {
				t.Errorf("scope sent to query = %q, want %q", gotScope, tt.wantScope)
			}

			gotID, ok := capture.args[tt.scopeArg+1].(pgtype.UUID)
			if !ok {
				t.Fatalf("arg %d = %#v, want pgtype.UUID", tt.scopeArg+1, capture.args[tt.scopeArg+1])
			}
			switch {
			case tt.wantScopeID == "" && gotID.Valid:
				t.Errorf("scope_id = %s, want NULL", uuid.UUID(gotID.Bytes))
			case tt.wantScopeID != "" && !gotID.Valid:
				t.Errorf("scope_id = NULL, want %s — the row is shared instead of keyed on the caller", tt.wantScopeID)
			case tt.wantScopeID != "" && uuid.UUID(gotID.Bytes).String() != tt.wantScopeID:
				t.Errorf("scope_id = %s, want %s", uuid.UUID(gotID.Bytes), tt.wantScopeID)
			}
		})
	}
}

// TestSettingsHandlersEnforceScopeGate asserts the four settings endpoints
// actually route through settingScopeID — a correct policy helper is worthless
// if a handler forgets to call it. Only outcomes that short-circuit before the
// database are listed, so the nil *db.Queries is never reached.
func TestSettingsHandlersEnforceScopeGate(t *testing.T) {
	app := newSettingsTestApp(t)

	tests := []struct {
		name       string
		method     string
		url        string
		body       string
		role       string
		wantStatus int
	}{
		// The reported hole: cluster scope skipped the permission check on
		// both write paths, so any authenticated user could create, overwrite
		// or delete entries in the shared cluster namespace.
		{"put cluster as viewer", http.MethodPut, "/settings/k", `{"value":1,"scope":"cluster"}`, "viewer", http.StatusBadRequest},
		{"put cluster as admin", http.MethodPut, "/settings/k", `{"value":1,"scope":"cluster"}`, "admin", http.StatusBadRequest},
		{"delete cluster as viewer", http.MethodDelete, "/settings/k?scope=cluster", "", "viewer", http.StatusBadRequest},
		{"delete cluster as admin", http.MethodDelete, "/settings/k?scope=cluster", "", "admin", http.StatusBadRequest},
		{"get cluster as viewer", http.MethodGet, "/settings/k?scope=cluster", "", "viewer", http.StatusBadRequest},
		{"list cluster as viewer", http.MethodGet, "/settings?scope=cluster", "", "viewer", http.StatusBadRequest},

		// Global writes stay admin-only on both paths. DeleteSetting used to
		// skip validation entirely, so an unknown scope silently no-opped.
		{"put global as viewer", http.MethodPut, "/settings/k", `{"value":1,"scope":"global"}`, "viewer", http.StatusForbidden},
		{"delete global as viewer", http.MethodDelete, "/settings/k?scope=global", "", "viewer", http.StatusForbidden},
		{"put unknown scope as admin", http.MethodPut, "/settings/k", `{"value":1,"scope":"node"}`, "admin", http.StatusBadRequest},
		{"delete unknown scope as admin", http.MethodDelete, "/settings/k?scope=node", "", "admin", http.StatusBadRequest},
		{"get unknown scope as viewer", http.MethodGet, "/settings/k?scope=node", "", "viewer", http.StatusBadRequest},
		{"list unknown scope as viewer", http.MethodGet, "/settings?scope=node", "", "viewer", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			if tt.body == "" {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			} else {
				req = httptest.NewRequest(tt.method, tt.url, bytes.NewBufferString(tt.body))
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("X-Test-Role", tt.role)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tt.wantStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, tt.wantStatus, body)
			}
		})
	}
}

// TestSettingScopeIDRefusesReservedKeys covers the second half of the gate:
// which keys the generic endpoints may touch at all. Shared-scope reads here
// are ungated by design, so a key whose dedicated handler gates on a narrower
// permission (syslog_forwarding on manage:audit) would otherwise be readable by
// every authenticated account and writable by anyone with manage:settings.
//
// The refusal is unconditional — an admin gets it too. Holding manage:settings
// must not be a second route to a manage:audit-owned key, so there is no role
// that turns these cases into a 200.
func TestSettingScopeIDRefusesReservedKeys(t *testing.T) {
	app := newSettingsTestApp(t)

	tests := []struct {
		name        string
		key         string
		scope       string
		write       bool
		role        string
		wantStatus  int
		wantScopeID string
	}{
		{"reserved global read as viewer", syslogSettingKey, "global", false, "viewer", http.StatusForbidden, ""},
		{"reserved global read as admin", syslogSettingKey, "global", false, "admin", http.StatusForbidden, ""},
		{"reserved global write as admin", syslogSettingKey, "global", true, "admin", http.StatusForbidden, ""},
		{"reserved global write as viewer", syslogSettingKey, "global", true, "viewer", http.StatusForbidden, ""},

		// The reservation binds to the shared row, not the name: a user-scope
		// row of the same key is keyed on the caller and reachable by nobody
		// else, so refusing it would be gratuitous.
		{"reserved key in user scope reads", syslogSettingKey, "user", false, "viewer", http.StatusOK, testSettingsUserID.String()},
		{"reserved key in user scope writes", syslogSettingKey, "user", true, "viewer", http.StatusOK, testSettingsUserID.String()},

		// An unreserved global key is unaffected — this is the branding fetch
		// the ungated global read exists for.
		{"unreserved global read as viewer", "branding.app_title", "global", false, "viewer", http.StatusOK, "null"},
		{"unreserved global write as admin", "branding.app_title", "global", true, "admin", http.StatusOK, "null"},

		// An unknown scope is still a 400, not a 403: the scope is validated
		// before the key is classified.
		{"reserved key in unknown scope", syslogSettingKey, "node", false, "admin", http.StatusBadRequest, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/scope-probe?scope=" + tt.scope + "&key=" + tt.key
			if tt.write {
				url += "&write=true"
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("X-Test-Role", tt.role)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, tt.wantStatus, body)
			}
			if tt.wantScopeID == "" {
				return
			}

			var got struct {
				ScopeID string `json:"scope_id"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode body %s: %v", body, err)
			}
			if got.ScopeID != tt.wantScopeID {
				t.Errorf("scope_id = %q, want %q", got.ScopeID, tt.wantScopeID)
			}
		})
	}
}

// TestSettingsHandlersRefuseReservedGlobalKeys asserts each keyed endpoint
// actually enforces the reservation, and — the load-bearing half — that it does
// so before issuing any query. A handler that returned 403 after reading the
// row would still have leaked nothing, but one that read and then forgot to
// check would pass a status-only assertion; capture.args pins the ordering.
func TestSettingsHandlersRefuseReservedGlobalKeys(t *testing.T) {
	reservedURL := "/settings/" + syslogSettingKey

	tests := []struct {
		name    string
		method  string
		url     string
		body    string
		role    string
		refused bool // 403 naming the owner, and no query issued
	}{
		{"get reserved global as viewer", http.MethodGet, reservedURL + "?scope=global", "", "viewer", true},
		{"get reserved global as admin", http.MethodGet, reservedURL + "?scope=global", "", "admin", true},
		{"put reserved global as admin", http.MethodPut, reservedURL, `{"value":{"enabled":false},"scope":"global"}`, "admin", true},
		{"put reserved global as viewer", http.MethodPut, reservedURL, `{"value":{"enabled":false},"scope":"global"}`, "viewer", true},
		{"delete reserved global as admin", http.MethodDelete, reservedURL + "?scope=global", "", "admin", true},
		{"delete reserved global as viewer", http.MethodDelete, reservedURL + "?scope=global", "", "viewer", true},

		// Same key, own row: allowed through to the query.
		{"get reserved key in user scope", http.MethodGet, reservedURL, "", "viewer", false},
		{"put reserved key in user scope", http.MethodPut, reservedURL, `{"value":1}`, "viewer", false},
		{"delete reserved key in user scope", http.MethodDelete, reservedURL, "", "viewer", false},

		// Unreserved global keys still work for the roles that could always
		// reach them.
		{"get unreserved global as viewer", http.MethodGet, "/settings/branding.app_title?scope=global", "", "viewer", false},
		{"put unreserved global as admin", http.MethodPut, "/settings/branding.app_title", `{"value":"Nexara","scope":"global"}`, "admin", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &captureDBTX{}
			app := newSettingsApp(t, db.New(capture))

			var req *http.Request
			if tt.body == "" {
				req = httptest.NewRequest(tt.method, tt.url, nil)
			} else {
				req = httptest.NewRequest(tt.method, tt.url, bytes.NewBufferString(tt.body))
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("X-Test-Role", tt.role)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)

			if !tt.refused {
				if capture.args == nil {
					t.Fatalf("query never reached (status %d) — the gate refused a request it should allow, body: %s",
						resp.StatusCode, body)
				}
				return
			}

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusForbidden, body)
			}
			if capture.args != nil {
				t.Errorf("the query ran anyway with args %#v — the reserved key reached the database", capture.args)
			}
			if owner := reservedGlobalSettings[syslogSettingKey]; !strings.Contains(string(body), owner) {
				t.Errorf("body %s does not name the owning endpoint %q — the caller has nowhere to go", body, owner)
			}
		})
	}
}

// TestListSettingsExcludesReservedGlobalKeys covers the bulk path, which takes
// no key and so cannot be refused by the gate. GET /settings?scope=global dumps
// every shared row at once, which would hand a Viewer the syslog config the
// keyed endpoint just refused them.
func TestListSettingsExcludesReservedGlobalKeys(t *testing.T) {
	const syslogValue = `{"enabled":true,"host":"siem.internal","port":6514,"protocol":"tls"}`

	tests := []struct {
		name     string
		scope    string
		wantKeys []string
	}{
		{"global listing drops the owned key", "global", []string{"branding.app_title"}},
		{"user listing keeps it — the row is the caller's own", "user", []string{"branding.app_title", syslogSettingKey}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &captureDBTX{rows: []db.Setting{
				newTestSetting(tt.scope, "branding.app_title", `"Nexara"`),
				newTestSetting(tt.scope, syslogSettingKey, syslogValue),
			}}
			app := newSettingsApp(t, db.New(capture))

			req := httptest.NewRequest(http.MethodGet, "/settings?scope="+tt.scope, nil)
			req.Header.Set("X-Test-Role", "viewer")

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, body)
			}

			var got []settingResponse
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode body %s: %v", body, err)
			}
			gotKeys := make([]string, len(got))
			for i, s := range got {
				gotKeys[i] = s.Key
			}
			if strings.Join(gotKeys, ",") != strings.Join(tt.wantKeys, ",") {
				t.Errorf("keys = %v, want %v", gotKeys, tt.wantKeys)
			}

			// The values ride along with the keys, so pin the disclosure
			// itself: the SIEM host must not appear in a global listing.
			if leaked := strings.Contains(string(body), "siem.internal"); leaked != (tt.scope != "global") {
				t.Errorf("SIEM host present in body = %v, want %v: %s", leaked, tt.scope != "global", body)
			}
		})
	}
}

// unreservedGlobalSettings are the shared-scope keys the generic endpoints may
// serve, each with the reason it is safe to expose. It exists purely to keep
// TestGuard_GlobalSettingKeysClassified exhaustive: together with
// reservedGlobalSettings it classifies every global key written from Go.
var unreservedGlobalSettings = map[string]string{
	"branding.logo_url":    "public branding asset URL; every signed-in user's SPA renders it",
	"branding.favicon_url": "public branding asset URL; every signed-in user's SPA renders it",
	// Written only by the SPA's Branding page through the generic PUT, so the
	// guard never sees it. Listed anyway so the two maps together stay a
	// complete inventory of the global keys in use.
	"branding.app_title": "public application name; every signed-in user's SPA renders it",
}

// settingParamsTypes are the sqlc param structs that carry a single setting
// key. ListSettingsByScopeParams is absent on purpose — it has no key.
var settingParamsTypes = map[string]bool{
	"GetSettingParams":    true,
	"UpsertSettingParams": true,
	"DeleteSettingParams": true,
}

// TestGuard_GlobalSettingKeysClassified is the drift guard behind the reserved
// list, in the same static-analysis style as tracktask_guard_test.go. It parses
// internal/api and internal/api/handlers and fails on any global setting key
// that is in neither reservedGlobalSettings nor unreservedGlobalSettings.
//
// Without it the reservation only covers keys someone remembered to add. The
// disclosure the list was written for is structural: shared-scope reads on the
// generic endpoints are ungated, so the next global key holding a secret is
// world-readable the day it lands. This forces its author to classify it.
//
// Only statically resolvable keys are checked — a string literal, or an
// identifier bound to a package-level string const. A global write whose key it
// cannot resolve is itself a failure, since that key would slip the guard.
//
// Two limits worth knowing. It reads the two Go packages that write settings
// today; a third would need adding here. And it cannot see keys the SPA writes
// through the generic PUT (branding.app_title is one) — those are classified by
// hand in unreservedGlobalSettings.
func TestGuard_GlobalSettingKeysClassified(t *testing.T) {
	for _, dir := range []string{".", ".."} {
		fset, files := parseGoFiles(t, dir)
		consts := packageStringConsts(files)

		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isSettingParamsType(lit.Type) {
					return true
				}
				fields := compositeFields(lit)
				pos := fset.Position(lit.Pos())

				scope, ok := resolveString(fields["Scope"], consts)
				if !ok {
					// The generic handlers pass the request's scope, and are
					// the reservation's own enforcement point — nowhere else
					// gets to write a setting under a scope chosen at runtime,
					// or the key would never be classified at all.
					if filepath.Base(pos.Filename) != "settings.go" {
						t.Errorf("%s: writes a setting under a scope this guard cannot resolve statically.\n"+
							"\tOnly the generic handlers in settings.go may do that. Use a literal scope here, "+
							"or route the write through settingScopeID.", pos)
					}
					return true
				}
				if scope != "global" {
					return true
				}

				key, ok := resolveString(fields["Key"], consts)
				if !ok {
					t.Errorf("%s: writes a global setting under a key this guard cannot resolve statically.\n"+
						"\tUse a string literal or a package-level string const so the key can be classified "+
						"in reservedGlobalSettings or unreservedGlobalSettings (settings_scope_test.go).", pos)
					return true
				}
				if reservedGlobalSettings[key] == "" && unreservedGlobalSettings[key] == "" {
					t.Errorf("%s: global setting key %q is classified in neither reservedGlobalSettings nor "+
						"unreservedGlobalSettings.\n"+
						"\tGlobal reads on GET /api/v1/settings are ungated, so this key is readable by every "+
						"authenticated user and writable by any manage:settings holder.\n"+
						"\tIf a dedicated endpoint owns it, add it to reservedGlobalSettings "+
						"(internal/api/handlers/settings.go); if it is safe for everyone to read, record why "+
						"in unreservedGlobalSettings.", pos, key)
				}
				return true
			})
		}
	}
}

// packageStringConsts collects package-level `const name = "literal"` bindings
// so a key referenced through its owner's const (syslogSettingKey) resolves.
func packageStringConsts(files []*ast.File) map[string]string {
	consts := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != len(vs.Values) {
					continue
				}
				for i, name := range vs.Names {
					if s, ok := stringLiteral(vs.Values[i]); ok {
						consts[name.Name] = s
					}
				}
			}
		}
	}
	return consts
}

// isSettingParamsType reports whether a composite literal's type is one of the
// sqlc setting param structs, i.e. `db.GetSettingParams` and friends.
func isSettingParamsType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && settingParamsTypes[sel.Sel.Name]
}

// compositeFields indexes a composite literal's `Field: value` elements by
// field name.
func compositeFields(lit *ast.CompositeLit) map[string]ast.Expr {
	fields := map[string]ast.Expr{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if name, ok := kv.Key.(*ast.Ident); ok {
			fields[name.Name] = kv.Value
		}
	}
	return fields
}

// resolveString reads a string literal, or an identifier bound to a
// package-level string const.
func resolveString(expr ast.Expr, consts map[string]string) (string, bool) {
	if expr == nil {
		return "", false
	}
	if s, ok := stringLiteral(expr); ok {
		return s, true
	}
	id, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	s, ok := consts[id.Name]
	return s, ok
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}
