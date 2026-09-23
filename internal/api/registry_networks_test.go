package api

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to
// registry_networks.go that quietly loosened a parameter would show up here.

// networkInterfaceRouteCount is how many endpoints
// registerNetworkInterfaceEndpoints declares. See vmRouteCount in
// registry_vms_test.go for why the registry total is a sum of per-domain
// constants rather than one number.
const networkInterfaceRouteCount = 7

const testIfaceName = "vmbr0"

// networkInterfaceLegacyPermissions is what each handler checked with a
// hand-placed call BEFORE Phase 6e, transcribed from
// `git show HEAD:internal/api/handlers/networks.go` at commit eb888b6: one
// requireClusterPerm per handler, nothing else — no requirePerm, no
// hasClusterPerm, no accessibleClusters.
//
// Every one of them hoists: the permission is a pair of literals in each
// case and :cluster_id is the first path parameter of every route, so
// nothing here is Deferred, Advisory, Public, SelfService or global.
//
// The entry worth writing out is the DELETE, which is the only one of the
// seven on delete:network. revert is manage:network even though it throws a
// pending configuration away, because what it discards is an unapplied edit
// rather than an interface — and that asymmetry is exactly the kind of thing
// a tally exists to freeze.
var networkInterfaceLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/networks":                      "view:network",
	"GET /api/v1/clusters/:cluster_id/networks/:node_name":           "view:network",
	"POST /api/v1/clusters/:cluster_id/networks/:node_name":          "manage:network",
	"PUT /api/v1/clusters/:cluster_id/networks/:node_name/:iface":    "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/networks/:node_name/:iface": "delete:network",
	"POST /api/v1/clusters/:cluster_id/networks/:node_name/apply":    "manage:network",
	"POST /api/v1/clusters/:cluster_id/networks/:node_name/revert":   "manage:network",
}

// declaredNetworkEndpoints returns every declaration listed in tally, keyed
// "METHOD path". Shared by the four NetworkHandler slices, each of which
// passes its own table.
func declaredNetworkEndpoints(t *testing.T, tally map[string]string) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if _, ours := tally[e.Method+" "+e.Path]; ours {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// assertNetworkTally is the check that makes each NetworkHandler slice a
// refactor rather than a change: every hand-placed call moves into
// middleware with the SAME action and the SAME resource.
//
// It compares the rendered permission rather than only the action, because
// checking the action alone would let a route drift onto another resource
// entirely and still pass — which is not hypothetical: the five node
// firewall routes spent six months on a :firewall resource that the
// permission catalogue never contained, and every one of them 403'd.
//
// scope is the scope every route in the table must declare, so that a
// cluster-scoped batch cannot quietly acquire a global route (which would
// refuse every operator holding the grant on exactly the cluster they are
// acting on) or the reverse.
func assertNetworkTally(t *testing.T, tally map[string]string, want int, scope ScopeKind, byPermission map[string]int) {
	t.Helper()
	declared := declaredNetworkEndpoints(t, tally)
	if len(declared) != want {
		t.Fatalf("the registry declares %d of these routes, want %d", len(declared), want)
	}
	if len(tally) != want {
		t.Fatalf("the tally has %d entries, want %d — it must cover every route", len(tally), want)
	}

	got := map[string]int{}
	for key, wantPerm := range tally {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, wantPerm)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check; its permission was a pair of literals",
				key, e.Permissions.Describe())
			continue
		}
		if have := e.Permissions.Describe(); have != wantPerm {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, have, wantPerm)
		}
		if e.Permissions.Check.Scope != scope {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, scope)
		}
		got[wantPerm]++
	}

	for perm, calls := range byPermission {
		if got[perm] != calls {
			t.Errorf("%d routes declare %s, want %d", got[perm], perm, calls)
		}
	}
	for perm, calls := range got {
		if _, listed := byPermission[perm]; !listed {
			t.Errorf("%d routes declare %s, which the per-permission breakdown does not name", calls, perm)
		}
	}
}

func TestNetworkInterfaceRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	// The per-permission breakdown, so a failure says WHICH pair drifted
	// rather than only that the total moved.
	assertNetworkTally(t, networkInterfaceLegacyPermissions, networkInterfaceRouteCount, ScopeCluster, map[string]int{
		"view:network":   2,
		"manage:network": 4,
		"delete:network": 1,
	})
}

// TestEveryDeclaredNetworkInterfaceRouteIsInTheTally is the other direction,
// and it is a SEPARATE test for the reason its node counterpart spells out:
// the tally above opens with two t.Fatalf count checks, and a newly declared
// route trips the first of them — so a loop sharing that body would never
// run in the one situation it exists for.
func TestEveryDeclaredNetworkInterfaceRouteIsInTheTally(t *testing.T) {
	s := newRouteStubServer(t)
	seen := 0
	for _, e := range s.registry.Endpoints() {
		if !strings.HasPrefix(e.Path, networkScope) {
			continue
		}
		seen++
		if _, listed := networkInterfaceLegacyPermissions[e.Method+" "+e.Path]; listed {
			continue
		}
		t.Errorf("%s %s is declared under the network scope but is not in "+
			"networkInterfaceLegacyPermissions — add it to the tally, or the tally stops being a "+
			"review surface", e.Method, e.Path)
	}
	if seen == 0 {
		t.Fatal("no declared route matched the network scope; this guard would pass vacuously")
	}
}

// TestNetworkInterfaceBodyDeclaresEveryProxmoxOptionField is the guard
// against the ONE regression this migration could ship silently.
//
// The layer being replaced bound the body with c.Bind().Body into
// proxmox.CreateNetworkInterfaceParams, so every JSON tag on that struct has
// always been an accepted key — and apischema rejects a key the schema does
// not declare. A field left out of the declaration therefore turns a request
// that has always worked into a 400 that blames the caller, and no fixture
// would reveal it unless the fixture happened to send that field.
//
// Reflecting over the struct rather than listing the names is the point: a
// field ADDED to the Proxmox params in a later release fails here until it is
// declared, instead of quietly becoming unsendable.
func TestNetworkInterfaceBodyDeclaresEveryProxmoxOptionField(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		path   string
		params any
	}{
		{"create", fiber.MethodPost, networkScope + "/:node_name", proxmox.CreateNetworkInterfaceParams{}},
		{"update", fiber.MethodPut, networkScope + "/:node_name/:iface", proxmox.UpdateNetworkInterfaceParams{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			tags := jsonFieldNames(reflect.TypeOf(tt.params))
			if len(tags) == 0 {
				t.Fatal("no JSON tags found; this guard would pass vacuously")
			}
			for _, tag := range tags {
				if _, ok := e.Parameters[tag]; !ok {
					t.Errorf("%s %s does not declare %q, which c.Bind().Body accepted before the "+
						"migration — a caller sending it now gets \"unknown parameter\"",
						tt.method, tt.path, tag)
				}
			}
		})
	}
}

// jsonFieldNames returns the JSON names of every field of a struct,
// descending into embedded structs the way encoding/json does.
func jsonFieldNames(rt reflect.Type) []string {
	var out []string
	for i := range rt.NumField() {
		f := rt.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" {
			out = append(out, jsonFieldNames(f.Type)...)
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		out = append(out, tag)
	}
	return out
}

// TestNetworkInterfaceTypeEnumsDifferBetweenCreateAndUpdate pins the split
// the client layer makes and that a tidier declaration would erase.
//
// The create set is the six types Proxmox's own Create menu offers; the edit
// set is those plus the six an interface can only come to have on its own (a
// physical NIC, an OVS port implied by its bridge). Unifying them would
// either make "eth" creatable — which Proxmox ACCEPTS, writing a junk stanza
// into the pending config and answering 200 — or make a physical NIC
// uneditable.
func TestNetworkInterfaceTypeEnumsDifferBetweenCreateAndUpdate(t *testing.T) {
	create := declaredEndpoint(t, fiber.MethodPost, networkScope+"/:node_name")
	update := declaredEndpoint(t, fiber.MethodPut, networkScope+"/:node_name/:iface")

	creatable := create.Parameters["type"].Enum
	editable := update.Parameters["type"].Enum
	if len(creatable) == 0 || len(editable) == 0 {
		t.Fatal("one of the type parameters carries no enum")
	}
	if slices.Equal(creatable, editable) {
		t.Fatal("the create and update type enums are identical; they are deliberately different sets")
	}
	for _, v := range creatable {
		if !slices.Contains(editable, v) {
			t.Errorf("type %q can be created but not edited", v)
		}
	}
	// The six that exist only on the edit side, spelled out rather than
	// derived, so that dropping one from the client's map is a failure here
	// rather than a silent narrowing of what an operator may edit.
	for _, v := range []string{"eth", "alias", "OVSPort", "vnet", "fabric", "unknown"} {
		if !slices.Contains(editable, v) {
			t.Errorf("type %q is not editable, but an existing interface can report it", v)
		}
		if slices.Contains(creatable, v) {
			t.Errorf("type %q is creatable, but POST cannot sensibly create one", v)
		}
	}

	// And it bites at the route, not only in the table.
	if _, err := create.Parameters.Validate(networkParamsWith(t, create, map[string]any{"type": "eth"})); err == nil {
		t.Error("the create route accepted type=eth")
	}
	if _, err := update.Parameters.Validate(networkParamsWith(t, update, map[string]any{"type": "eth"})); err != nil {
		t.Errorf("the update route refused type=eth: %v", err)
	}
}

// TestNetworkInterfaceDeleteKeysAreAllowListed covers the parameter that can
// corrupt an interface rather than merely misconfigure it.
//
// Proxmox's `delete` takes RAW option names, so delete=type or delete=iface
// rewrites the stanza without the thing that identifies it. The client
// allow-lists it in validateNetworkInterfaceDeleteKeys; the declaration
// states the same rule one layer earlier and names the offending entry.
func TestNetworkInterfaceDeleteKeysAreAllowListed(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, networkScope+"/:node_name/:iface")
	prop, ok := e.Parameters["delete"]
	if !ok {
		t.Fatal("the update route declares no delete parameter")
	}
	if prop.Items == nil || len(prop.Items.Enum) == 0 {
		t.Fatal("delete carries no items enum, so any option name would pass")
	}
	if !slices.Equal(prop.Items.Enum, proxmox.DeletableNetworkInterfaceKeys) {
		t.Errorf("the delete enum is %v, want the client's own allow-list %v",
			prop.Items.Enum, proxmox.DeletableNetworkInterfaceKeys)
	}

	for _, tt := range []struct {
		name  string
		value any
		ok    bool
	}{
		{"a clearable setting", []any{"gateway"}, true},
		{"several", []any{"gateway", "cidr", "mtu"}, true},
		{"none", []any{}, true},
		{"the type", []any{"type"}, false},
		{"the interface name", []any{"iface"}, false},
		{"a traversal", []any{".."}, false},
		{"one good and one bad", []any{"gateway", "iface"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.Parameters.Validate(networkParamsWith(t, e, map[string]any{"delete": tt.value}))
			if tt.ok && err != nil {
				t.Fatalf("delete=%v was refused: %v", tt.value, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("delete=%v was accepted", tt.value)
			}
			if !tt.ok && !strings.HasPrefix(err.Error(), "delete[") {
				t.Errorf("delete=%v was refused, but the message blames something else: %v", tt.value, err)
			}
		})
	}
}

// TestNetworkInterfaceSentinelsSurvive is the case class the brief for this
// migration called out, and the one a format would have broken.
//
// Three values have to keep meaning what they always meant:
//
//   - autostart 0 — the explicit off, which Proxmox's own checkbox sends as
//     an unchecked value and which is ALWAYS written to the form.
//   - mtu 0, ovs_tag 0, vlan-id 0 — "not set", which
//     networkIfaceOptionsToForm spells by omitting the key. A Minimum of
//     1280 or 1 would 400 a caller who has been spelling it that way since
//     the endpoint shipped.
//   - an empty string on any of the text settings — also "not set", dropped
//     by the same function. Every registered apischema format rejects "", so
//     none of them carries one.
func TestNetworkInterfaceSentinelsSurvive(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, networkScope+"/:node_name")
	for _, tt := range []struct {
		name  string
		value map[string]any
	}{
		{"autostart off", map[string]any{"autostart": 0}},
		{"autostart on", map[string]any{"autostart": 1}},
		{"an unset mtu", map[string]any{"mtu": 0}},
		{"an unset ovs tag", map[string]any{"ovs_tag": 0}},
		{"an unset vlan id", map[string]any{"vlan-id": 0}},
		{"an empty gateway", map[string]any{"gateway": ""}},
		{"an empty cidr", map[string]any{"cidr": ""}},
		{"an empty bond mode", map[string]any{"bond_mode": ""}},
		{"an empty ovs bridge", map[string]any{"ovs_bridge": ""}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.Parameters.Validate(networkParamsWith(t, e, tt.value)); err != nil {
				t.Fatalf("%v was refused: %v", tt.value, err)
			}
		})
	}

	// And the value the handler reads back for an OMITTED autostart is the
	// explicit off, not "absent" — because the form always carries it.
	params, err := e.Parameters.Validate(networkParamsWith(t, e, nil))
	if err != nil {
		t.Fatalf("the known-good parameter set was rejected: %v", err)
	}
	if params.Bool("autostart") {
		t.Error("an omitted autostart reads back as true; the form would then enable it")
	}
	if got := params.Int("mtu"); got != 0 {
		t.Errorf("an omitted mtu reads back as %d, want 0 (the \"not set\" sentinel)", got)
	}
}

// TestNetworkInterfaceNumericBoundsMatchTheClientGuard pins the CEILINGS
// against the client's own constants, so that raising one in
// internal/proxmox and not the other is a failure rather than a route that
// refuses a value Proxmox accepts.
//
// Only the ceilings transfer. The MTU FLOOR cannot, because 0 is the "not
// set" sentinel and a Minimum of 1280 would reject it — that half of the
// rule stays in proxmox.validateNetworkInterfaceOptions, and this asserts it
// is still there rather than assuming it.
func TestNetworkInterfaceNumericBoundsMatchTheClientGuard(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, networkScope+"/:node_name")
	for _, tt := range []struct {
		param string
		max   float64
	}{
		{"mtu", float64(proxmox.MaxInterfaceMTU)},
		{"ovs_tag", float64(proxmox.MaxVLANTag)},
		{"vlan-id", float64(proxmox.MaxVLANTag)},
	} {
		t.Run(tt.param, func(t *testing.T) {
			prop := e.Parameters[tt.param]
			if prop.Minimum == nil || *prop.Minimum != 0 {
				t.Errorf("%s has minimum %v, want 0 — 0 is the \"not set\" sentinel", tt.param, prop.Minimum)
			}
			if prop.Maximum == nil || *prop.Maximum != tt.max {
				t.Errorf("%s has maximum %v, want %v (the client's own ceiling)", tt.param, prop.Maximum, tt.max)
			}
			if _, err := e.Parameters.Validate(networkParamsWith(t, e, map[string]any{tt.param: int(tt.max) + 1})); err == nil {
				t.Errorf("%s accepted a value above its ceiling", tt.param)
			}
			if _, err := e.Parameters.Validate(networkParamsWith(t, e, map[string]any{tt.param: -1})); err == nil {
				t.Errorf("%s accepted a negative value", tt.param)
			}
		})
	}

	// The floor the schema deliberately does not state is still enforced one
	// layer down, in proxmox.validateNetworkInterfaceOptions — which both the
	// create and the update path call, and which
	// TestValidateNetworkInterfaceOptions in
	// internal/proxmox/client_network_test.go covers ("mtu below bound",
	// "vlan tag above range", "ovs tag negative"). It stays unexported so it
	// cannot become the opt-in shape a second caller forgets.
}

// TestIfaceParamRefusesATraversalSegment covers the one value in this slice
// that becomes a Proxmox PATH segment.
//
// proxmox.DeleteNetworkInterface already guards it with validatePathSegment,
// and its own comment says why: pveproxy would read iface="." as an interface
// name and refuse it, but a normalising proxy in front of it collapses the
// path to /nodes/{node}/network, which is Proxmox's REVERT endpoint — gated in
// Nexara behind a different permission than the delete — and ".." one level
// further, onto the node. The declaration states the same rule one layer
// earlier and names the field.
func TestIfaceParamRefusesATraversalSegment(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodPut, networkScope + "/:node_name/:iface"},
		{fiber.MethodDelete, networkScope + "/:node_name/:iface"},
	} {
		t.Run(tt.method, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			if e.Parameters["iface"].Pattern == "" {
				t.Fatal("iface carries no pattern, so \".\" and \"..\" pass the declaration and are refused " +
					"only by the proxmox client's guard, one layer late")
			}
			if _, err := e.Parameters.Validate(networkParamsWith(t, e, nil)); err != nil {
				t.Fatalf("the known-good parameter set was rejected: %v", err)
			}
			for _, bad := range []string{".", "..", "a/b", `a\b`, "-leading", "", "vmbr0 "} {
				_, err := e.Parameters.Validate(networkParamsWith(t, e, map[string]any{"iface": bad}))
				if err == nil {
					t.Errorf("iface accepted %q", bad)
					continue
				}
				if !strings.HasPrefix(err.Error(), "iface:") {
					t.Errorf("iface = %q was rejected, but the message blames something else: %v", bad, err)
				}
			}
			for _, good := range []string{"vmbr0", "bond0", "vmbr0.100", "eth0:0", "enp1s0"} {
				if _, err := e.Parameters.Validate(networkParamsWith(t, e, map[string]any{"iface": good})); err != nil {
					t.Errorf("iface refused %q, which a node can report: %v", good, err)
				}
			}
		})
	}
}

// TestNetworkInterfaceRoutesRejectAnUndeclaredQueryKey is the behaviour
// change worth a case of its own, because it is the one that can break a
// working caller: an undeclared key is now a 400 rather than being silently
// ignored. It runs end to end so that what it asserts is the status a client
// receives.
func TestNetworkInterfaceRoutesRejectAnUndeclaredQueryKey(t *testing.T) {
	const path = networkScope + "/:node_name"
	cap := &capture{}
	e := declaredEndpoint(t, fiber.MethodGet, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}

	app := newRegistryApp(t, noAuth(), e)
	target := strings.NewReplacer(":cluster_id", testClusterID, ":node_name", testNodeName).Replace(path)

	status, _ := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("the plain request answered %d, want 204", status)
	}
	status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+"?verbose=1", nil))
	if status != fiber.StatusBadRequest {
		t.Fatalf("an undeclared query key answered %d, want 400", status)
	}
	if !strings.Contains(env.Message, "verbose") {
		t.Errorf("the rejection does not name the offending key: %q", env.Message)
	}
}

// networkValidValues is one acceptable value per REQUIRED parameter across
// the whole NetworkHandler registry.
//
// It exists so that a case about one parameter fills in every other required
// one, and therefore fails for the reason under test rather than for a
// missing sibling. A Validate call that supplies a single key always errors,
// which would make every "this value is refused" assertion pass vacuously —
// a shape this repo has been bitten by before.
//
// The names are the placeholder scheme's rather than textbook ones, because
// these are route substitutions and a value that could collide with a real
// object buys nothing and has to be argued about in every review.
var networkValidValues = map[string]any{
	"cluster_id": testClusterID,
	"node_name":  testNodeName,
	"iface":      testIfaceName,
	"type":       "bridge",
}

// networkParamsWith fills every required parameter of e with a known-good
// value, then applies overrides. It FAILS rather than guesses when a required
// parameter has no entry above, so a new one cannot quietly make a caller of
// this helper vacuous.
func networkParamsWith(t *testing.T, e Endpoint, overrides map[string]any) map[string]any {
	t.Helper()
	out := map[string]any{}
	for name, prop := range e.Parameters {
		if prop.Optional {
			continue
		}
		v, ok := networkValidValues[name]
		if !ok {
			t.Fatalf("%s %s: no known-good value for required parameter %q; add one to networkValidValues",
				e.Method, e.Path, name)
		}
		out[name] = v
	}
	maps.Copy(out, overrides)
	return out
}

// requireDeclaredProperty is the shared "this parameter exists and looks like
// this" assertion the firewall and SDN slices reuse.
func requireDeclaredProperty(t *testing.T, e Endpoint, name string) apischema.Property {
	t.Helper()
	prop, ok := e.Parameters[name]
	if !ok {
		t.Fatalf("%s %s declares no %q parameter", e.Method, e.Path, name)
	}
	return prop
}
