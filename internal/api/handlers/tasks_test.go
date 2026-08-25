package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func TestParseVmidsParam(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []int32
		wantErr bool
	}{
		{name: "single", raw: "105", want: []int32{105}},
		{name: "list", raw: "100,101,205", want: []int32{100, 101, 205}},
		{name: "spaces tolerated", raw: " 100 , 101 ", want: []int32{100, 101}},
		{name: "empty element", raw: "100,,101", wantErr: true},
		{name: "non-numeric", raw: "100,abc", wantErr: true},
		{name: "negative", raw: "-5", wantErr: true},
		{name: "overflow", raw: "99999999999", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVmidsParam(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseVmidsParam(%q) = %v, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVmidsParam(%q) unexpected error: %v", tt.raw, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseVmidsParam(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("parseVmidsParam(%q)[%d] = %d, want %d", tt.raw, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseVmidsParamCap(t *testing.T) {
	parts := make([]string, maxVmidsFilter+1)
	for i := range parts {
		parts[i] = strconv.Itoa(100 + i)
	}
	if _, err := parseVmidsParam(strings.Join(parts, ",")); err == nil {
		t.Fatalf("expected error for %d vmids (cap %d)", len(parts), maxVmidsFilter)
	}

	if _, err := parseVmidsParam(strings.Join(parts[:maxVmidsFilter], ",")); err != nil {
		t.Fatalf("unexpected error at the cap: %v", err)
	}
}

// TestApplyTaskListScope pins the load-bearing wiring: the caller's view:task
// scope must land on BOTH query params — the count one especially, since
// Total goes to the client unfiltered and no per-row guard can repair it.
// Dropping the countP assignment (the original leak) fails here.
//
// The no-grant cases are asserted rather than skipped: they used to return
// early, which meant nothing checked the params on the one path where the
// helper left them nil — nil being SQL NULL, i.e. every cluster.
func TestApplyTaskListScope(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()

	tests := []struct {
		name     string
		access   clusterAccess
		wantScan bool // false = caller should skip the DB entirely
		wantIDs  []uuid.UUID
		wantNil  bool // expect nil scope on the params (global = no filter)
	}{
		{
			name:     "global access queries unrestricted",
			access:   clusterAccess{HasGlobal: true},
			wantScan: true,
			wantNil:  true,
		},
		{
			name:     "scoped access stamps the grant set on list and count",
			access:   clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: true, clusterB: true}},
			wantScan: true,
			wantIDs:  []uuid.UUID{clusterA, clusterB},
		},
		{
			// '{}' matches nothing, so a caller that ignores the false return
			// reads an empty set rather than every cluster's tasks.
			name:     "no grants stamps an empty scope and reports skippable",
			access:   clusterAccess{Allowed: map[uuid.UUID]bool{}},
			wantScan: false,
			wantIDs:  []uuid.UUID{},
		},
		{
			name:     "zero-value access stamps an empty scope and reports skippable",
			access:   clusterAccess{},
			wantScan: false,
			wantIDs:  []uuid.UUID{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var listP db.ListTaskHistoryFilteredParams
			var countP db.CountTaskHistoryFilteredParams
			got := applyTaskListScope(tt.access, &listP, &countP)
			if got != tt.wantScan {
				t.Fatalf("applyTaskListScope() = %v, want %v", got, tt.wantScan)
			}
			if tt.wantNil {
				if listP.AccessibleClusterIds != nil || countP.AccessibleClusterIds != nil {
					t.Fatalf("global access must leave scope nil, got list=%v count=%v",
						listP.AccessibleClusterIds, countP.AccessibleClusterIds)
				}
				return
			}
			for label, ids := range map[string][]uuid.UUID{
				"list":  listP.AccessibleClusterIds,
				"count": countP.AccessibleClusterIds,
			} {
				if ids == nil {
					t.Fatalf("%s params: scope must be non-nil for scoped access "+
						"(nil reads as SQL NULL = every cluster)", label)
				}
				if len(ids) != len(tt.wantIDs) {
					t.Fatalf("%s params: scope = %v, want %d IDs", label, ids, len(tt.wantIDs))
				}
				gotSet := make(map[uuid.UUID]bool, len(ids))
				for _, id := range ids {
					gotSet[id] = true
				}
				for _, id := range tt.wantIDs {
					if !gotSet[id] {
						t.Fatalf("%s params: scope = %v, missing %v", label, ids, id)
					}
				}
			}
		})
	}
}

// newTaskListTestApp wires TaskHandler.List with NIL queries: every case in
// the scope-gate test below must resolve before any DB access. A user without
// view:task grants must short-circuit ahead of the DB (pre-fix, that path
// reached CountTaskHistoryFiltered and leaked a cross-cluster task count into
// Total — here it panics on the nil Queries and fails loudly). The SQL scope
// filter is the actual access control; see TestApplyTaskListScope for the
// wiring that pins it.
func newTaskListTestApp(t *testing.T) *fiber.App {
	t.Helper()
	handler := NewTaskHandler(nil, nil, 0)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	installTestRoleMiddleware(app)
	app.Get("/tasks", handler.List)
	return app
}

func TestTaskList_ScopeGatesBeforeDB(t *testing.T) {
	app := newTaskListTestApp(t)
	clusterID := uuid.New().String()

	tests := []struct {
		name   string
		role   string // empty = unauthenticated (no user_id local)
		query  string
		status int
	}{
		{
			name:   "no view:task grant returns empty page without touching the DB",
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
			name:   "invalid status filter",
			role:   "viewer",
			query:  "?status=bogus",
			status: http.StatusBadRequest,
		},
		{
			name:   "unauthenticated fails loud",
			status: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/tasks"+tt.query, nil)
			if tt.role != "" {
				req.Header.Set("X-Test-Role", tt.role)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d (body: %s)", resp.StatusCode, tt.status, body)
			}
			if tt.status != http.StatusOK {
				return
			}
			var got ListResponse[taskResponse]
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode body %q: %v", body, err)
			}
			if got.Total != 0 || len(got.Items) != 0 {
				t.Fatalf("scope-less user must get total=0 and no items, got %s", body)
			}
			// Pin the wire shape: items must serialize as [], never null.
			if !strings.Contains(string(body), `"items":[]`) {
				t.Fatalf("items must be an empty array, got %s", body)
			}
		})
	}
}
