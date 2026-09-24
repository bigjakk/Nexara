package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// metricServerRouteCount is how many endpoints registerMetricServerEndpoints
// declares.
const metricServerRouteCount = 5

// metricServerLegacyPermissions is the permission each handler checked with a
// hand-placed requireClusterPerm call BEFORE Phase 6j, transcribed from
// `git show HEAD:internal/api/handlers/metric_servers.go` at commit eaeafa7 —
// five handlers, five calls, every one cluster-scoped on the CLUSTER resource
// rather than on a metric_server resource of its own.
//
// That is carried across unchanged and deliberately: these endpoints edit the
// cluster's status.cfg, so the permission that opens them is the one that edits
// the cluster. Introducing a narrower resource would be a permission-catalogue
// change, not a migration.
var metricServerLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/metric-servers":               "view:cluster",
	"POST /api/v1/clusters/:cluster_id/metric-servers":              "manage:cluster",
	"GET /api/v1/clusters/:cluster_id/metric-servers/:server_id":    "view:cluster",
	"PUT /api/v1/clusters/:cluster_id/metric-servers/:server_id":    "manage:cluster",
	"DELETE /api/v1/clusters/:cluster_id/metric-servers/:server_id": "manage:cluster",
}

func declaredMetricServerEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, metricServerScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestMetricServerRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes deleting five requireClusterPerm calls a refactor rather than a change.
func TestMetricServerRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredMetricServerEndpoints(t)
	if len(declared) != metricServerRouteCount {
		t.Fatalf("the registry declares %d metric-server routes, want %d", len(declared), metricServerRouteCount)
	}
	if len(metricServerLegacyPermissions) != metricServerRouteCount {
		t.Fatalf("metricServerLegacyPermissions has %d entries, want %d",
			len(metricServerLegacyPermissions), metricServerRouteCount)
	}

	var view, manage int
	for key, want := range metricServerLegacyPermissions {
		switch want {
		case "view:cluster":
			view++
		case "manage:cluster":
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
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path", key, e.Permissions.Check.Scope)
		}
	}
	if view != 2 || manage != 3 {
		t.Errorf("the tally splits %d view:cluster / %d manage:cluster, want 2 / 3", view, manage)
	}

	for key := range declared {
		if _, listed := metricServerLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in metricServerLegacyPermissions", key)
		}
	}
}

// TestMetricServerCreateTakesIDUnderItsAlias is the assertion behind the one
// declaration in this domain that could not use the wire name it was given.
//
// checkPathParams refuses a parameter NAMED "id" that resolves to anything but
// the path, because clusterIDFromParam reads TWO names — cluster_id, falling
// back to id — so "id" is a name the permission gate also reads. The create
// body genuinely carries one, so it is declared as server_id with "id" as its
// alias, and BOTH spellings have to keep working: the dialog sends "id".
func TestMetricServerCreateTakesIDUnderItsAlias(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, metricServerScope)
	if _, wrong := e.Parameters["id"]; wrong {
		t.Fatal("the create body declares a parameter literally named id; checkPathParams refuses that " +
			"on a cluster-scoped route, and Register would have panicked")
	}
	if got := e.Parameters["server_id"].Alias; got != "id" {
		t.Fatalf("server_id declares alias %q, want id — the dialog sends id and would otherwise get "+
			"\"unknown parameter\"", got)
	}

	target := strings.ReplaceAll(metricServerScope, ":cluster_id", testClusterID)
	for _, spelling := range []string{"id", "server_id"} {
		cap := &capture{}
		probe := e
		probe.Handler = cap.handler()
		probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), probe)

		body := `{"` + spelling + `":"influx01","type":"influxdb","server":"metrics.example.com","port":8086}`
		status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
		if status != fiber.StatusNoContent {
			t.Fatalf("spelling %q: status = %d (%q), want 204", spelling, status, env.Message)
		}
		if got := cap.params.String("server_id"); got != "influx01" {
			t.Errorf("spelling %q reached the handler as server_id=%q", spelling, got)
		}
	}
}

// TestMetricServerFlagsStayTristate pins the two PVE 0/1 flags.
//
// Proxmox reads an absent option as "leave the stored value alone" and an
// explicit 0 as "turn it off" — the same absent-versus-zero distinction that
// destroyed a VM's boot disk through disks/attach. The handler keeps the two
// apart with p.OptInt, which a declared default cannot fool
// (apischema.Property.Default); a Default would still document every save
// that never touched the checkbox as writing it.
func TestMetricServerFlagsStayTristate(t *testing.T) {
	const path = metricServerScope + "/:server_id"
	e := declaredEndpoint(t, fiber.MethodPut, path)
	for _, name := range []string{"disable", "verify-certificate", "port"} {
		prop := e.Parameters[name]
		if !prop.Optional {
			t.Errorf("%s is required on the update; the dialog saves without it", name)
		}
		if prop.Default != nil {
			t.Errorf("%s declares default %#v; an omitted one leaves the stored value alone, and a "+
				"default would document it as written", name, prop.Default)
		}
	}

	target := strings.NewReplacer(":cluster_id", testClusterID, ":server_id", "influx01").Replace(path)
	cap := &capture{}
	probe := e
	probe.Handler = cap.handler()
	probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), probe)

	if status, env := send(t, app, jsonRequest(http.MethodPut, target, `{"disable":0}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if value, supplied := cap.params.OptInt("disable"); !supplied || value != 0 {
		t.Errorf("an explicit disable:0 read back as (%d, supplied=%v)", value, supplied)
	}
	if _, supplied := cap.params.OptInt("verify-certificate"); supplied {
		t.Error("verify-certificate reads as supplied on a body that omitted it")
	}
}

// TestMetricServerTokenIsWriteOnly is the secret-handling assertion this domain
// needed.
//
// The InfluxDB token is a credential the caller sends and neither read endpoint
// returns. What the declaration can say is that it is optional and bounded; what
// this pins in addition is that no audit row is built from Params.Raw(), which
// would publish the token to every Viewer — view:audit is a default Viewer
// grant. The handler-side half is
// handlers.TestGuard_MetricServerAuditOmitsTheToken.
func TestMetricServerTokenIsWriteOnly(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodPost, metricServerScope},
		{fiber.MethodPut, metricServerScope + "/:server_id"},
	} {
		prop := declaredEndpoint(t, tt.method, tt.path).Parameters["token"]
		if !prop.Optional {
			t.Errorf("%s %s: token is required; a graphite server has none", tt.method, tt.path)
		}
		if prop.Default != nil {
			t.Errorf("%s %s: token declares a default; a credential must never have one", tt.method, tt.path)
		}
		if !strings.Contains(prop.Description, "Write-only") {
			t.Errorf("%s %s: the token description does not say it is write-only: %q",
				tt.method, tt.path, prop.Description)
		}
	}
}

// TestEveryMetricServerEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing.
func TestEveryMetricServerEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredMetricServerEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Metrics" {
			t.Errorf("%s is in group %q, want Metrics", key, e.Group)
		}
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first", key, names)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
