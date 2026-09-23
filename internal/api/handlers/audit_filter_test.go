package handlers

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// clusterGrantEngine grants one action:resource on ONE cluster and nothing
// globally. It is the caller an empty filter has to be tested against: read
// as a uuid to parse, "" fails (a 500); read as a cluster to authorize, it is
// refused (a 403). A global caller passes any permission check and so says
// nothing about whether one ran.
type clusterGrantEngine struct {
	action, resource string
	cluster          uuid.UUID
}

func (e clusterGrantEngine) HasPermission(_ context.Context, _ uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
	return action == e.action && resource == e.resource && scopeType == "cluster" && scopeID == e.cluster, nil
}

func (clusterGrantEngine) HasGlobalPermission(context.Context, uuid.UUID, string, string) (bool, error) {
	return false, nil
}

func (e clusterGrantEngine) LoadUserPermissions(context.Context, uuid.UUID) (*auth.UserPermissions, error) {
	return &auth.UserPermissions{Permissions: []auth.ScopedPermission{{
		Action: e.action, Resource: e.resource, ScopeType: "cluster", ScopeID: e.cluster.String(),
	}}}, nil
}

// replayRow replays one row struct through pgx.Row in field order — a sqlc
// row, or a one-field struct standing for a scalar such as a count.
type replayRow struct{ row any }

func (r replayRow) Scan(dest ...any) error {
	rows := &structRows{rows: []any{r.row}}
	rows.Next()
	return rows.Scan(dest...)
}

// listFilter is one filter a listing declares: the query key a caller sends
// it under, the name Params answers to, a value that applies it, and set,
// which writes that value into the column the handler must put it in — on
// the generated parameter structs of the listing query (L) and its count (C).
type listFilter[L, C any] struct {
	wire, name, value string
	set               func(*L, *C)
}

// listExpect is what one listing's queries are expected to receive: base
// builds the parameter structs of a request that sends no filter, and args
// runs the GENERATED query functions over a pair of them on a recording
// DBTX, which yields the argument lists a correct handler hands the
// database. Comparing against those, rather than decoding what the handler
// sent, is what holds each value to its own column: a filter written into a
// neighbour of the same type shows up as a difference.
type listExpect[L, C any] struct {
	base func() (L, C)
	args func(L, C) (list, count []any)
}

// want returns the argument lists for a request that sets f, or none.
func (e listExpect[L, C]) want(f *listFilter[L, C]) (list, count []any) {
	l, c := e.base()
	if f != nil {
		f.set(&l, &c)
	}
	return e.args(l, c)
}

// filterNames lists the parameter names filters answer to.
func filterNames[L, C any](filters []listFilter[L, C]) []string {
	names := make([]string, 0, len(filters))
	for _, f := range filters {
		names = append(names, f.name)
	}
	return names
}

// assertFiltersCoverSchema fails unless names, together with the parameters
// that are not filters — paging, sorting, the export format, the path —
// are every key of the schema the route is served with. It is what lets a
// test say it drives EVERY filter: a filter added to the schema and to no
// list is a failure here, not a filter nothing sends empty.
func assertFiltersCoverSchema(t *testing.T, schema apischema.Properties, names []string, others ...string) {
	t.Helper()
	covered := slices.Sorted(slices.Values(append(slices.Clone(names), others...)))
	declared := slices.Sorted(maps.Keys(schema))
	if !slices.Equal(covered, declared) {
		t.Fatalf("the filters under test and the other parameters are %v, but the schema declares %v — "+
			"a filter the schema declares and no list names is one nothing sends empty", covered, declared)
	}
}

// listReply is one request through a real listing handler: what it answered,
// the parameters it was handed (nil when validation refused the request
// first), and the arguments its listing and count queries received.
type listReply struct {
	status      int
	body        string
	params      *apischema.Params
	list, count []any
}

// arrivedEmpty fails the test unless every one of names reached the handler as
// a SUPPLIED empty value. Without it the "empty" request could quietly be the
// omitted one — if the harness or Fiber ever dropped a key with no value — and
// a comparison against the omitted request would then pass by comparing it
// with itself.
func arrivedEmpty(t *testing.T, r listReply, names ...string) {
	t.Helper()
	if r.params == nil {
		t.Fatalf("precondition: the handler never ran (%d %s)", r.status, r.body)
	}
	for _, name := range names {
		if !r.params.Has(name) || r.params.String(name) != "" {
			t.Fatalf("precondition: %s reached the handler as (%q, supplied=%v), want (\"\", true)",
				name, r.params.String(name), r.params.Has(name))
		}
	}
}

// assertEmptyFiltersAreNone drives one listing through serve with each of
// filters sent EMPTY — on its own, then all at once — and holds every such
// request to the omitted request's queries, argument for argument. The
// omitted request is itself held to expect's base, so "the same as omitted"
// means no filter, the caller's own scope, and nothing else, on the page and
// on the count alike. Each filter is also sent with a real value, whose
// queries must be expect's with that one filter set: in its own column, on
// the count as well as the page. That control is what shows the filter is
// read at all, so an ignored filter cannot pass for an empty one.
func assertEmptyFiltersAreNone[L, C any](t *testing.T, serve func(*testing.T, string) listReply,
	filters []listFilter[L, C], expect listExpect[L, C]) {
	t.Helper()
	omitted := serve(t, "")
	if omitted.status != http.StatusOK || omitted.list == nil || omitted.count == nil {
		t.Fatalf("precondition: the omitted request answered %d (%s) and queried list=%v count=%v; "+
			"it must reach both queries for the comparisons below to mean anything",
			omitted.status, omitted.body, omitted.list != nil, omitted.count != nil)
	}
	// DeepEqual throughout: a nil scope is every cluster and an empty one
	// is none, and the two must not read as the same.
	if wantList, wantCount := expect.want(nil); !reflect.DeepEqual(omitted.list, wantList) ||
		!reflect.DeepEqual(omitted.count, wantCount) {
		t.Fatalf("precondition: the omitted request reached the queries as\n  list  %v\n  count %v\n"+
			"want no filter under the caller's own scope:\n  list  %v\n  count %v",
			omitted.list, omitted.count, wantList, wantCount)
	}

	sameAsOmitted := func(t *testing.T, query string, names ...string) {
		t.Helper()
		empty := serve(t, query)
		arrivedEmpty(t, empty, names...)
		if empty.status != http.StatusOK {
			t.Fatalf("%s: status = %d (%s), want 200 — an empty filter is no filter", query, empty.status, empty.body)
		}
		if !reflect.DeepEqual(empty.list, omitted.list) || !reflect.DeepEqual(empty.count, omitted.count) {
			t.Errorf("%s reached the queries as\n  list  %v\n  count %v\nwant the omitted request's\n"+
				"  list  %v\n  count %v", query, empty.list, empty.count, omitted.list, omitted.count)
		}
	}

	allQuery := make([]string, 0, len(filters))
	for i := range filters {
		f := &filters[i]
		allQuery = append(allQuery, f.wire+"=")
		t.Run(f.wire+" empty", func(t *testing.T) {
			sameAsOmitted(t, "?"+f.wire+"=", f.name)
		})
		t.Run(f.wire+" set", func(t *testing.T) {
			query := "?" + f.wire + "=" + url.QueryEscape(f.value)
			sent := serve(t, query)
			if sent.status != http.StatusOK {
				t.Fatalf("%s: status = %d (%s), want 200", query, sent.status, sent.body)
			}
			wantList, wantCount := expect.want(f)
			if !reflect.DeepEqual(sent.list, wantList) || !reflect.DeepEqual(sent.count, wantCount) {
				t.Errorf("%s reached the queries as\n  list  %v\n  count %v\nwant the value in its own "+
					"column, on both:\n  list  %v\n  count %v", query, sent.list, sent.count, wantList, wantCount)
			}
		})
	}
	t.Run("all empty", func(t *testing.T) {
		sameAsOmitted(t, "?"+strings.Join(allQuery, "&"), filterNames(filters)...)
	})
}

// auditFilterDBTX answers the audit listing with no rows and a zero count, and
// records the arguments each of the two queries was handed.
type auditFilterDBTX struct {
	list, count []any
}

func (*auditFilterDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errCaptured
}

func (d *auditFilterDBTX) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	if !strings.Contains(sql, "-- name: ListAuditLogAdvanced :many") {
		return nil, errCaptured
	}
	d.list = args
	return &structRows{}, nil
}

func (d *auditFilterDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if !strings.Contains(sql, "-- name: CountAuditLogAdvanced :one") {
		return failRow{err: errCaptured}
	}
	d.count = args
	return replayRow{row: struct{ N int64 }{}}
}

// auditQueryArgs is the argument lists the generated audit queries send for a
// pair of parameter structs.
func auditQueryArgs(l db.ListAuditLogAdvancedParams, c db.CountAuditLogAdvancedParams) (list, count []any) {
	rec := &auditFilterDBTX{}
	q := db.New(rec)
	_, _ = q.ListAuditLogAdvanced(context.Background(), l)
	_, _ = q.CountAuditLogAdvanced(context.Background(), c)
	return rec.list, rec.count
}

// uuidArg, textArg and timeArg build the non-NULL values a filter writes.
func uuidArg(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func textArg(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

func timeArg(t *testing.T, s string) pgtype.Timestamptz {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("fixture time %q: %v", s, err)
	}
	return pgtype.Timestamptz{Time: v, Valid: true}
}

// TestAuditReadsTreatAnEmptyFilterAsNone drives the three real audit reads
// with every filter each declares sent EMPTY — one at a time, then all
// together — and holds each request to the omitted one's queries, argument for
// argument: no filter, and the caller's own scope on the page and on the
// count, which is what `if x != ""` meant before the registry. The instance-
// wide listing and export take ?cluster_id= as well as the filters all three
// share (user_id, resource_type, action, source, start_time, end_time and
// vmids).
//
// The empty cluster filter is the one with a permission side, and the caller
// that exposes it is one holding view:audit on a single cluster: had "" been
// taken as supplied, auditClusterFilter would have parsed it (a 500) or asked
// whether that caller may see it (a 403). A non-empty value of each filter is
// the control that shows it is read at all — and the cluster is applied in
// either case of hex digit when the caller may see it, and refused before any
// query when they may not.
func TestAuditReadsTreatAnEmptyFilterAsNone(t *testing.T) {
	clusterA, clusterB, user := uuid.New(), uuid.New(), uuid.New()

	perCluster := auditListMirror(t)
	delete(perCluster, "filter_cluster_id")
	perCluster["cluster_id"] = apischema.Property{Type: apischema.String, Format: "uuid", Source: apischema.SourcePath}
	perCluster = compiledMirror(t, perCluster)

	type auditFilter = listFilter[db.ListAuditLogAdvancedParams, db.CountAuditLogAdvancedParams]
	type (
		auditList  = db.ListAuditLogAdvancedParams
		auditCount = db.CountAuditLogAdvancedParams
	)
	startTime, endTime := "2026-01-02T03:04:05Z", "2026-01-03T03:04:05Z"

	// Every filter the three reads share, and the cluster filter only the
	// two instance-wide ones declare — each with the column it belongs in.
	shared := []auditFilter{
		{"user_id", "user_id", user.String(), func(l *auditList, c *auditCount) {
			l.UserID, c.UserID = uuidArg(user), uuidArg(user)
		}},
		{"resource_type", "resource_type", "vm", func(l *auditList, c *auditCount) {
			l.ResourceType, c.ResourceType = textArg("vm"), textArg("vm")
		}},
		{"action", "action", "vm_created", func(l *auditList, c *auditCount) {
			l.Action, c.Action = textArg("vm_created"), textArg("vm_created")
		}},
		{"source", "source", "nexara", func(l *auditList, c *auditCount) {
			l.Source, c.Source = textArg("nexara"), textArg("nexara")
		}},
		{"start_time", "start_time", startTime, func(l *auditList, c *auditCount) {
			l.StartTime, c.StartTime = timeArg(t, startTime), timeArg(t, startTime)
		}},
		{"end_time", "end_time", endTime, func(l *auditList, c *auditCount) {
			l.EndTime, c.EndTime = timeArg(t, endTime), timeArg(t, endTime)
		}},
		{"vmids", "vmids", "100,101", func(l *auditList, c *auditCount) {
			l.Vmids, c.Vmids = []int32{100, 101}, []int32{100, 101}
		}},
	}
	byCluster := auditFilter{"cluster_id", "filter_cluster_id", clusterA.String(), func(l *auditList, c *auditCount) {
		l.ClusterID, c.ClusterID = uuidArg(clusterA), uuidArg(clusterA)
	}}
	wide := append([]auditFilter{byCluster}, shared...)

	routes := []struct {
		name       string
		pattern    string
		target     string
		mirror     apischema.Properties
		pathParams []string
		serve      func(h *AuditHandler) func(fiber.Ctx, *apischema.Params) error
		filters    []auditFilter
		// others are the route's parameters that are not filters.
		others []string
		// base is what the route queries with no filter sent, under a
		// caller's scope: the export reads up to its own cap, and the
		// per-cluster listing always carries its path's cluster.
		base func(scope []uuid.UUID) (auditList, auditCount)
		// wide routes take a cluster filter, and so have a permission side.
		wide bool
	}{
		{"listing", "/audit-log", "/audit-log", auditListMirror(t), nil,
			func(h *AuditHandler) func(fiber.Ctx, *apischema.Params) error { return h.List },
			wide, []string{"limit", "offset"},
			func(scope []uuid.UUID) (auditList, auditCount) {
				return auditList{Limit: 50, AccessibleClusterIds: scope}, auditCount{AccessibleClusterIds: scope}
			}, true},
		{"export", "/audit-log/export", "/audit-log/export", auditExportMirror(t), nil,
			func(h *AuditHandler) func(fiber.Ctx, *apischema.Params) error { return h.Export },
			wide, []string{"limit", "offset", "format"},
			func(scope []uuid.UUID) (auditList, auditCount) {
				return auditList{Limit: exportRowCap, AccessibleClusterIds: scope}, auditCount{AccessibleClusterIds: scope}
			}, true},
		{"per-cluster listing", "/clusters/:cluster_id/audit-log", "/clusters/" + clusterA.String() + "/audit-log",
			perCluster, []string{"cluster_id"},
			func(h *AuditHandler) func(fiber.Ctx, *apischema.Params) error { return h.ListByCluster },
			shared, []string{"limit", "offset", "cluster_id"},
			func(scope []uuid.UUID) (auditList, auditCount) {
				return auditList{Limit: 50, ClusterID: uuidArg(clusterA), AccessibleClusterIds: scope},
					auditCount{ClusterID: uuidArg(clusterA), AccessibleClusterIds: scope}
			}, false},
	}
	callers := []struct {
		name   string
		engine permissionEngine
		scope  []uuid.UUID
	}{
		{"view:audit on one cluster", clusterGrantEngine{"view", "audit", clusterA}, []uuid.UUID{clusterA}},
		{"global view:audit", newGrantEngine("view:audit"), nil},
	}

	for _, route := range routes {
		serve := func(t *testing.T, engine permissionEngine, query string) listReply {
			t.Helper()
			dbtx := &auditFilterDBTX{}
			handler := route.serve(NewAuditHandler(db.New(dbtx), nil))
			var seen *apischema.Params
			app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
			app.Use(func(c fiber.Ctx) error {
				c.Locals("user_id", uuid.New())
				c.Locals("rbac_engine", engine)
				return c.Next()
			})
			app.Get(route.pattern, withRequestParams(t, route.mirror, route.pathParams,
				func(c fiber.Ctx, p *apischema.Params) error {
					seen = p
					return handler(c, p)
				}))
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, route.target+query, nil))
			if err != nil {
				t.Fatalf("request %s: %v", query, err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			return listReply{status: resp.StatusCode, body: string(body), params: seen, list: dbtx.list, count: dbtx.count}
		}

		t.Run(route.name+"/every filter is under test", func(t *testing.T) {
			assertFiltersCoverSchema(t, route.mirror, filterNames(route.filters), route.others...)
		})

		for _, caller := range callers {
			t.Run(route.name+"/"+caller.name, func(t *testing.T) {
				expect := listExpect[auditList, auditCount]{
					base: func() (auditList, auditCount) { return route.base(caller.scope) },
					args: auditQueryArgs,
				}
				assertEmptyFiltersAreNone(t, func(t *testing.T, query string) listReply {
					t.Helper()
					return serve(t, caller.engine, query)
				}, route.filters, expect)

				if !route.wide {
					return
				}
				// The cluster filter applies in upper case too: the rule
				// admits upper-case hex, which the uuid format used to
				// lower-case, and the handler parses rather than compares.
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

		if route.wide {
			t.Run(route.name+"/a cluster the caller cannot see", func(t *testing.T) {
				refused := serve(t, clusterGrantEngine{"view", "audit", clusterA}, "?cluster_id="+clusterB.String())
				if refused.status != http.StatusForbidden {
					t.Errorf("status = %d (%s), want 403 — a filter the caller may not see is refused, not emptied",
						refused.status, refused.body)
				}
				if refused.list != nil || refused.count != nil {
					t.Error("a refused filter still reached the database")
				}
			})
		}
	}
}
