package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestApplyAuditListScope pins the load-bearing wiring: the caller's view:audit
// scope must land on BOTH query params — the count one especially, since Total
// goes to the client whole and no per-row guard can repair a number. Dropping
// the countP assignment (the original leak) fails here.
//
// It also pins auditScope's nil/non-nil contract, which Export and ListRecent
// consume directly: nil means SQL NULL means every cluster, so a scoped caller
// must always come back non-nil, empty or not.
func TestApplyAuditListScope(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()

	tests := []struct {
		name      string
		access    clusterAccess
		wantQuery bool // false = caller may skip the DB entirely
		wantNil   bool // expect nil scope (global = no SQL restriction)
		wantIDs   []uuid.UUID
	}{
		{
			name:      "global access queries unrestricted",
			access:    clusterAccess{HasGlobal: true},
			wantQuery: true,
			wantNil:   true,
		},
		{
			name:      "global wins over a populated Allowed map",
			access:    clusterAccess{HasGlobal: true, Allowed: map[uuid.UUID]bool{clusterA: true}},
			wantQuery: true,
			wantNil:   true,
		},
		{
			name:      "scoped access stamps the grant set on list and count",
			access:    clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: true, clusterB: true}},
			wantQuery: true,
			wantIDs:   []uuid.UUID{clusterA, clusterB},
		},
		{
			name:      "explicit false entry is excluded, matching PermitsCluster",
			access:    clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: true, clusterB: false}},
			wantQuery: true,
			wantIDs:   []uuid.UUID{clusterA},
		},
		{
			// The scope is still stamped — '{}' matches nothing — so a caller
			// that ignores the false return reads an empty set rather than
			// every cluster's entries.
			name:      "no grants stamps an empty scope and reports skippable",
			access:    clusterAccess{Allowed: map[uuid.UUID]bool{}},
			wantQuery: false,
			wantIDs:   []uuid.UUID{},
		},
		{
			name:      "zero-value access stamps an empty scope and reports skippable",
			access:    clusterAccess{},
			wantQuery: false,
			wantIDs:   []uuid.UUID{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var listP db.ListAuditLogAdvancedParams
			var countP db.CountAuditLogAdvancedParams

			got := applyAuditListScope(tt.access, &listP, &countP)
			if got != tt.wantQuery {
				t.Fatalf("applyAuditListScope() = %v, want %v", got, tt.wantQuery)
			}

			// The bare form Export and ListRecent use must agree with it.
			bare, bareQuery := auditScope(tt.access)
			if bareQuery != tt.wantQuery {
				t.Fatalf("auditScope() query = %v, want %v", bareQuery, tt.wantQuery)
			}

			assertScope(t, "list", listP.AccessibleClusterIds, tt.wantNil, tt.wantIDs)
			assertScope(t, "count", countP.AccessibleClusterIds, tt.wantNil, tt.wantIDs)
			assertScope(t, "auditScope", bare, tt.wantNil, tt.wantIDs)
		})
	}
}

// TestParseAuditFilters_AlwaysScopes covers what TestApplyAuditListScope
// cannot: that the params List and Export actually run are scoped. Both build
// them through parseAuditFilters, so the scope is stamped there rather than by
// each caller — an unscoped struct is unreachable, not merely unused.
//
// Every path out of the function is checked, error paths included: a caller
// that acted on the params despite the error would otherwise read across
// clusters. The scope is stamped before any parse can fail, which is what
// makes that hold.
func TestParseAuditFilters_AlwaysScopes(t *testing.T) {
	clusterA := uuid.New()
	handler := NewAuditHandler(nil, nil)

	tests := []struct {
		name      string
		access    clusterAccess
		query     string
		wantErr   bool
		wantQuery bool
		wantNil   bool
		wantIDs   []uuid.UUID
	}{
		{
			name:      "global access is unrestricted",
			access:    clusterAccess{HasGlobal: true},
			wantQuery: true,
			wantNil:   true,
		},
		{
			name:      "scoped access carries the grant set",
			access:    clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: true}},
			wantQuery: true,
			wantIDs:   []uuid.UUID{clusterA},
		},
		{
			name:      "no grants carries an empty scope",
			access:    clusterAccess{Allowed: map[uuid.UUID]bool{}},
			wantQuery: false,
			wantIDs:   []uuid.UUID{},
		},
		{
			name:      "scope survives a rejected filter",
			access:    clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: true}},
			query:     "?user_id=not-a-uuid",
			wantErr:   true,
			wantQuery: true,
			wantIDs:   []uuid.UUID{clusterA},
		},
		{
			name:      "scope survives a rejected filter for a scope-less caller",
			access:    clusterAccess{},
			query:     "?start_time=yesterday",
			wantErr:   true,
			wantQuery: false,
			wantIDs:   []uuid.UUID{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A probe route is the only way to hold a real fiber.Ctx; the
			// assertions run inside it and report through the closure. `ran`
			// is load-bearing: without it a route that stopped matching would
			// 404, the assertions would never execute, and the test would
			// report success having checked nothing.
			ran := false
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Get("/probe", func(c fiber.Ctx) error {
				ran = true
				listP, countP, query, err := handler.parseAuditFilters(c, tt.access)
				if (err != nil) != tt.wantErr {
					t.Errorf("parseAuditFilters err = %v, wantErr %v", err, tt.wantErr)
				}
				if query != tt.wantQuery {
					t.Errorf("parseAuditFilters query = %v, want %v", query, tt.wantQuery)
				}
				assertScope(t, "list", listP.AccessibleClusterIds, tt.wantNil, tt.wantIDs)
				assertScope(t, "count", countP.AccessibleClusterIds, tt.wantNil, tt.wantIDs)
				return c.SendStatus(http.StatusOK)
			})

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/probe"+tt.query, nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			_ = resp.Body.Close()
			if !ran {
				t.Fatalf("probe handler never ran (status %d) — no assertion was made", resp.StatusCode)
			}
		})
	}
}

// TestParseAuditFilters_ClampsPagination pins the LIMIT/OFFSET bounds.
// safeconv.Int32 bounds only the int32 range, so before the low clamps a
// negative ?limit= reached Postgres as `LIMIT -1`, which it rejects — the
// caller got a 500 from a query that never should have been sent.
func TestParseAuditFilters_ClampsPagination(t *testing.T) {
	handler := NewAuditHandler(nil, nil)
	access := clusterAccess{HasGlobal: true}

	tests := []struct {
		name       string
		query      string
		wantLimit  int32
		wantOffset int32
	}{
		{name: "defaults", wantLimit: 50},
		{name: "negative limit clamps to 1", query: "?limit=-1", wantLimit: 1},
		{name: "zero limit clamps to 1", query: "?limit=0", wantLimit: 1},
		{name: "limit above the cap clamps to 200", query: "?limit=500", wantLimit: 200},
		{name: "limit inside the range is kept", query: "?limit=25", wantLimit: 25},
		{name: "negative offset clamps to 0", query: "?limit=10&offset=-1", wantLimit: 10},
		{name: "offset is kept", query: "?limit=10&offset=40", wantLimit: 10, wantOffset: 40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Get("/probe", func(c fiber.Ctx) error {
				ran = true
				listP, _, _, err := handler.parseAuditFilters(c, access)
				if err != nil {
					t.Errorf("parseAuditFilters: %v", err)
					return c.SendStatus(http.StatusOK)
				}
				if listP.Limit != tt.wantLimit {
					t.Errorf("Limit = %d, want %d", listP.Limit, tt.wantLimit)
				}
				if listP.Offset != tt.wantOffset {
					t.Errorf("Offset = %d, want %d", listP.Offset, tt.wantOffset)
				}
				return c.SendStatus(http.StatusOK)
			})

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/probe"+tt.query, nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			_ = resp.Body.Close()
			if !ran {
				t.Fatalf("probe handler never ran (status %d) — no assertion was made", resp.StatusCode)
			}
		})
	}
}

// assertScope checks one stamped scope against the nil/non-nil contract: nil
// only for global access, since nil reaches SQL as NULL and lifts the filter.
func assertScope(t *testing.T, label string, got []uuid.UUID, wantNil bool, wantIDs []uuid.UUID) {
	t.Helper()

	if wantNil {
		if got != nil {
			t.Errorf("%s: global access must leave scope nil, got %v", label, got)
		}
		return
	}
	if got == nil {
		t.Errorf("%s: scope must be non-nil for scoped access "+
			"(nil reads as SQL NULL = every cluster)", label)
		return
	}
	if len(got) != len(wantIDs) {
		t.Errorf("%s: scope = %v, want %d IDs", label, got, len(wantIDs))
		return
	}
	// Map iteration order is unspecified — compare as sets.
	gotSet := make(map[uuid.UUID]bool, len(got))
	for _, id := range got {
		gotSet[id] = true
	}
	for _, want := range wantIDs {
		if !gotSet[want] {
			t.Errorf("%s: scope = %v, missing %v", label, got, want)
		}
	}
}

// newAuditScopeTestApp wires the three scoped audit reads with NIL queries:
// every case below must resolve before any DB access. A caller with no
// view:audit grant must short-circuit ahead of the DB — pre-fix, List reached
// CountAuditLogAdvanced and leaked a cross-cluster entry count into Total,
// while Export and ListRecent fetched global rows and trimmed them after.
// Here any DB access panics on the nil Queries and fails loudly. The SQL scope
// filter is the actual access control; TestApplyAuditListScope pins the
// wiring, and TestAuditScopeSQL_ExcludesNullCluster (internal/db) pins the
// clause it wires to.
func newAuditScopeTestApp(t *testing.T) *fiber.App {
	t.Helper()
	handler := NewAuditHandler(nil, nil)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	installTestRoleMiddleware(app)
	app.Get("/audit-log", handler.List)
	app.Get("/audit-log/recent", handler.ListRecent)
	app.Get("/audit-log/export", handler.Export)
	return app
}

// auditScopeRequest issues one request against the nil-queries app and returns
// the status and body.
func auditScopeRequest(t *testing.T, app *fiber.App, path, role string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body %s: %v", path, err)
	}
	return resp.StatusCode, string(body)
}

func TestAuditList_ScopeGatesBeforeDB(t *testing.T) {
	app := newAuditScopeTestApp(t)
	clusterID := uuid.New().String()

	tests := []struct {
		name   string
		role   string // empty = unauthenticated (no user_id local)
		query  string
		status int
	}{
		{
			name:   "no view:audit grant returns an empty page without touching the DB",
			role:   "viewer",
			status: http.StatusOK,
		},
		{
			name:   "cluster filter outside the access set is rejected",
			role:   "viewer",
			query:  "?cluster_id=" + clusterID,
			status: http.StatusForbidden,
		},
		{
			name:   "invalid cluster_id filter",
			role:   "viewer",
			query:  "?cluster_id=not-a-uuid",
			status: http.StatusBadRequest,
		},
		{
			name:   "invalid user_id filter",
			role:   "viewer",
			query:  "?user_id=not-a-uuid",
			status: http.StatusBadRequest,
		},
		{
			name:   "invalid start_time filter",
			role:   "viewer",
			query:  "?start_time=yesterday",
			status: http.StatusBadRequest,
		},
		{
			name:   "unauthenticated fails loud",
			status: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := auditScopeRequest(t, app, "/audit-log"+tt.query, tt.role)
			if status != tt.status {
				t.Fatalf("status = %d, want %d (body: %s)", status, tt.status, body)
			}
			if tt.status != http.StatusOK {
				return
			}
			var got auditListResponse
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decode body %q: %v", body, err)
			}
			if got.Total != 0 || len(got.Items) != 0 {
				t.Fatalf("scope-less user must get total=0 and no items, got %s", body)
			}
			// Pin the wire shape: items must serialize as [], never null.
			if !strings.Contains(body, `"items":[]`) {
				t.Fatalf("items must be an empty array, got %s", body)
			}
		})
	}
}

func TestAuditListRecent_ScopeGatesBeforeDB(t *testing.T) {
	app := newAuditScopeTestApp(t)

	t.Run("no view:audit grant returns an empty feed without touching the DB", func(t *testing.T) {
		status, body := auditScopeRequest(t, app, "/audit-log/recent", "viewer")
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", status, body)
		}
		var got []auditLogResponse
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("decode body %q: %v", body, err)
		}
		if len(got) != 0 {
			t.Fatalf("scope-less user must get no entries, got %s", body)
		}
		if strings.TrimSpace(body) != "[]" {
			t.Fatalf("feed must serialize as [], got %s", body)
		}
	})

	t.Run("unauthenticated fails loud", func(t *testing.T) {
		status, body := auditScopeRequest(t, app, "/audit-log/recent", "")
		if status != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (body: %s)", status, body)
		}
	})
}

func TestAuditExport_ScopeGatesBeforeDB(t *testing.T) {
	app := newAuditScopeTestApp(t)
	clusterID := uuid.New().String()

	t.Run("formats", func(t *testing.T) {
		// A scope-less export is an empty export — but still a well-formed one
		// in the requested format, so a client parsing it sees zero rows rather
		// than a broken file.
		tests := []struct {
			name  string
			query string
			check func(t *testing.T, body string)
		}{
			{
				name:  "json defaults to an empty array",
				query: "",
				check: func(t *testing.T, body string) {
					if strings.TrimSpace(body) != "[]" {
						t.Fatalf("want [], got %q", body)
					}
				},
			},
			{
				name:  "csv is the header row alone",
				query: "?format=csv",
				check: func(t *testing.T, body string) {
					lines := strings.Split(strings.TrimSpace(body), "\n")
					if len(lines) != 1 || !strings.HasPrefix(lines[0], "Timestamp,") {
						t.Fatalf("want a lone header row, got %q", body)
					}
				},
			},
			{
				name:  "syslog is empty",
				query: "?format=syslog",
				check: func(t *testing.T, body string) {
					if body != "" {
						t.Fatalf("want no records, got %q", body)
					}
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				status, body := auditScopeRequest(t, app, "/audit-log/export"+tt.query, "viewer")
				if status != http.StatusOK {
					t.Fatalf("status = %d, want 200 (body: %s)", status, body)
				}
				tt.check(t, body)
			})
		}
	})

	t.Run("rejections", func(t *testing.T) {
		tests := []struct {
			name   string
			role   string
			query  string
			status int
		}{
			{
				name:   "unknown format",
				role:   "viewer",
				query:  "?format=xml",
				status: http.StatusBadRequest,
			},
			{
				name:   "cluster filter outside the access set is rejected",
				role:   "viewer",
				query:  "?cluster_id=" + clusterID,
				status: http.StatusForbidden,
			},
			{
				name:   "unauthenticated fails loud",
				status: http.StatusInternalServerError,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				status, body := auditScopeRequest(t, app, "/audit-log/export"+tt.query, tt.role)
				if status != tt.status {
					t.Fatalf("status = %d, want %d (body: %s)", status, tt.status, body)
				}
			})
		}
	})
}
