package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
	// their own scope) can still be asserted without one.
	app.Get("/scope-probe", func(c fiber.Ctx) error {
		scopeID, err := settingScopeID(c, c.Query("scope"), c.Query("write") == "true")
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
type captureDBTX struct{ args []any }

func (c *captureDBTX) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	c.args = args
	return pgconn.CommandTag{}, nil
}

func (c *captureDBTX) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	c.args = args
	return nil, errCaptured
}

func (c *captureDBTX) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	c.args = args
	return errRow{}
}

type errRow struct{}

func (errRow) Scan(...any) error { return errCaptured }

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
