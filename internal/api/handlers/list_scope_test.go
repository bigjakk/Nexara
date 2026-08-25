package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// TestClusterScopeFilter pins the contract every scoped list endpoint depends
// on. The nil/non-nil split is the whole safety property: pgx sends a nil
// slice as SQL NULL, which the narg queries read as "no restriction", while a
// non-nil empty slice becomes '{}' and matches nothing. A helper that returned
// nil for a caller with no grants would lift the filter for exactly the caller
// who should see least.
func TestClusterScopeFilter(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()

	tests := []struct {
		name      string
		access    clusterAccess
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
			name:      "global wins over a populated Allowed map",
			access:    clusterAccess{HasGlobal: true, Allowed: map[uuid.UUID]bool{clusterA: true}},
			wantQuery: true,
			wantNil:   true,
		},
		{
			name:      "scoped access returns exactly the granted IDs",
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
			name:      "no grants yields '{}' and reports skippable",
			access:    clusterAccess{Allowed: map[uuid.UUID]bool{}},
			wantQuery: false,
			wantIDs:   []uuid.UUID{},
		},
		{
			// Only explicit-false entries: ScopedIDs drops them, so the filter
			// is '{}' and the flag must agree rather than promise a row.
			name:      "only revoked entries reports skippable",
			access:    clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: false}},
			wantQuery: false,
			wantIDs:   []uuid.UUID{},
		},
		{
			name:      "zero-value access yields '{}' and reports skippable",
			access:    clusterAccess{},
			wantQuery: false,
			wantIDs:   []uuid.UUID{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, query := clusterScopeFilter(tt.access)
			if query != tt.wantQuery {
				t.Fatalf("clusterScopeFilter() query = %v, want %v", query, tt.wantQuery)
			}
			assertScope(t, "clusterScopeFilter", ids, tt.wantNil, tt.wantIDs)
		})
	}
}

// newListScopeTestApp wires the RBAC-scoped list endpoints that used to fetch
// a page across every cluster and trim it per row, all with NIL queries. A
// caller holding no grant for the relevant resource must answer from the scope
// alone; reaching the DB panics on the nil Queries and fails loudly.
//
// The stub engine grants a non-admin role nothing (permission_stub_test.go), so
// "viewer" is a caller with no view:alert / view:migration / view:report.
func newListScopeTestApp(t *testing.T) *fiber.App {
	t.Helper()

	alerts := NewAlertHandler(nil, "", nil, nil)
	migrations := NewMigrationHandler(context.TODO(), nil, "", nil)
	reports := NewReportHandler(nil, "", nil, nil)

	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	installTestRoleMiddleware(app)
	app.Get("/alert-rules", alerts.ListRules)
	app.Get("/alerts", alerts.ListAlerts)
	app.Get("/migrations", migrations.List)
	app.Get("/report-schedules", reports.ListSchedules)
	app.Get("/report-runs", reports.ListRuns)
	return app
}

func TestListEndpoints_ScopeGatesBeforeDB(t *testing.T) {
	app := newListScopeTestApp(t)

	paths := []string{
		"/alert-rules",
		"/alerts",
		"/migrations",
		"/report-schedules",
		"/report-runs",
	}

	for _, path := range paths {
		t.Run("no grant returns an empty page without touching the DB: "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-Test-Role", "viewer")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request %s: %v", path, err)
			}
			defer func() { _ = resp.Body.Close() }()

			body := readBody(t, resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s status = %d, want 200 (body: %s)", path, resp.StatusCode, body)
			}
			// Pin the wire shape too: every collection returns the
			// ListResponse envelope, and `items` must serialize as [], never
			// null.
			if strings.TrimSpace(body) != `{"items":[],"total":0}` {
				t.Fatalf("%s must return an empty envelope for a scope-less caller, got %s", path, body)
			}
		})

		t.Run("unauthenticated fails loud: "+path, func(t *testing.T) {
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
			if err != nil {
				t.Fatalf("request %s: %v", path, err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("%s status = %d, want 500 — a request with no RBAC engine must "+
					"fail rather than degrade to an unscoped listing", path, resp.StatusCode)
			}
		})
	}
}
