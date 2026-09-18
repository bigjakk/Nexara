package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
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
	// The two alert listings are registry endpoints now, so they take their
	// validated parameters rather than reading the query themselves. The scope
	// gate this test is about runs before any of them is consulted, so an
	// all-defaults Params is the honest stand-in for the request.
	app.Get("/alert-rules", withParams(t, alertListMirror(t, nil), alerts.ListRules))
	app.Get("/alerts", withParams(t, alertListMirror(t, apischema.Properties{
		"state":    {Type: apischema.String, Optional: true},
		"severity": {Type: apischema.String, Optional: true},
	}), alerts.ListAlerts))
	// The migration listing is a registry endpoint now, so it takes its
	// validated parameters rather than reading the query itself. The scope
	// gate this test is about runs before either is consulted, so an
	// all-defaults Params is the honest stand-in for the request.
	app.Get("/migrations", withParams(t, migrationListMirror(t), migrations.List))
	// The two report listings are registry endpoints now. Neither declares a
	// parameter — the page size is a constant in the handler — so an empty
	// schema is the whole of their declaration rather than a mirror of it.
	app.Get("/report-schedules", withParams(t, apischema.Properties{}, reports.ListSchedules))
	app.Get("/report-runs", withParams(t, apischema.Properties{}, reports.ListRuns))
	return app
}

// migrationListMirror is a local copy of the limit/offset half of the
// migration listing's declaration (internal/api/registry_migrations.go).
//
// It is a mirror rather than the real thing because package api imports
// this package, not the other way round — the same reason
// TestDiskAttachRequestFrom keeps its own copy. Nothing here depends on
// the bounds matching: the handler under test reads both keys and never
// reaches them, so what this has to get right is only that both ARE
// declared with a default.
func migrationListMirror(t *testing.T) apischema.Properties {
	t.Helper()
	props := apischema.Properties{
		"limit":  {Type: apischema.Integer, Optional: true, Default: 50},
		"offset": {Type: apischema.Integer, Optional: true, Default: 0},
	}
	if err := props.Compile(); err != nil {
		t.Fatalf("the mirror schema is itself invalid: %v", err)
	}
	return props
}

// alertListMirror is a local copy of the parts of the two alert listings'
// declarations (internal/api/registry_alerts.go) that their handlers read.
//
// A mirror for the same reason migrationListMirror is one, and with the same
// caveat: nothing here depends on the bounds matching. What it has to get
// right is that every key the handler reads IS declared — a Params accessor
// panics on an undeclared one — and that limit/offset carry a default.
func alertListMirror(t *testing.T, extra apischema.Properties) apischema.Properties {
	t.Helper()
	props := apischema.Properties{
		"limit":             {Type: apischema.Integer, Optional: true, Default: 50},
		"offset":            {Type: apischema.Integer, Optional: true, Default: 0},
		"filter_cluster_id": {Type: apischema.String, Alias: "cluster_id", Optional: true},
	}
	for name, prop := range extra {
		props[name] = prop
	}
	if err := props.Compile(); err != nil {
		t.Fatalf("the mirror schema is itself invalid: %v", err)
	}
	return props
}

// withParams adapts a registry-shaped handler to a fiber.Handler by
// validating an EMPTY request against props, so every declared parameter
// arrives carrying its declared default.
func withParams(t *testing.T, props apischema.Properties, h func(fiber.Ctx, *apischema.Params) error) fiber.Handler {
	t.Helper()
	params, err := props.Validate(map[string]any{})
	if err != nil {
		t.Fatalf("validating an empty request against the mirror schema: %v", err)
	}
	return func(c fiber.Ctx) error { return h(c, params) }
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
