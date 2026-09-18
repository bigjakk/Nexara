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

// dlqRouteCount is how many endpoints registerNotificationDLQEndpoints
// declares.
const dlqRouteCount = 5

// dlqRoutesOutsideTheClusterCheckShape records why all five are GLOBAL rather
// than cluster-scoped Checks. It is folded into
// routesOutsideTheClusterCheckShape in registry_vms_test.go.
//
// This is the one domain where "global" is the finding rather than the default:
// the rows carry a denormalised cluster_id and the handler already applies a
// per-row guard, so it LOOKS cluster-scopable — but ListNotificationDLQ applies
// LIMIT/OFFSET across every cluster's rows and trims afterwards, so opening it
// to a scoped caller would page through the global rowset and hand back short
// pages with holes. Widening the gate is a query change, not a declaration
// change.
var dlqRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/notification-dlq":              "global: a cluster-scoped grant satisfies no global check, and the listing pages across every cluster's rows",
	"GET /api/v1/notification-dlq/summary":      "global: the counts are instance-wide",
	"POST /api/v1/notification-dlq/:id/retry":   "global: the replay also needs manage:notification_channel, and the row's cluster is only known after it is read",
	"POST /api/v1/notification-dlq/:id/dismiss": "global: the row's cluster is only known after it is read",
	"DELETE /api/v1/notification-dlq/:id":       "global: the row's cluster is only known after it is read",
}

// dlqLegacyPermissions is the permission each handler checked with a
// hand-placed requirePerm call BEFORE Phase 6j, transcribed from
// `git show HEAD:internal/api/handlers/notification_dlq.go` at commit eaeafa7.
//
// SIX global calls across five handlers, not five: Retry made two. The extra
// one is manage:notification_channel, which Permissions has no shape for and
// which therefore stays in the handler — see dlqExtraPermissions below.
var dlqLegacyPermissions = map[string]string{
	"GET /api/v1/notification-dlq":              "view:notification_dlq",
	"GET /api/v1/notification-dlq/summary":      "view:notification_dlq",
	"POST /api/v1/notification-dlq/:id/retry":   "manage:notification_dlq",
	"POST /api/v1/notification-dlq/:id/dismiss": "manage:notification_dlq",
	"DELETE /api/v1/notification-dlq/:id":       "manage:notification_dlq",
}

// dlqExtraPermissions is the other half of that tally: the checks these
// handlers STILL make, which the declaration deliberately does not claim.
var dlqExtraPermissions = map[string][]string{
	"POST /api/v1/notification-dlq/:id/retry":   {"manage:notification_channel"},
	"POST /api/v1/notification-dlq/:id/dismiss": nil,
	"DELETE /api/v1/notification-dlq/:id":       nil,
}

func declaredDLQEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, dlqScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestNotificationDLQRoutesDeclareTheSamePermissionTheyEnforced is the tally
// that makes deleting six requirePerm calls a refactor rather than a change.
func TestNotificationDLQRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredDLQEndpoints(t)
	if len(declared) != dlqRouteCount {
		t.Fatalf("the registry declares %d DLQ routes, want %d", len(declared), dlqRouteCount)
	}
	if len(dlqLegacyPermissions) != dlqRouteCount {
		t.Fatalf("dlqLegacyPermissions has %d entries, want %d", len(dlqLegacyPermissions), dlqRouteCount)
	}

	var view, manage int
	for key, want := range dlqLegacyPermissions {
		switch want {
		case "view:notification_dlq":
			view++
		case "manage:notification_dlq":
			manage++
		}
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check", key, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
		if e.Permissions.Check.Scope != ScopeGlobal {
			t.Errorf("%s is %s-scoped; requirePerm demanded the grant instance-wide, and a cluster-scoped "+
				"grant satisfies no global check", key, e.Permissions.Check.Scope)
		}
	}
	if view != 2 || manage != 3 {
		t.Errorf("the tally splits %d view:notification_dlq / %d manage:notification_dlq, want 2 / 3", view, manage)
	}

	// The permissions a route checks but does NOT declare must be named in its
	// description, or an operator building a role reads a gate that is not the
	// whole gate.
	for key, extra := range dlqExtraPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("dlqExtraPermissions lists %s but no such route is declared", key)
			continue
		}
		for _, perm := range extra {
			if !strings.Contains(e.Description, perm) {
				t.Errorf("%s also requires %s, but its description never says so: %q", key, perm, e.Description)
			}
		}
	}
}

// TestNotificationDLQStateVocabulary pins the declared Enum against the
// handler's own validDLQStates map. The direction that bites is a schema
// accepting a state the column has no rows for: the listing comes back empty
// and reads as "nothing has failed" rather than as a typo.
func TestNotificationDLQStateVocabulary(t *testing.T) {
	got := slices.Clone(declaredEndpoint(t, fiber.MethodGet, dlqScope).Parameters["state"].Enum)
	slices.Sort(got)
	if want := handlers.DLQStateKeys(); !slices.Equal(got, want) {
		t.Errorf("the declared ?state= enum is %v but handlers.validDLQStates carries %v", got, want)
	}
}

// TestNotificationDLQListBoundsArePinned covers the pagination the handler used
// to clamp silently.
//
// The old body read ?limit= with strconv.Atoi and IGNORED the error, then
// replaced anything outside 1..100 with 50 — so ?limit=banana and ?limit=5000
// both quietly returned 50 rows, and a caller paging through the queue had no
// way to tell their parameter had been discarded. The declaration answers 400
// instead, naming the field.
func TestNotificationDLQListBoundsArePinned(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, dlqScope)
	if got := e.Parameters["limit"].Default; got != 50 {
		t.Errorf("limit default = %#v, want 50 — the value the handler substituted", got)
	}

	for _, tt := range []struct {
		query string
		want  int
		limit int64
	}{
		{query: "", want: fiber.StatusNoContent, limit: 50},
		{query: "?limit=10", want: fiber.StatusNoContent, limit: 10},
		{query: "?limit=100", want: fiber.StatusNoContent, limit: 100},
		{query: "?limit=0", want: fiber.StatusBadRequest},
		{query: "?limit=101", want: fiber.StatusBadRequest},
		{query: "?limit=banana", want: fiber.StatusBadRequest},
		{query: "?offset=-1", want: fiber.StatusBadRequest},
		{query: "?state=nonsense", want: fiber.StatusBadRequest},
		{query: "?channel_id=not-a-uuid", want: fiber.StatusBadRequest},
		{query: "?page=2", want: fiber.StatusBadRequest},
	} {
		cap := &capture{}
		probe := e
		probe.Handler = cap.handler()
		probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), probe)

		status, env := send(t, app, httptest.NewRequest(http.MethodGet, dlqScope+tt.query, nil))
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

// TestEveryNotificationDLQEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing.
func TestEveryNotificationDLQEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredDLQEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Notification Channels" {
			t.Errorf("%s is in group %q, want Notification Channels", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
