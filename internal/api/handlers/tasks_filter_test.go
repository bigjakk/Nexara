package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// taskFilterDBTX answers the task listing with no rows and a zero count, and
// records the arguments each of the two queries was handed.
type taskFilterDBTX struct {
	list, count []any
}

func (*taskFilterDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCaptured
}

func (d *taskFilterDBTX) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	if !strings.Contains(sql, "-- name: ListTaskHistoryFiltered :many") {
		return nil, errCaptured
	}
	d.list = args
	return &structRows{}, nil
}

func (d *taskFilterDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if !strings.Contains(sql, "-- name: CountTaskHistoryFiltered :one") {
		return failRow{err: errCaptured}
	}
	d.count = args
	return replayRow{row: struct{ N int64 }{}}
}

// taskQueryArgs is the argument lists the generated task queries send for a
// pair of parameter structs.
func taskQueryArgs(l db.ListTaskHistoryFilteredParams, c db.CountTaskHistoryFilteredParams) (list, count []any) {
	rec := &taskFilterDBTX{}
	q := db.New(rec)
	_, _ = q.ListTaskHistoryFiltered(context.Background(), l)
	_, _ = q.CountTaskHistoryFiltered(context.Background(), c)
	return rec.list, rec.count
}

// TestTaskListTreatsAnEmptyFilterAsNone drives the real List handler with each
// of its filters — ?cluster_id=, ?status= and ?vmids= — sent EMPTY, one at a
// time and then all together, and holds each request to the omitted one's
// queries, argument for argument: no filter, and the caller's own scope on the
// page and on the count. That is what `if x != ""` meant before the registry.
//
// The cluster filter is the one with a permission side, and the caller that
// exposes it is one holding view:task on a single cluster: had "" been taken
// as supplied, List would have parsed it (a 500) or asked whether that caller
// may see it (a 403). A non-empty value of each filter is the control that
// shows it is read at all — and the cluster is applied in either case of hex
// digit when the caller may see it, and refused before any query when they
// may not.
func TestTaskListTreatsAnEmptyFilterAsNone(t *testing.T) {
	clusterA, clusterB := uuid.New(), uuid.New()

	serve := func(t *testing.T, engine permissionEngine, query string) listReply {
		t.Helper()
		dbtx := &taskFilterDBTX{}
		handler := NewTaskHandler(db.New(dbtx), nil, 0)
		var seen *apischema.Params
		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Use(func(c fiber.Ctx) error {
			c.Locals("user_id", uuid.New())
			c.Locals("rbac_engine", engine)
			return c.Next()
		})
		app.Get("/tasks", withRequestParams(t, taskListMirror(t), nil, func(c fiber.Ctx, p *apischema.Params) error {
			seen = p
			return handler.List(c, p)
		}))
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/tasks"+query, nil))
		if err != nil {
			t.Fatalf("request %s: %v", query, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return listReply{status: resp.StatusCode, body: string(body), params: seen, list: dbtx.list, count: dbtx.count}
	}

	type (
		taskList  = db.ListTaskHistoryFilteredParams
		taskCount = db.CountTaskHistoryFilteredParams
	)
	byCluster := listFilter[taskList, taskCount]{"cluster_id", "filter_cluster_id", clusterA.String(),
		func(l *taskList, c *taskCount) { l.ClusterID, c.ClusterID = uuidArg(clusterA), uuidArg(clusterA) }}
	filters := []listFilter[taskList, taskCount]{
		byCluster,
		{"status", "status", "running", func(l *taskList, c *taskCount) {
			l.Status, c.Status = textArg("running"), textArg("running")
		}},
		{"vmids", "vmids", "100,101", func(l *taskList, c *taskCount) {
			l.Vmids, c.Vmids = []int32{100, 101}, []int32{100, 101}
		}},
	}
	assertFiltersCoverSchema(t, taskListMirror(t), filterNames(filters), "limit", "offset", "sort", "order")

	for _, caller := range []struct {
		name   string
		engine permissionEngine
		scope  []uuid.UUID
	}{
		{"view:task on one cluster", clusterGrantEngine{"view", "task", clusterA}, []uuid.UUID{clusterA}},
		{"global view:task", newGrantEngine("view:task"), nil},
	} {
		t.Run(caller.name, func(t *testing.T) {
			// The declared defaults, sorting included, under the caller's
			// own scope. Sorting never reaches the count.
			expect := listExpect[taskList, taskCount]{
				base: func() (taskList, taskCount) {
					return taskList{Limit: 50, SortBy: "started", SortDir: "desc", AccessibleClusterIds: caller.scope},
						taskCount{AccessibleClusterIds: caller.scope}
				},
				args: taskQueryArgs,
			}
			assertEmptyFiltersAreNone(t, func(t *testing.T, query string) listReply {
				t.Helper()
				return serve(t, caller.engine, query)
			}, filters, expect)

			// The cluster filter applies in upper case too: the rule admits
			// upper-case hex, which the uuid format used to lower-case, and
			// List parses rather than compares.
			query := "?cluster_id=" + strings.ToUpper(clusterA.String())
			upper := serve(t, caller.engine, query)
			if upper.status != http.StatusOK {
				t.Fatalf("%s: status = %d (%s), want 200", query, upper.status, upper.body)
			}
			wantList, wantCount := expect.want(&byCluster)
			if !reflect.DeepEqual(upper.list, wantList) || !reflect.DeepEqual(upper.count, wantCount) {
				t.Errorf("%s reached the queries as\n  list  %v\n  count %v\nwant\n  list  %v\n  count %v",
					query, upper.list, upper.count, wantList, wantCount)
			}
		})
	}

	t.Run("a cluster the caller cannot see", func(t *testing.T) {
		refused := serve(t, clusterGrantEngine{"view", "task", clusterA}, "?cluster_id="+clusterB.String())
		if refused.status != http.StatusForbidden {
			t.Errorf("status = %d (%s), want 403 — a filter the caller may not see is refused, not emptied",
				refused.status, refused.body)
		}
		if refused.list != nil || refused.count != nil {
			t.Error("a refused filter still reached the database")
		}
	})
}
