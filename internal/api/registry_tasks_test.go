package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// taskRouteCount is how many endpoints registerTaskEndpoints declares.
const taskRouteCount = 4

// taskRoutesOutsideTheClusterCheckShape records why three of the four are not
// plain cluster-scoped Checks. It is folded into
// routesOutsideTheClusterCheckShape in registry_vms_test.go.
var taskRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/tasks": "Advisory: the listing spans every cluster, so there is none for a gate to " +
		"resolve; accessibleClusters builds the SQL scope for the page AND the count",
	"POST /api/v1/tasks": "Deferred: the cluster is in the body, because the path names none",
	"DELETE /api/v1/tasks": "global: the underlying DELETE is unscoped, so a caller holding manage:task " +
		"on one cluster must not be able to wipe another's history",
	"PUT /api/v1/tasks/:upid": "Deferred: the cluster is a property of the task ROW the :upid resolves to",
}

// taskLegacyPermissions is what each handler checked BEFORE Phase 6j,
// transcribed from `git show HEAD:internal/api/handlers/tasks.go` at commit
// eaeafa7 — four handlers, four checks, and THREE different shapes:
//
//	List            accessibleClusters("view","task"), no gate.
//	Create          requireClusterPerm(manage, task, <body cluster_id>).
//	Update          requireClusterPerm(manage, task, <row's cluster_id>).
//	ClearCompleted  requirePerm(manage, task) — global.
var taskLegacyPermissions = map[string]string{
	"GET /api/v1/tasks":       "view:task (filtered)",
	"POST /api/v1/tasks":      "deferred",
	"PUT /api/v1/tasks/:upid": "deferred",
	"DELETE /api/v1/tasks":    "manage:task",
}

func declaredTaskEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, taskHistoryScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestTaskRoutesDeclareWhatTheyEnforced is the tally that makes this domain's
// four checks a refactor rather than a change.
func TestTaskRoutesDeclareWhatTheyEnforced(t *testing.T) {
	declared := declaredTaskEndpoints(t)
	if len(declared) != taskRouteCount {
		t.Fatalf("the registry declares %d task routes, want %d", len(declared), taskRouteCount)
	}
	if len(taskLegacyPermissions) != taskRouteCount {
		t.Fatalf("taskLegacyPermissions has %d entries, want %d", len(taskLegacyPermissions), taskRouteCount)
	}

	for key, want := range taskLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s is in the tally but is not declared in the registry", key)
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
	}

	// The two Deferred reasons must name the call the handler still makes, or
	// they document nothing an operator could go and verify.
	for _, key := range []string{"POST /api/v1/tasks", "PUT /api/v1/tasks/:upid"} {
		reason := declared[key].Permissions.Deferred
		if !strings.Contains(reason, "requireClusterPerm") || !strings.Contains(reason, "manage") {
			t.Errorf("%s: the Deferred reason does not name the check the handler makes: %q", key, reason)
		}
	}

	// The clear is GLOBAL, and that is the finding rather than the default: a
	// cluster-scoped manage:task must not open it.
	clear := declared["DELETE /api/v1/tasks"]
	if clear.Permissions.Check == nil || clear.Permissions.Check.Scope != ScopeGlobal {
		t.Errorf("the bulk clear declares %q; it must be a GLOBAL Check, because the DELETE it runs is unscoped",
			clear.Permissions.Describe())
	}
}

// TestTaskSortVocabulary and TestTaskStatusVocabulary pin the declared Enums
// against the handler's own whitelists.
//
// The sort one is the load-bearing half: queries/tasks.sql matches sort_by on
// the STRING, so a key the schema accepts and the SQL does not know falls
// through to the default order and returns 200 with silently unsorted rows —
// which is exactly what taskSortColumns was introduced to stop.
func TestTaskSortVocabulary(t *testing.T) {
	got := slices.Clone(declaredEndpoint(t, fiber.MethodGet, taskHistoryScope).Parameters["sort"].Enum)
	slices.Sort(got)
	if want := handlers.TaskSortKeys(); !slices.Equal(got, want) {
		t.Errorf("the declared ?sort= enum is %v but handlers.taskSortColumns carries %v — a key in one "+
			"and not the other is a page that comes back unsorted with no error", got, want)
	}
	if def := declaredEndpoint(t, fiber.MethodGet, taskHistoryScope).Parameters["sort"].Default; def != "started" {
		t.Errorf("?sort= default = %#v, want \"started\"", def)
	}
	if def := declaredEndpoint(t, fiber.MethodGet, taskHistoryScope).Parameters["order"].Default; def != "desc" {
		t.Errorf("?order= default = %#v, want \"desc\"", def)
	}
}

func TestTaskStatusVocabulary(t *testing.T) {
	want := handlers.TaskStatusKeys()
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodGet, taskHistoryScope},
		{fiber.MethodPost, taskHistoryScope},
		{fiber.MethodPut, taskHistoryScope + "/:upid"},
	} {
		got := slices.Clone(declaredEndpoint(t, tt.method, tt.path).Parameters["status"].Enum)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s %s declares the status enum %v but the handler accepts %v", tt.method, tt.path, got, want)
		}
	}
}

// TestTaskClusterIsNotNamedClusterID is the escalation guard both the listing
// and the create had to route around, driven on BOTH spellings because the SPA
// sends the alias.
func TestTaskClusterIsNotNamedClusterID(t *testing.T) {
	for _, tt := range []struct {
		method string
		name   string
	}{
		{fiber.MethodGet, "filter_cluster_id"},
		{fiber.MethodPost, "task_cluster_id"},
	} {
		e := declaredEndpoint(t, tt.method, taskHistoryScope)
		if _, declared := e.Parameters["cluster_id"]; declared {
			t.Fatalf("%s declares a parameter NAMED cluster_id; the permission middleware reads that name, "+
				"so Register would have refused it", tt.method)
		}
		if got := e.Parameters[tt.name].Alias; got != "cluster_id" {
			t.Errorf("%s: %s declares alias %q, want cluster_id", tt.method, tt.name, got)
		}
	}

	// End to end on both spellings, through the create body.
	for _, spelling := range []string{"cluster_id", "task_cluster_id"} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodPost, taskHistoryScope)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		body := `{"` + spelling + `":"` + testClusterID + `","upid":"UPID:pve-01:0000A:qmstart::root@pam:"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, taskHistoryScope, body))
		if status != fiber.StatusNoContent {
			t.Fatalf("spelling %q: status = %d (%q), want 204", spelling, status, env.Message)
		}
		if got := cap.params.String("task_cluster_id"); got != testClusterID {
			t.Errorf("spelling %q reached the handler as task_cluster_id=%q", spelling, got)
		}
	}
}

// TestTaskListBoundsArePinned covers the filters the handler used to clamp or
// reject by hand, including the one it clamped SILENTLY: fiber.Query[int]
// returns 0 for a value it cannot parse, which the handler then raised to 1 —
// so ?limit=banana quietly returned one row.
func TestTaskListBoundsArePinned(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, taskHistoryScope)

	for _, tt := range []struct {
		query string
		want  int
		limit int64
	}{
		{query: "", want: fiber.StatusNoContent, limit: 50},
		{query: "?limit=200", want: fiber.StatusNoContent, limit: 200},
		{query: "?limit=0", want: fiber.StatusBadRequest},
		{query: "?limit=201", want: fiber.StatusBadRequest},
		{query: "?limit=banana", want: fiber.StatusBadRequest},
		{query: "?offset=-1", want: fiber.StatusBadRequest},
		{query: "?sort=upid", want: fiber.StatusBadRequest},
		{query: "?sort=started;DROP+TABLE+task_history", want: fiber.StatusBadRequest},
		{query: "?order=ASC", want: fiber.StatusBadRequest},
		{query: "?status=bogus", want: fiber.StatusBadRequest},
		{query: "?cluster_id=not-a-uuid", want: fiber.StatusBadRequest},
		{query: "?vmids=100,101", want: fiber.StatusNoContent, limit: 50},
		{query: "?page=2", want: fiber.StatusBadRequest},
	} {
		cap := &capture{}
		probe := e
		probe.Handler = cap.handler()
		probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), probe)

		status, env := send(t, app, httptest.NewRequest(http.MethodGet, taskHistoryScope+tt.query, nil))
		if status != tt.want {
			t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
			continue
		}
		if tt.want != fiber.StatusNoContent {
			continue
		}
		if got := cap.params.Int("limit"); got != tt.limit {
			t.Errorf("%q: limit reached the handler as %d, want %d", tt.query, got, tt.limit)
		}
	}
}

// TestTaskUpdateKeepsNullMeaningAbsent is the compatibility assertion this
// domain needed.
//
// The SPA sends progress and finished_at UNCONDITIONALLY, as an explicit null
// when it has nothing to say. apischema reads an explicit null as absent, which
// is exactly how the handler already read its *float64 and *string — so the two
// agree, and the "stopped with no finished_at stamps now" branch still fires.
func TestTaskUpdateKeepsNullMeaningAbsent(t *testing.T) {
	const path = taskHistoryScope + "/:upid"
	cap := &capture{}
	e := declaredEndpoint(t, fiber.MethodPut, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), e)

	target := strings.ReplaceAll(path, ":upid", "UPID%3Apve-01%3A0000A%3Aqmstart%3A%3Aroot%40pam%3A")
	body := `{"status":"stopped","exit_status":"","progress":null,"finished_at":null}`
	if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — this is the body the SPA sends", status, env.Message)
	}
	if _, supplied := cap.params.OptFloat("progress"); supplied {
		t.Error("an explicit null progress reads as supplied; the handler would write 0 over the stored value")
	}
	if _, supplied := cap.params.OptString("finished_at"); supplied {
		t.Error("an explicit null finished_at reads as supplied; the \"stopped stamps now\" branch would never fire")
	}
	// The percent-encoded UPID survives the path parameter intact — the handler
	// unescapes it, and a pattern written against either form would break one
	// of the two clients.
	if got := cap.params.String("upid"); !strings.HasPrefix(got, "UPID%3A") {
		t.Errorf("upid reached the handler as %q; Fiber does not decode path parameters and the handler unescapes it itself", got)
	}
}

// TestEveryTaskEndpointIsDocumented holds the declarations to the standard that
// makes this whole effort worth doing.
func TestEveryTaskEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredTaskEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Tasks" {
			t.Errorf("%s is in group %q, want Tasks", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}

// TestTaskCreateNodeIsHeldToTheNodeNameRule pins the one node parameter in the
// registry that used to carry no rule at all.
//
// `node` was a bare optString(63) here while every other node parameter in the
// registry carried node-name or its sentinel twin, and this is the worst place
// for that exception: the value does not stay in the row it is filed on.
// reconcileRunningTasks (internal/collector/task_reconcile.go) reads it back
// off every row still marked running and replays it through GetTaskStatus on
// each sync tick, with the server's own credentials and nobody watching, and
// the task listing hands it to any view:task holder.
//
// The traversal half is closed at the client — proxmox.validateNodeName
// refuses a value that could leave its path segment, and refuses it there
// rather than here so that the collector and the scheduler inherit it too.
// What this declaration stops is the ROW. Without it the write succeeds, the
// collector then calls GetTaskStatus once per tick and discards the error
// silently — task_reconcile.go's error branch has no log line — and at
// staleTaskGrace (24h) the row is flipped to failed/"vanished". So the cost is
// a day of futile calls and a bogus failure left in the activity feed, not an
// unbounded loop.
//
// The empty string has to stay acceptable, which is why this is the sentinel
// twin and not the format: task_history.node is NOT NULL DEFAULT ”
// (migrations/000008_task_history.up.sql) and apischema counts "" as a value
// the caller supplied, which every format rejects.
func TestTaskCreateNodeIsHeldToTheNodeNameRule(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, taskHistoryScope)

	for _, tt := range []struct {
		name string
		node string
		want int
		why  string
	}{
		{name: "a real node name", node: `"pve-01"`, want: fiber.StatusNoContent,
			why: "the ordinary case, and the control that stops this test passing by refusing everything"},
		// node-name is deliberately LOOSER than PVE's own rule: it admits a
		// dot (catalogue.go's node-name Divergence). This is the value
		// validateNodeName used to 500 on, so it is the case that catches
		// someone "tightening" this site to PVE's literal class.
		{name: "a dotted name", node: `"pve..01"`, want: fiber.StatusNoContent,
			why: "the catalogue's documented divergence; refusing it would 400 a name PVE accepts"},
		{name: "the empty sentinel", node: `""`, want: fiber.StatusNoContent,
			why: "the column's own default; refusing it would 400 a request that has always worked"},
		{name: "omitted", node: "", want: fiber.StatusNoContent,
			why: "optional, and absent is not the same as empty"},
		{name: "a bare dot", node: `"."`, want: fiber.StatusBadRequest,
			why: "a segment that disappears when the far side normalises the path"},
		{name: "a traversal", node: `".."`, want: fiber.StatusBadRequest,
			why: "the same, one level up"},
		{name: "a separator", node: `"pve-01/x"`, want: fiber.StatusBadRequest,
			why: "two segments where the path has room for one"},
		// NOT the %2e%2e-decodes-on-the-far-side finding: that one is about a
		// value interpolated RAW (forbiddenVolumeIDChars, client_storage.go).
		// Here the body is JSON, so nothing percent-decodes it, and on replay
		// GetTaskStatus writes url.PathEscape(node), which re-encodes the "%"
		// to "%25" — Proxmox would receive a literal percent in a name, not a
		// separator. What refuses it is simply the charset.
		{name: "a percent", node: `"pve-01%2Fx"`, want: fiber.StatusBadRequest,
			why: "a percent is outside node-name's charset; on this route it was never an escape"},
		// Nothing but the newline: "pve-01\nX-Evil: 1" would also be refused
		// for the space and the colon, so it could not show the newline is
		// what does it.
		{name: "a newline", node: `"pve-01\n"`, want: fiber.StatusBadRequest,
			why: "the value is echoed to any view:task holder by the task listing"},
		{name: "a leading dash", node: `"-pve-01"`, want: fiber.StatusBadRequest,
			why: "node-name requires an alphanumeric at both ends"},
		// The one case the PATTERN cannot refuse — node-name-or-empty carries
		// no length bound — so this is the only witness for the MaxLength,
		// which this site now spells by hand instead of inheriting from
		// optString.
		{name: "over the cap", node: `"` + strings.Repeat("n", 64) + `"`, want: fiber.StatusBadRequest,
			why: "MaxLength 63 refuses it, not the pattern"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"task_cluster_id":"` + testClusterID + `","upid":"UPID:pve-01:0:0:0:qmstart:100:root@pam:"`
			if tt.node != "" {
				body += `,"node":` + tt.node
			}
			body += `}`

			cap := &capture{}
			probe := e
			probe.Handler = cap.handler()
			probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), probe)

			status, env := send(t, app, jsonRequest(http.MethodPost, taskHistoryScope, body))
			if status != tt.want {
				t.Fatalf("node=%s: status = %d (%q), want %d — %s", tt.node, status, env.Message, tt.want, tt.why)
			}
		})
	}
}
