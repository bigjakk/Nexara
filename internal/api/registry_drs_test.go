package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to registry_drs.go
// that quietly loosened a parameter would show up here.

// drsRouteCount is how many endpoints registerDRSEndpoints declares. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum
// of per-domain constants rather than one number.
const drsRouteCount = 10

const testDRSRuleID = "8a1d2ab3-3f0e-4b4c-9f8a-000000000003"

// drsRoute renders one DRS route's path with the test ids substituted.
func drsRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":rule_id", testDRSRuleID,
		":rule_name", "ha-rule01",
	).Replace(path)
}

// drsLegacyPermissions is the permission each DRS handler checked with a
// hand-placed requireClusterPerm call BEFORE Phase 6b, transcribed from
// internal/api/handlers/drs.go at commit 13ceac0 (10 handlers, 10 calls,
// every one of them cluster-scoped on the "drs" resource).
//
// The two ha-rules writes are the entries worth pausing on: they reach the
// same PVE endpoints HAHandler does, and they are deliberately gated on
// drs rather than ha. Delegating either to the HA handler would silently
// change which permission opens the route, which is why DeleteHARule is a
// separate function with its own comment saying so.
var drsLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/drs/config":                 "view:drs",
	"PUT /api/v1/clusters/:cluster_id/drs/config":                 "manage:drs",
	"GET /api/v1/clusters/:cluster_id/drs/rules":                  "view:drs",
	"POST /api/v1/clusters/:cluster_id/drs/rules":                 "manage:drs",
	"DELETE /api/v1/clusters/:cluster_id/drs/rules/:rule_id":      "manage:drs",
	"POST /api/v1/clusters/:cluster_id/drs/evaluate":              "manage:drs",
	"GET /api/v1/clusters/:cluster_id/drs/history":                "view:drs",
	"GET /api/v1/clusters/:cluster_id/drs/ha-rules":               "view:drs",
	"POST /api/v1/clusters/:cluster_id/drs/ha-rules":              "manage:drs",
	"DELETE /api/v1/clusters/:cluster_id/drs/ha-rules/:rule_name": "manage:drs",
}

// declaredDRSEndpoints returns every declaration whose path is under the
// /drs prefix, keyed "METHOD path".
func declaredDRSEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	return declaredEndpointsWhere(t, underPath(drsScope))
}

// TestDRSRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes deleting 10 requireClusterPerm calls a refactor rather than a
// change.
func TestDRSRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredDRSEndpoints(t)
	if len(declared) != drsRouteCount {
		t.Fatalf("the registry declares %d DRS routes, want %d", len(declared), drsRouteCount)
	}
	if len(drsLegacyPermissions) != drsRouteCount {
		t.Fatalf("drsLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(drsLegacyPermissions), drsRouteCount)
	}

	var view, manage int
	for key, want := range drsLegacyPermissions {
		switch want {
		case "view:drs":
			view++
		case "manage:drs":
			manage++
		}
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; every DRS route's permission is statically known",
				key, e.Permissions.Describe())
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path, so the "+
				"declaration has to as well", key, e.Permissions.Check.Scope)
		}
	}
	if view != 4 || manage != 6 {
		t.Errorf("the tally splits %d view:drs / %d manage:drs, want 4 / 6", view, manage)
	}

	for key := range declared {
		if _, listed := drsLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in drsLegacyPermissions — a new DRS route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestDRSHARuleRoutesStayOnTheDRSPermission is the specific claim the two
// ha-rules routes rest on, asserted rather than left in a comment: they
// write the same PVE objects HAHandler does and are gated on manage:drs,
// NOT manage:ha. An operator's DRS role opens them; their HA role does
// not, and the reverse.
func TestDRSHARuleRoutesStayOnTheDRSPermission(t *testing.T) {
	declared := declaredDRSEndpoints(t)
	for _, key := range []string{
		fiber.MethodPost + " " + drsScope + "/ha-rules",
		fiber.MethodDelete + " " + drsScope + "/ha-rules/:rule_name",
	} {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s is not declared", key)
			continue
		}
		if got := e.Permissions.Describe(); got != "manage:drs" {
			t.Errorf("%s declares %q, want manage:drs — delegating to the HA handler would change which "+
				"permission opens the route", key, got)
		}
	}
	// And the HA tab's own rule routes still require manage:ha, so the two
	// doors onto one PVE endpoint really do differ.
	if got := declaredEndpoint(t, fiber.MethodDelete, haScope+"/rules/:rule").Permissions.Describe(); got != "manage:ha" {
		t.Errorf("the HA tab's rule delete declares %q, want manage:ha", got)
	}
}

// TestDRSRoutesDeclareEveryPathParameter re-states checkPathParams' rule
// for this domain and adds the one it does NOT enforce: that :cluster_id
// is the FIRST placeholder.
func TestDRSRoutesDeclareEveryPathParameter(t *testing.T) {
	for key, e := range declaredDRSEndpoints(t) {
		names := pathParamNames(e.Path)
		if len(names) == 0 || names[0] != "cluster_id" {
			t.Errorf("%s has path parameters %v; :cluster_id must be the first, or the permission "+
				"middleware cannot resolve the cluster the route acts on", key, names)
		}
		for _, name := range names {
			prop, ok := e.Parameters[name]
			if !ok {
				t.Errorf("%s has :%s with no entry in Parameters", key, name)
				continue
			}
			if prop.Optional {
				t.Errorf("%s declares the path parameter %q optional; a URL segment is always present",
					key, name)
			}
		}
	}
	// The two rule parameters name different things and must not be
	// confused: :rule_id is Nexara's own row id, :rule_name is Proxmox's.
	if got := declaredEndpoint(t, fiber.MethodDelete, drsScope+"/rules/:rule_id").Parameters["rule_id"].Format; got != "uuid" {
		t.Errorf("rule_id declares format %q, want uuid — it is Nexara's own row id", got)
	}
	if got := declaredEndpoint(t, fiber.MethodDelete, drsScope+"/ha-rules/:rule_name").Parameters["rule_name"].Format; got != "" {
		t.Errorf("rule_name declares format %q; it is a Proxmox rule NAME, not a uuid", got)
	}
}

// probeDRSEndpoint is a declared DRS endpoint with its handler swapped for
// a capture.
func probeDRSEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestDRSVocabulariesComeFromTheHandlers pins that the two enums ARE
// handlers.DRSModes and handlers.DRSRuleTypes rather than hand-written
// literals beside them, and that neither aliases the package-level slice.
func TestDRSVocabulariesComeFromTheHandlers(t *testing.T) {
	mode := declaredEndpoint(t, fiber.MethodPut, drsScope+"/config").Parameters["mode"]
	if !slices.Equal(mode.Enum, handlers.DRSModes) {
		t.Errorf("mode enum = %v, want handlers.DRSModes %v", mode.Enum, handlers.DRSModes)
	}
	if len(mode.Enum) > 0 && &mode.Enum[0] == &handlers.DRSModes[0] {
		t.Error("the declared mode enum aliases handlers.DRSModes; clone it")
	}

	for _, path := range []string{drsScope + "/rules", drsScope + "/ha-rules"} {
		rt := declaredEndpoint(t, fiber.MethodPost, path).Parameters["rule_type"]
		if !slices.Equal(rt.Enum, handlers.DRSRuleTypes) {
			t.Errorf("%s: rule_type enum = %v, want handlers.DRSRuleTypes %v", path, rt.Enum, handlers.DRSRuleTypes)
		}
		if len(rt.Enum) > 0 && &rt.Enum[0] == &handlers.DRSRuleTypes[0] {
			t.Errorf("%s: the declared rule_type enum aliases handlers.DRSRuleTypes; clone it", path)
		}
	}
}

// TestDRSConfigBody covers the body the config card sends, and the three
// hand-rolled checks that used to guard it.
//
// One of those checks is still in the handler on purpose and is asserted
// here from the outside: apischema's Minimum is inclusive, so the schema
// cannot say "greater than 0" and the endpoint refusal stays where it was.
// A threshold of exactly 0 has to keep coming back as a 400.
func TestDRSConfigBody(t *testing.T) {
	const path = drsScope + "/config"
	target := drsRoute(path)
	full := `{"mode":"advisory","weights":{"cpu":0.5,"memory":0.5},"imbalance_threshold":0.25,` +
		`"eval_interval_seconds":300,"include_containers":false,"exclude_veeam_workers":true}`

	t.Run("the config card's body is accepted", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPut, path, cap))
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, full)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got := cap.params.Float("imbalance_threshold"); got != 0.25 {
			t.Errorf("imbalance_threshold = %v, want 0.25", got)
		}
		weights := cap.params.Object("weights")
		raw, err := json.Marshal(weights)
		if err != nil {
			t.Fatalf("marshal weights: %v", err)
		}
		if string(raw) != `{"cpu":0.5,"memory":0.5}` {
			t.Errorf("weights marshalled to %s; the JSONB column stores exactly this", raw)
		}
	})

	t.Run("omitted weights carry the declared default", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPut, path, cap))
		body := `{"mode":"advisory","imbalance_threshold":0.25,"eval_interval_seconds":300}`
		if status, env := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		raw, err := json.Marshal(cap.params.Object("weights"))
		if err != nil {
			t.Fatalf("marshal weights: %v", err)
		}
		// The same object the handler used to substitute for a missing key.
		if string(raw) != `{"cpu":0.3,"memory":0.7}` {
			t.Errorf("default weights marshalled to %s, want {\"cpu\":0.3,\"memory\":0.7}", raw)
		}
		if cap.params.Has("weights") {
			t.Error("weights reads as supplied; a default is not something the caller sent")
		}
	})

	t.Run("exclude_veeam_workers stays tri-state", func(t *testing.T) {
		// The whole reason the stored column is nullable: omitting the key
		// must leave the stored value alone. The handler's p.OptBool read
		// does, default or not (apischema.Property.Default); a Default of
		// false would document every save as disarming the Veeam protection.
		prop := declaredEndpoint(t, fiber.MethodPut, path).Parameters["exclude_veeam_workers"]
		if !prop.Optional {
			t.Error("exclude_veeam_workers is required; a client predating the field would be refused")
		}
		if prop.Default != nil {
			t.Errorf("exclude_veeam_workers declares default %#v; an omitted one keeps the stored value, "+
				"and a default would document it as set", prop.Default)
		}

		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPut, path, cap))
		body := `{"mode":"advisory","imbalance_threshold":0.25,"eval_interval_seconds":300}`
		send(t, app, jsonRequest(http.MethodPut, target, body))
		if _, supplied := cap.params.OptBool("exclude_veeam_workers"); supplied {
			t.Error("exclude_veeam_workers reads as supplied on a body that omitted it")
		}

		cap = &capture{}
		app = newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPut, path, cap))
		body = `{"mode":"advisory","imbalance_threshold":0.25,"eval_interval_seconds":300,"exclude_veeam_workers":false}`
		send(t, app, jsonRequest(http.MethodPut, target, body))
		value, supplied := cap.params.OptBool("exclude_veeam_workers")
		if !supplied || value {
			t.Errorf("an explicit false read back as (%v, supplied=%v)", value, supplied)
		}
	})

	for _, tt := range []struct{ name, body, field string }{
		{"missing mode", `{"imbalance_threshold":0.25,"eval_interval_seconds":300}`, "mode:"},
		{"unknown mode", `{"mode":"aggressive","imbalance_threshold":0.25,"eval_interval_seconds":300}`, "mode:"},
		{"empty mode", `{"mode":"","imbalance_threshold":0.25,"eval_interval_seconds":300}`, "mode:"},
		{"threshold above 1", `{"mode":"advisory","imbalance_threshold":1.5,"eval_interval_seconds":300}`, "imbalance_threshold:"},
		{"negative threshold", `{"mode":"advisory","imbalance_threshold":-0.1,"eval_interval_seconds":300}`, "imbalance_threshold:"},
		{"missing threshold", `{"mode":"advisory","eval_interval_seconds":300}`, "imbalance_threshold:"},
		{"interval under a minute", `{"mode":"advisory","imbalance_threshold":0.25,"eval_interval_seconds":59}`, "eval_interval_seconds:"},
		{"interval past int32", `{"mode":"advisory","imbalance_threshold":0.25,"eval_interval_seconds":4294967296}`, "eval_interval_seconds:"},
		{"hold-off past a day", `{"mode":"advisory","imbalance_threshold":0.25,"eval_interval_seconds":300,"boot_holdoff_minutes":1441}`, "boot_holdoff_minutes:"},
		{"negative hold-off", `{"mode":"advisory","imbalance_threshold":0.25,"eval_interval_seconds":300,"boot_holdoff_minutes":-1}`, "boot_holdoff_minutes:"},
		{"weights as an array", `{"mode":"advisory","weights":[1,2],"imbalance_threshold":0.25,"eval_interval_seconds":300}`, "weights:"},
		{"a misspelled key", `{"mode":"advisory","imbalance_threshold":0.25,"eval_interval_seconds":300,"include_container":true}`, "include_container:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPut, path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPut, target, tt.body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.field) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.field)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}

	// The schema can only call weights "an object", so UpdateConfig refuses a
	// weight outside 0..drs.MaxWeight before anything is stored: 1e200 made
	// every imbalance +Inf. The real handler runs, with no database behind it.
	t.Run("a weight out of range is refused", func(t *testing.T) {
		e := declaredEndpoint(t, fiber.MethodPut, path)
		e.Handler = handlers.NewDRSHandler(nil, "", nil, nil).UpdateConfig
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)
		for _, weights := range []string{
			`{"cpu":1e200,"memory":1e200}`,
			`{"cpu":1e400,"memory":0.7}`,
			`{"cpu":-0.1,"memory":0.7}`,
			`{"cpu":0.3,"memory":"0.7"}`,
		} {
			body := `{"mode":"advisory","weights":` + weights + `,"imbalance_threshold":0.25,"eval_interval_seconds":300}`
			status, env := send(t, app, jsonRequest(http.MethodPut, target, body))
			if status != fiber.StatusBadRequest || !strings.HasPrefix(env.Message, "weights.") {
				t.Errorf("%s: status = %d (%q), want 400 naming the weight", weights, status, env.Message)
			}
		}
	})

	// A threshold of exactly 0 passes the SCHEMA — the bound is inclusive —
	// and has to be refused by the handler instead. This drives the real
	// declaration with the real handler attached, so it is the endpoint
	// refusal being tested, not the schema's.
	t.Run("a zero threshold is still refused", func(t *testing.T) {
		e := declaredEndpoint(t, fiber.MethodPut, path)
		if e.Parameters["imbalance_threshold"].Minimum == nil || *e.Parameters["imbalance_threshold"].Minimum != 0 {
			t.Fatalf("the declared minimum is %v; this test assumes the schema lets 0 through so the "+
				"handler can refuse it", e.Parameters["imbalance_threshold"].Minimum)
		}
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPut, path, cap))
		body := `{"mode":"advisory","imbalance_threshold":0,"eval_interval_seconds":300}`
		if status, _ := send(t, app, jsonRequest(http.MethodPut, target, body)); status != fiber.StatusNoContent {
			t.Fatalf("the schema refused 0 (status %d); the handler's own check would then be dead code", status)
		}
		if !cap.called {
			t.Fatal("the handler did not run, so its refusal is untested")
		}
	})
}

// TestDRSRuleBodiesKeepTheEmptyArray is this domain's sentinel: the create
// dialog sends node_names:[] for an affinity or anti-affinity rule,
// because the node picker only renders for a pin. A MinLength on
// node_names would 400 two of the three rule types, and only on the paths
// nobody clicks while testing a pin rule.
func TestDRSRuleBodiesKeepTheEmptyArray(t *testing.T) {
	for _, tt := range []struct{ name, path, body string }{
		{
			"affinity rule with no nodes",
			drsScope + "/rules",
			`{"rule_type":"affinity","vm_ids":[100,101],"node_names":[],"enabled":true}`,
		},
		{
			"pin rule with nodes",
			drsScope + "/rules",
			`{"rule_type":"pin","vm_ids":[100],"node_names":["pve-01","pve-02"],"enabled":true}`,
		},
		{
			"HA anti-affinity rule with no nodes",
			drsScope + "/ha-rules",
			`{"rule_name":"ha-rule01","rule_type":"anti-affinity","vm_ids":[100,101],"node_names":[],"enabled":true}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPost, tt.path, cap))
			if status, env := send(t, app, jsonRequest(http.MethodPost, drsRoute(tt.path), tt.body)); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
		})
	}

	// The VMIDs have to survive as numbers, because the column is JSONB and
	// the API's own response type is number[]. Params.Strings renders them
	// as text, so this is what intListFromStrings exists to undo.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPost, drsScope+"/rules", cap))
	body := `{"rule_type":"affinity","vm_ids":[100,101],"node_names":[],"enabled":true}`
	send(t, app, jsonRequest(http.MethodPost, drsRoute(drsScope+"/rules"), body))
	if got := cap.params.Strings("vm_ids"); !slices.Equal(got, []string{"100", "101"}) {
		t.Errorf("vm_ids = %v, want the two ids in order", got)
	}

	// apischema coerces a numeric STRING to an integer — it is deliberately
	// forgiving in the ways real clients are sloppy (see coerce in
	// validate.go) — so a caller sending ["100"] gets the same rule as one
	// sending [100], and intListFromStrings still sees decimal text either
	// way. Pinned because the JSONB column would otherwise be able to hold
	// strings depending on which client wrote the rule.
	cap = &capture{}
	app = newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPost, drsScope+"/rules", cap))
	quoted := `{"rule_type":"affinity","vm_ids":["100","101"],"node_names":[],"enabled":true}`
	if status, env := send(t, app, jsonRequest(http.MethodPost, drsRoute(drsScope+"/rules"), quoted)); status != fiber.StatusNoContent {
		t.Fatalf("quoted vmids: status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.Strings("vm_ids"); !slices.Equal(got, []string{"100", "101"}) {
		t.Errorf("quoted vm_ids = %v, want them coerced to the same two integers", got)
	}
}

// TestDRSRuleBodiesRejectWhatTheHandlersUsedTo pins the four "x is
// required" checks the handlers no longer make, plus the difference
// between the two rule bodies: a Nexara rule may name no guests, a Proxmox
// HA rule may not.
func TestDRSRuleBodiesRejectWhatTheHandlersUsedTo(t *testing.T) {
	for _, tt := range []struct{ name, path, body, field string }{
		{"rule with no type", drsScope + "/rules", `{"vm_ids":[100]}`, "rule_type:"},
		{"rule with an unknown type", drsScope + "/rules", `{"rule_type":"colocate","vm_ids":[100]}`, "rule_type:"},
		{"HA rule with no name", drsScope + "/ha-rules", `{"rule_type":"pin","vm_ids":[100]}`, "rule_name:"},
		{"HA rule with an empty name", drsScope + "/ha-rules", `{"rule_name":"","rule_type":"pin","vm_ids":[100]}`, "rule_name:"},
		{"HA rule with no vm_ids", drsScope + "/ha-rules", `{"rule_name":"ha-rule01","rule_type":"pin"}`, "vm_ids:"},
		{"HA rule with empty vm_ids", drsScope + "/ha-rules", `{"rule_name":"ha-rule01","rule_type":"pin","vm_ids":[]}`, "vm_ids:"},
		{"a vmid that is not a number", drsScope + "/rules", `{"rule_type":"pin","vm_ids":["abc"]}`, "vm_ids[0]:"},
		{"a fractional vmid", drsScope + "/rules", `{"rule_type":"pin","vm_ids":[1.5]}`, "vm_ids[0]:"},
		{"a zero vmid", drsScope + "/rules", `{"rule_type":"pin","vm_ids":[0]}`, "vm_ids[0]:"},
		{"a node name that is not one", drsScope + "/rules", `{"rule_type":"pin","vm_ids":[100],"node_names":["a/b"]}`, "node_names[0]:"},
		{"a misspelled key", drsScope + "/rules", `{"rule_type":"pin","vm_ids":[100],"vm_names":[]}`, "vm_names:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPost, tt.path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, drsRoute(tt.path), tt.body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.field) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.field)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}

	// A Nexara rule with no guests at all is still accepted: the handler
	// substituted [] for a missing vm_ids and the frontend guard, not the
	// API, is what requires two.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPost, drsScope+"/rules", cap))
	if status, env := send(t, app, jsonRequest(http.MethodPost, drsRoute(drsScope+"/rules"), `{"rule_type":"pin"}`)); status != fiber.StatusNoContent {
		t.Errorf("status = %d (%q), want 204 — vm_ids has always been optional on a Nexara rule", status, env.Message)
	}
}

// TestDRSHistoryLimitIsBounded is the first query parameter in this domain
// and a deliberate behaviour change: the handler used to CLAMP an
// out-of-range limit back to 50 and answer 200, so ?limit=5000 quietly
// returned 50 rows and said nothing. The declaration refuses it instead.
func TestDRSHistoryLimitIsBounded(t *testing.T) {
	const path = drsScope + "/history"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	limit := e.Parameters["limit"]
	if limit.Default != 50 {
		t.Errorf("limit default = %#v, want 50 — the value the handler substituted", limit.Default)
	}

	target := drsRoute(path)
	for _, tt := range []struct {
		query string
		want  int
		value int64
	}{
		{query: "", want: fiber.StatusNoContent, value: 50},
		{query: "?limit=25", want: fiber.StatusNoContent, value: 25},
		{query: "?limit=500", want: fiber.StatusNoContent, value: 500},
		{query: "?limit=501", want: fiber.StatusBadRequest},
		{query: "?limit=0", want: fiber.StatusBadRequest},
		{query: "?limit=-1", want: fiber.StatusBadRequest},
		{query: "?limit=many", want: fiber.StatusBadRequest},
		{query: "?limit=", want: fiber.StatusBadRequest},
		{query: "?offset=10", want: fiber.StatusBadRequest},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodGet, path, cap))
		status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+tt.query, nil))
		if status != tt.want {
			t.Errorf("%q: status = %d (%q), want %d", tt.query, status, env.Message, tt.want)
			continue
		}
		if tt.want == fiber.StatusNoContent && cap.params.Int("limit") != tt.value {
			t.Errorf("%q: limit = %d, want %d", tt.query, cap.params.Int("limit"), tt.value)
		}
	}
}

// TestDRSEvaluateTakesNoBodyAtAll covers the one request shape in this
// domain that carries nothing: apiClient.post(url) with a single argument
// sends body:null while still setting Content-Type: application/json, so a
// decoder that insisted on a parseable object would 400 the button.
func TestDRSEvaluateTakesNoBodyAtAll(t *testing.T) {
	const path = drsScope + "/evaluate"
	if got := len(declaredEndpoint(t, fiber.MethodPost, path).Parameters); got != 1 {
		t.Fatalf("evaluate declares %d parameters, want just :cluster_id", got)
	}
	for _, body := range []string{``, `{}`} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPost, path, cap))
		if status, env := send(t, app, jsonRequest(http.MethodPost, drsRoute(path), body)); status != fiber.StatusNoContent {
			t.Errorf("body %q: status = %d (%q), want 204", body, status, env.Message)
		}
	}
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPost, path, cap))
	if status, _ := send(t, app, jsonRequest(http.MethodPost, drsRoute(path), `{"force":true}`)); status != fiber.StatusBadRequest {
		t.Errorf("status = %d for an undeclared parameter, want 400", status)
	}
}

// TestDRSRouteIsGatedByItsDeclaration proves the permission the
// declaration states is the permission the route enforces, end to end.
//
// It is run against the HA-rule delete because that is the route where a
// wrong gate would be least visible: it writes a Proxmox object the HA tab
// also writes, under a different permission, and a mistake would read as
// "the HA role covers it" rather than as a missing check.
func TestDRSRouteIsGatedByItsDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, drsScope+"/ha-rules/:rule_name")
	if e.Permissions.Describe() != "manage:drs" {
		t.Fatalf("the HA-rule delete declares %q, want manage:drs", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := drsRoute(drsScope + "/ha-rules/:rule_name")

	t.Run("manage:ha does not open it", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:ha": true, "view:drs": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodDelete, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller holding only the HA permission")
		}
	})

	t.Run("manage:drs does", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:drs": true}), gated)
		status, env := send(t, app, authedRequest(http.MethodDelete, target))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:drs": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryDRSEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryDRSEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredDRSEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "DRS" {
			t.Errorf("%s is in group %q, want DRS", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}

// The create dialog's Enabled box reaches Proxmox: an explicit false sends
// pve-ha-manager's disable=1, and true or an omitted key sends no disable,
// which Proxmox creates enabled. Real declaration and handler, stand-in Proxmox.
func TestDRSCreateHARuleForwardsEnabled(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		enabled     string // the body's "enabled" member, or "" to omit it
		wantDisable string
	}{
		{enabled: `,"enabled":false`, wantDisable: "1"},
		{enabled: `,"enabled":true`},
		{},
	} {
		pve, dbtx := &haCreatePVE{}, &haCreateDBTX{}
		e := declaredEndpoint(t, fiber.MethodPost, drsScope+"/ha-rules")
		e.Handler = handlers.NewDRSHandler(db.New(dbtx), haCreateEncKey, nil, nil).CreateHARule
		app := newHAStandInApp(t, e, pve, dbtx, map[string]bool{"manage:drs": true})
		body := `{"rule_name":"ha-rule01","rule_type":"anti-affinity","vm_ids":[100,101]` + tt.enabled + `}`
		req := jsonRequest(http.MethodPost, drsRoute(drsScope+"/ha-rules"), body)
		req.Header.Set("X-Test-User", "yes")
		if status, env := send(t, app, req); status != fiber.StatusCreated {
			t.Fatalf("%s: status = %d (%q), want 201", body, status, env.Message)
		}
		if len(pve.seen) != 1 || pve.seen[0].path != "/api2/json/cluster/ha/rules" {
			t.Fatalf("%s: Proxmox received %+v, want one POST to /cluster/ha/rules", body, pve.seen)
		}
		if got := pve.seen[0].form.Get("disable"); got != tt.wantDisable {
			t.Errorf("%s: disable = %q, want %q", body, got, tt.wantDisable)
		}
	}
}

// PVE 8 has no HA rules (pve-ha-manager 4.0.x; rules arrived in 5.0.2, PVE 9.0):
// a known older version is refused before Proxmox is asked, and an unknown one
// that Proxmox answers with its 501 gets the same 400, not the 501. The DRS
// page's create and the HA tab's share the check.
func TestCreateHARuleRefusedWithoutHARules(t *testing.T) {
	t.Parallel()
	routes := []struct {
		route, path, body, grant string
		handler                  func(*db.Queries) func(fiber.Ctx, *apischema.Params) error
	}{
		{"DRS", drsScope + "/ha-rules", `{"rule_name":"ha-rule01","rule_type":"anti-affinity","vm_ids":[100,101]}`, "manage:drs",
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewDRSHandler(q, haCreateEncKey, nil, nil).CreateHARule
			}},
		{"HA", haScope + "/rules", `{"rule":"ha-rule01","type":"resource-affinity","resources":"vm:100,vm:101","affinity":"negative"}`, "manage:ha",
			func(q *db.Queries) func(fiber.Ctx, *apischema.Params) error {
				return handlers.NewHAHandler(q, haCreateEncKey, nil).CreateRule
			}},
	}
	for _, r := range routes {
		for _, tt := range []struct {
			name      string
			version   string // the cluster row's pve_version; "" leaves no row to read
			wantCalls int
		}{
			{name: "PVE 8 on record", version: "8.4", wantCalls: 0},
			{name: "version not known, Proxmox answers 501", wantCalls: 1},
		} {
			pve := &haCreatePVE{replies: map[string]string{"POST /api2/json/cluster/ha/rules": "unrouted"}}
			dbtx := &haCreateDBTX{}
			if tt.version != "" {
				dbtx.models = map[string]any{"GetCluster :one": db.Cluster{PveVersion: tt.version}}
			}
			e := declaredEndpoint(t, fiber.MethodPost, r.path)
			e.Handler = r.handler(db.New(dbtx))
			app := newHAStandInApp(t, e, pve, dbtx, map[string]bool{r.grant: true})
			req := jsonRequest(http.MethodPost, haRoute(r.path), r.body)
			req.Header.Set("X-Test-User", "yes")
			status, env := send(t, app, req)
			if status != fiber.StatusBadRequest || !strings.Contains(env.Message, "9.0 or newer") {
				t.Errorf("%s, %s: status = %d (%q), want 400 naming PVE 9.0", r.route, tt.name, status, env.Message)
			}
			if len(pve.seen) != tt.wantCalls {
				t.Errorf("%s, %s: Proxmox received %d requests, want %d", r.route, tt.name, len(pve.seen), tt.wantCalls)
			}
		}
	}
}

// A Nexara rule created without "enabled" is created enabled, as the dialog's
// default sends it.
func TestDRSCreateRuleDefaultsToEnabled(t *testing.T) {
	t.Parallel()
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeDRSEndpoint(t, fiber.MethodPost, drsScope+"/rules", cap))
	body := `{"rule_type":"affinity","vm_ids":[100,101],"node_names":[]}`
	if status, env := send(t, app, jsonRequest(http.MethodPost, drsRoute(drsScope+"/rules"), body)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if !cap.params.Bool("enabled") {
		t.Error("an omitted enabled reads false; the rule would be created disabled")
	}
}
