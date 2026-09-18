package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// auditRouteCount is how many endpoints registerAuditEndpoints declares.
const auditRouteCount = 9

// auditRoutesOutsideTheClusterCheckShape records why eight of the nine are not
// cluster-scoped Checks. It is folded into routesOutsideTheClusterCheckShape in
// registry_vms_test.go.
var auditRoutesOutsideTheClusterCheckShape = map[string]string{
	"GET /api/v1/audit-log":        "Advisory: the listing spans every cluster and the SQL scope is the authorization",
	"GET /api/v1/audit-log/recent": "Advisory: the feed spans every cluster and the SQL scope is the authorization",
	"GET /api/v1/audit-log/export": "Advisory: the export spans every cluster and the SQL scope is the authorization",
	"GET /api/v1/audit-log/actions": "global: the distinct-value lookups read across the whole table with no " +
		"per-cluster clause, so a cluster-scoped grant must not open them",
	"GET /api/v1/audit-log/users":         "global: as for /actions",
	"GET /api/v1/audit-log/syslog-config": "global: the forwarding config belongs to the install, not to a cluster",
	"PUT /api/v1/audit-log/syslog-config": "global: the forwarding config belongs to the install, not to a cluster",
	"POST /api/v1/audit-log/syslog-test":  "global: the probe dials a host the caller names, on the install's behalf",
}

// auditLegacyPermissions is what each handler checked BEFORE Phase 6j,
// transcribed from `git show HEAD:internal/api/handlers/audit.go` at commit
// eaeafa7 — nine handlers, nine checks, in three shapes:
//
//	List/ListRecent/Export   accessibleClusters(view, audit), no gate.
//	ListActions/ListUsers    requirePerm(view, audit) — global.
//	Get/Update/TestSyslog    requirePerm(manage, audit) — global.
//	ListByCluster            requireClusterPerm(view, audit, <:cluster_id>).
//
// ListByCluster ALSO calls accessibleClusters, and that is not a second gate:
// its own comment says so — the stamp is a no-op against a caller who just
// passed requireClusterPerm, and it is there so this endpoint is not the one
// audit read whose safety rests on a lock the other three share.
var auditLegacyPermissions = map[string]string{
	"GET /api/v1/audit-log":                      "view:audit (filtered)",
	"GET /api/v1/audit-log/recent":               "view:audit (filtered)",
	"GET /api/v1/audit-log/export":               "view:audit (filtered)",
	"GET /api/v1/audit-log/actions":              "view:audit",
	"GET /api/v1/audit-log/users":                "view:audit",
	"GET /api/v1/audit-log/syslog-config":        "manage:audit",
	"PUT /api/v1/audit-log/syslog-config":        "manage:audit",
	"POST /api/v1/audit-log/syslog-test":         "manage:audit",
	"GET /api/v1/clusters/:cluster_id/audit-log": "view:audit",
}

func declaredAuditEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if _, want := auditLegacyPermissions[e.Method+" "+e.Path]; want {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestAuditRoutesDeclareWhatTheyEnforced is the tally that makes this domain's
// nine checks a refactor rather than a change.
func TestAuditRoutesDeclareWhatTheyEnforced(t *testing.T) {
	declared := declaredAuditEndpoints(t)
	if len(declared) != auditRouteCount {
		t.Fatalf("the registry declares %d audit routes, want %d", len(declared), auditRouteCount)
	}
	if len(auditLegacyPermissions) != auditRouteCount {
		t.Fatalf("auditLegacyPermissions has %d entries, want %d", len(auditLegacyPermissions), auditRouteCount)
	}

	var advisory, global, cluster int
	for key, want := range auditLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s is in the tally but is not declared in the registry", key)
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
			continue
		}
		switch {
		case e.Permissions.Advisory != nil:
			advisory++
			if !strings.Contains(e.Permissions.Advisory.Reason, "applyAuditListScope") {
				t.Errorf("%s: the Advisory reason does not name what stamps the scope: %q",
					key, e.Permissions.Advisory.Reason)
			}
			// The COUNT is the half a per-row guard cannot repair, so the reason
			// has to say the scope lands on it too.
			if !strings.Contains(e.Permissions.Advisory.Reason, "count") {
				t.Errorf("%s: the Advisory reason does not mention the count query, which is the half no "+
					"per-row guard can repair: %q", key, e.Permissions.Advisory.Reason)
			}
		case e.Permissions.Check != nil && e.Permissions.Check.Scope == ScopeGlobal:
			global++
		case e.Permissions.Check != nil && e.Permissions.Check.Scope == ScopeCluster:
			cluster++
		}
	}
	if advisory != 3 || global != 5 || cluster != 1 {
		t.Errorf("the tally splits %d Advisory / %d global Check / %d cluster Check, want 3 / 5 / 1",
			advisory, global, cluster)
	}
}

// TestAuditPerClusterListingRefusesAClusterFilter is the assertion behind the
// one place this domain's two filter schemas deliberately differ.
//
// checkPathParams refuses a "cluster_id" ALIAS on a route whose gate resolves
// the cluster from the path (commit facdf56), and the per-cluster listing is
// exactly that: the handler stamps the path's cluster over whatever a query
// carried, so accepting a second spelling would let a caller believe they had
// filtered to a cluster the gate never authorized. It is therefore not declared
// there at all, and a caller who sends one is told so.
func TestAuditPerClusterListingRefusesAClusterFilter(t *testing.T) {
	const path = clusterScope + "/audit-log"
	e := declaredEndpoint(t, fiber.MethodGet, path)

	// :cluster_id IS declared here — as the PATH parameter, which is the one
	// spelling that cannot disagree with the gate.
	if got := e.Parameters["cluster_id"].Source; got != apischema.SourcePath {
		t.Errorf("cluster_id is declared with source %q, want path", got)
	}
	if _, declared := e.Parameters["filter_cluster_id"]; declared {
		t.Error("filter_cluster_id is declared on the per-cluster listing; the path already names the " +
			"cluster the gate authorized, and a second spelling could only disagree with it")
	}

	// The two instance-wide reads DO carry it, under the alias every caller
	// sends.
	for _, wide := range []string{auditScope, auditScope + "/export"} {
		prop := declaredEndpoint(t, fiber.MethodGet, wide).Parameters["filter_cluster_id"]
		if prop.Alias != "cluster_id" {
			t.Errorf("%s: filter_cluster_id declares alias %q, want cluster_id", wide, prop.Alias)
		}
	}

	// End to end: ?cluster_id= on the per-cluster listing is an unknown
	// parameter, not a silently ignored one.
	cap := &capture{}
	probe := e
	probe.Handler = cap.handler()
	probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), probe)

	target := strings.ReplaceAll(path, ":cluster_id", testClusterID) + "?cluster_id=" + testClusterID
	status, env := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400", status, env.Message)
	}
	if cap.called {
		t.Error("the handler ran for a request carrying a filter it does not accept")
	}
}

// TestAuditListBoundsArePinned is the other half of the assertion that moved
// out of handlers.TestParseAuditFilters_ClampsPagination.
//
// The handler used to CLAMP: a negative limit became 1, an oversized one became
// 200, and the caller was never told. It grew those clamps because
// safeconv.Int32 bounds only the int32 range, so `?limit=-1` once reached
// Postgres as `LIMIT -1` and came back a 500. The declaration refuses the value
// by name instead, which means it can no longer reach the query at all.
func TestAuditListBoundsArePinned(t *testing.T) {
	for _, path := range []string{auditScope, auditScope + "/export", clusterScope + "/audit-log"} {
		t.Run(path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodGet, path)
			if got := e.Parameters["limit"].Default; got != 50 {
				t.Errorf("limit default = %#v, want 50 — the value the handler substituted", got)
			}
			// Asserted on the property rather than over the wire: a request
			// long enough to breach it does not fit in fasthttp's 4 KiB header
			// buffer, so the only place the bound is observable is here.
			// parseVmidsParam's own 500-entry cap is pinned by
			// handlers.TestParseVmidsParamCap.
			if got := e.Parameters["vmids"].MaxLength; got == nil || *got != 6000 {
				t.Errorf("vmids MaxLength = %v, want 6000 — 500 nine-digit VMIDs plus separators", got)
			}
			target := strings.ReplaceAll(path, ":cluster_id", testClusterID)

			for _, tt := range []struct {
				query string
				want  int
			}{
				{"", fiber.StatusNoContent},
				{"?limit=200", fiber.StatusNoContent},
				{"?limit=-1", fiber.StatusBadRequest},
				{"?limit=0", fiber.StatusBadRequest},
				{"?limit=201", fiber.StatusBadRequest},
				{"?offset=-1", fiber.StatusBadRequest},
				{"?user_id=not-a-uuid", fiber.StatusBadRequest},
				{"?vmids=100,101", fiber.StatusNoContent},
				{"?since=yesterday", fiber.StatusBadRequest},
			} {
				cap := &capture{}
				probe := e
				probe.Handler = cap.handler()
				probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
				app := newRegistryApp(t, noAuth(), probe)

				status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+tt.query, nil))
				if status != tt.want {
					t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
				}
			}
		})
	}
}

// TestAuditExportFormatIsAnEnum pins the vocabulary the handler used to check by
// hand, and the default it substituted.
func TestAuditExportFormatIsAnEnum(t *testing.T) {
	prop := declaredEndpoint(t, fiber.MethodGet, auditScope+"/export").Parameters["format"]
	if prop.Default != "json" {
		t.Errorf("format default = %#v, want \"json\"", prop.Default)
	}
	want := map[string]bool{"json": true, "csv": true, "syslog": true}
	if len(prop.Enum) != len(want) {
		t.Fatalf("format enum = %v, want json, csv and syslog", prop.Enum)
	}
	for _, v := range prop.Enum {
		if !want[v] {
			t.Errorf("format enum carries %q, which exportCSV/exportSyslog/exportJSON have no branch for", v)
		}
	}
}

// TestSyslogConfigSchemaIsSharedByBothWrites pins that the store and the probe
// take the same body. They are one config, and two schemas would be two
// vocabularies for one thing — with the probe quietly accepting a field the
// store refuses, or the reverse.
func TestSyslogConfigSchemaIsSharedByBothWrites(t *testing.T) {
	put := declaredEndpoint(t, fiber.MethodPut, auditScope+"/syslog-config").Parameters
	post := declaredEndpoint(t, fiber.MethodPost, auditScope+"/syslog-test").Parameters

	if len(put) != len(post) {
		t.Fatalf("the store declares %d parameters and the probe %d; they take one config", len(put), len(post))
	}
	for name, prop := range put {
		other, ok := post[name]
		if !ok {
			t.Errorf("the store declares %q and the probe does not", name)
			continue
		}
		if prop.Type != other.Type || prop.Optional != other.Optional {
			t.Errorf("%q differs between the two writes: %+v vs %+v", name, prop, other)
		}
	}

	// The host bound is load-bearing rather than cosmetic: neither endpoint
	// bounded it before, and the probe's audit row wraps the dialler's message
	// around it — so an unbounded host was a body-sized string in a table every
	// Viewer can read.
	if put["host"].MaxLength == nil {
		t.Error("host declares no maximum length; an unbounded one reaches an audit row every Viewer can read")
	}
}

// TestEveryAuditEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryAuditEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredAuditEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Audit Log" {
			t.Errorf("%s is in group %q, want Audit Log", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
