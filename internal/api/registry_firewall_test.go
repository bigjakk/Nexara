package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// firewallRouteCount is how many endpoints registerFirewallEndpoints
// declares. See vmRouteCount in registry_vms_test.go for why the registry
// total is a sum of per-domain constants rather than one number.
const firewallRouteCount = 28

// firewallLegacyPermissions is what each handler checked with a hand-placed
// call BEFORE Phase 6e, transcribed from
// `git show HEAD:internal/api/handlers/networks.go` at commit eb888b6: one
// requireClusterPerm per handler, nothing else.
//
// Two shapes in here are the reason it is written out in full rather than
// summarised:
//
//   - The resource is :network on all twenty-eight, even though the node
//     firewall routes next door use :firewall for the same verbs on the same
//     kind of object. That split is preserved, not resolved.
//   - Only TWO of the twenty-eight are delete:network — removing a cluster
//     rule and removing a guest rule. Deleting an alias, an IP set, an IP set
//     entry, a security group or a security-group rule is manage:network.
//     There is no rule behind that, so the table is the record of it.
var firewallLegacyPermissions = map[string]string{
	// Cluster rules and options.
	"GET /api/v1/clusters/:cluster_id/firewall/rules":         "view:network",
	"POST /api/v1/clusters/:cluster_id/firewall/rules":        "manage:network",
	"PUT /api/v1/clusters/:cluster_id/firewall/rules/:pos":    "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/firewall/rules/:pos": "delete:network",
	"GET /api/v1/clusters/:cluster_id/firewall/options":       "view:network",
	"PUT /api/v1/clusters/:cluster_id/firewall/options":       "manage:network",

	// Per-guest rules.
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules":         "view:network",
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules":        "manage:network",
	"PUT /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos":    "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos": "delete:network",

	// Aliases.
	"GET /api/v1/clusters/:cluster_id/firewall/aliases":          "view:network",
	"POST /api/v1/clusters/:cluster_id/firewall/aliases":         "manage:network",
	"PUT /api/v1/clusters/:cluster_id/firewall/aliases/:name":    "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/firewall/aliases/:name": "manage:network",

	// IP sets.
	"GET /api/v1/clusters/:cluster_id/firewall/ipset":                        "view:network",
	"POST /api/v1/clusters/:cluster_id/firewall/ipset":                       "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/firewall/ipset/:name":               "manage:network",
	"GET /api/v1/clusters/:cluster_id/firewall/ipset/:name/entries":          "view:network",
	"POST /api/v1/clusters/:cluster_id/firewall/ipset/:name/entries":         "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/firewall/ipset/:name/entries/:cidr": "manage:network",

	// Security groups.
	"GET /api/v1/clusters/:cluster_id/firewall/groups":                      "view:network",
	"POST /api/v1/clusters/:cluster_id/firewall/groups":                     "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/firewall/groups/:group":            "manage:network",
	"GET /api/v1/clusters/:cluster_id/firewall/groups/:group/rules":         "view:network",
	"POST /api/v1/clusters/:cluster_id/firewall/groups/:group/rules":        "manage:network",
	"PUT /api/v1/clusters/:cluster_id/firewall/groups/:group/rules/:pos":    "manage:network",
	"DELETE /api/v1/clusters/:cluster_id/firewall/groups/:group/rules/:pos": "manage:network",

	// Log.
	"GET /api/v1/clusters/:cluster_id/firewall/log": "view:network",
}

func TestFirewallRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	assertNetworkTally(t, firewallLegacyPermissions, firewallRouteCount, ScopeCluster, map[string]int{
		"view:network":   9,
		"manage:network": 17,
		"delete:network": 2,
	})
}

// TestEveryDeclaredFirewallRouteIsInTheTally is the other direction, and a
// SEPARATE test for the reason its node counterpart spells out: the tally
// above opens with two t.Fatalf count checks, so a loop sharing that body
// would never run in the one situation it exists for.
//
// It sweeps BOTH prefixes this slice owns, because the per-guest rules hang
// off /vms/:vm_id rather than off /firewall.
func TestEveryDeclaredFirewallRouteIsInTheTally(t *testing.T) {
	s := newRouteStubServer(t)
	seen := 0
	for _, e := range s.registry.Endpoints() {
		underFirewall := strings.HasPrefix(e.Path, firewallScope+"/")
		underGuest := strings.HasPrefix(e.Path, guestFirewallScope+"/")
		if !underFirewall && !underGuest {
			continue
		}
		seen++
		if _, listed := firewallLegacyPermissions[e.Method+" "+e.Path]; listed {
			continue
		}
		t.Errorf("%s %s is declared under a firewall scope but is not in firewallLegacyPermissions — "+
			"add it to the tally, or the tally stops being a review surface", e.Method, e.Path)
	}
	if seen == 0 {
		t.Fatal("no declared route matched a firewall scope; this guard would pass vacuously")
	}
}

// TestFirewallRuleRequiredSetMatchesEachHandler is the check the brief for
// this migration asked for by name: pin each body's REQUIRED set against
// what the prior handler enforced, not against what would be consistent.
//
// The three rule creates disagree, and the disagreement is real:
// CreateClusterFirewallRule and CreateNodeFirewallRule both answered 400 for
// an empty type or action, and CreateSecurityGroupRule answered nothing at
// all. Every update route required neither. A fixture could never reveal
// this, because a fixture that bothers to build a rule always sends both.
func TestFirewallRuleRequiredSetMatchesEachHandler(t *testing.T) {
	for _, tt := range []struct {
		name     string
		method   string
		path     string
		required bool
	}{
		{"cluster create", fiber.MethodPost, firewallScope + "/rules", true},
		{"cluster update", fiber.MethodPut, firewallScope + "/rules/:pos", false},
		{"guest create", fiber.MethodPost, guestFirewallScope + "/rules", true},
		{"guest update", fiber.MethodPut, guestFirewallScope + "/rules/:pos", false},
		// The one that is NOT like its siblings.
		{"security group create", fiber.MethodPost, firewallScope + "/groups/:group/rules", false},
		{"security group update", fiber.MethodPut, firewallScope + "/groups/:group/rules/:pos", false},
		// The node routes share firewallRuleBody, so they are driven here
		// too: a change to the shared helper that flipped them would
		// otherwise only be caught by a test in another file.
		{"node create", fiber.MethodPost, nodeScope + "/:node_name/firewall/rules", true},
		{"node update", fiber.MethodPut, nodeScope + "/:node_name/firewall/rules/:pos", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			for _, field := range []string{"type", "action"} {
				prop := requireDeclaredProperty(t, e, field)
				if prop.Optional == tt.required {
					t.Errorf("%s is optional=%v, want optional=%v", field, prop.Optional, !tt.required)
				}
				// Required is not the whole of what the create handlers
				// enforced: they refused an EMPTY type or action as well,
				// and apischema's required check only refuses an absent
				// key. So the value itself is driven through the schema — a
				// Property-shape assertion alone could not tell "required"
				// from "required and non-empty". The update spelling takes ""
				// to mean "keep the current value", and must still accept it.
				_, err := e.Parameters.Validate(firewallParamsWith(t, e, map[string]any{field: ""}))
				if tt.required && err == nil {
					t.Errorf("an empty %s was accepted; the create handler answered 400 for it", field)
				}
				if tt.required && err != nil && !strings.HasPrefix(err.Error(), field) {
					t.Errorf("an empty %s was refused as %q; the refusal should name %s", field, err, field)
				}
				if !tt.required && err != nil {
					t.Errorf("an empty %s was refused (%v); this route has always accepted one — an "+
						"update keeps the current value, and the security-group create never checked it",
						field, err)
				}
			}
			// Every other rule field is optional on every route.
			for _, field := range []string{"source", "dest", "sport", "dport", "proto", "macro", "comment", "log", "iface", "enable"} {
				if !requireDeclaredProperty(t, e, field).Optional {
					t.Errorf("%s is required, but no handler ever demanded it", field)
				}
			}
		})
	}
}

// TestFirewallAliasRenameKeepsTheEmptySentinel pins that the alias update's
// schema admits the empty rename it always took: proxmox.UpdateFirewallAlias
// sends rename only when it is non-empty, so an empty one has always kept the
// alias's name. The bare object-name rule refuses "", which turned that
// request into a 400; the declaration carries the -or-empty variant instead,
// and still refuses a traversal. The meaning half — that "" really leaves the
// name alone — is the client's, and TestUpdateFirewallAliasOmitsAnEmptyRename
// in internal/proxmox pins it.
//
// It also holds this call site of pveObjectNameOrEmptyParam: the pattern must
// be the catalogued rule and the MaxLength must survive, because a Pattern
// reassigned to a literal after the helper returns is invisible to the guards
// that read the source (see registry_rule_reference_ratchet_test.go).
func TestFirewallAliasRenameKeepsTheEmptySentinel(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, firewallScope+"/aliases/:name")
	prop := e.Parameters["rename"]
	if want := apischema.Rule("pve-object-id-or-empty"); prop.Pattern != want {
		t.Errorf("rename declares pattern %q, want the catalogued pve-object-id-or-empty %q", prop.Pattern, want)
	}
	if prop.MaxLength == nil {
		t.Error("rename declares no MaxLength, want 64 — the bound pveObjectNameParam sets")
	} else if *prop.MaxLength != 64 {
		t.Errorf("rename declares MaxLength %d, want 64 — the bound pveObjectNameParam sets", *prop.MaxLength)
	}
	for _, tt := range []struct {
		rename string
		ok     bool
	}{
		{"", true},
		{"storealias2", true},
		{"..", false},
	} {
		_, err := e.Parameters.Validate(firewallParamsWith(t, e, map[string]any{"rename": tt.rename}))
		if (err == nil) != tt.ok {
			t.Errorf("rename %q: err = %v, want accepted=%v", tt.rename, err, tt.ok)
		}
	}
}

// TestFirewallOptionsKeepEnableTriState is the case that would have broken
// the cluster firewall silently.
//
// proxmox.FirewallOptions.Enable is a *int and firewallOptionsToForm sends
// the key only when it is non-nil. The options card sends
// `{"policy_in": "DROP"}` on its own — so a Default of 0 on enable would
// write enable=0 alongside it and turn the cluster firewall OFF on a policy
// change. The declaration therefore carries no default, and Has reports the
// difference.
func TestFirewallOptionsKeepEnableTriState(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, firewallScope+"/options")
	enable := requireDeclaredProperty(t, e, "enable")
	if !enable.Optional {
		t.Error("enable is required; the options card sends a policy on its own")
	}
	if enable.Default != nil {
		t.Errorf("enable declares default %v; a default would write enable=0 on every policy-only save", enable.Default)
	}

	policyOnly, err := e.Parameters.Validate(map[string]any{"cluster_id": testClusterID, "policy_in": "DROP"})
	if err != nil {
		t.Fatalf("a policy-only body was rejected: %v", err)
	}
	if policyOnly.Has("enable") {
		t.Error("a body without enable reports it as supplied; the firewall would be written off")
	}
	explicitOff, err := e.Parameters.Validate(map[string]any{"cluster_id": testClusterID, "enable": 0})
	if err != nil {
		t.Fatalf("an explicit enable=0 was rejected: %v", err)
	}
	if !explicitOff.Has("enable") || explicitOff.Int("enable") != 0 {
		t.Error("an explicit enable=0 does not read back as a supplied zero")
	}

	// And every field is optional, because the handler required none.
	for name, prop := range e.Parameters {
		if name == "cluster_id" {
			continue
		}
		if !prop.Optional {
			t.Errorf("%s is required, but SetFirewallOptions required nothing", name)
		}
	}
}

// TestIPSetEntryNoMatchKeepsItsTriState is the same shape one route over:
// proxmox.FirewallIPSetEntryParams.NoMatch is a *int, so "omitted" and "0"
// have to stay different requests.
func TestIPSetEntryNoMatchKeepsItsTriState(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, firewallScope+"/ipset/:name/entries")
	nomatch := requireDeclaredProperty(t, e, "nomatch")
	if nomatch.Default != nil {
		t.Errorf("nomatch declares default %v; the key is only sent when the caller chose", nomatch.Default)
	}
	params, err := e.Parameters.Validate(map[string]any{
		"cluster_id": testClusterID, "name": "storeset", "cidr": "192.0.2.0/24",
	})
	if err != nil {
		t.Fatalf("a body without nomatch was rejected: %v", err)
	}
	if params.Has("nomatch") {
		t.Error("a body without nomatch reports it as supplied")
	}
}

// TestFirewallPathNamesRefuseATraversalSegment covers every caller-supplied
// value in this slice that becomes a Proxmox PATH segment.
//
// Only DeleteFirewallIPSetEntry — and UpdateFirewallIPSetEntry, which no route
// reaches — is guarded at the client as well: each checks the set name with
// proxmox.validatePathSegment and the entry's cidr with
// validatePathSegmentAllowingSlash (client_firewall.go). For the rest,
// url.PathEscape leaves "." and ".." alone, so the request resolves upward
// once pveproxy normalises it: a "." segment drops out and a ".." takes the
// segment before it along. As the last segment that lands on the PARENT
// collection or on /cluster/firewall above it — POST .../ipset/. reaches the
// endpoint that creates IP sets rather than the one that adds an entry, and
// GET .../groups/. lists the groups instead of one group's rules — and where
// the client appends a rule position, PUT or DELETE .../groups/./{pos}
// addresses the group NAMED by the position. Same permission either way, so
// this is a correctness anchor rather than an escalation fix — but an
// operation that silently does something else is not a thing to leave
// declarable.
func TestFirewallPathNamesRefuseATraversalSegment(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		param  string
	}{
		{fiber.MethodPut, firewallScope + "/aliases/:name", "name"},
		{fiber.MethodDelete, firewallScope + "/aliases/:name", "name"},
		{fiber.MethodDelete, firewallScope + "/ipset/:name", "name"},
		{fiber.MethodGet, firewallScope + "/ipset/:name/entries", "name"},
		{fiber.MethodPost, firewallScope + "/ipset/:name/entries", "name"},
		{fiber.MethodDelete, firewallScope + "/ipset/:name/entries/:cidr", "name"},
		{fiber.MethodDelete, firewallScope + "/ipset/:name/entries/:cidr", "cidr"},
		{fiber.MethodDelete, firewallScope + "/groups/:group", "group"},
		{fiber.MethodGet, firewallScope + "/groups/:group/rules", "group"},
		{fiber.MethodPost, firewallScope + "/groups/:group/rules", "group"},
		{fiber.MethodPut, firewallScope + "/groups/:group/rules/:pos", "group"},
		{fiber.MethodDelete, firewallScope + "/groups/:group/rules/:pos", "group"},
	} {
		t.Run(tt.method+" "+tt.path+" "+tt.param, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			prop := requireDeclaredProperty(t, e, tt.param)
			if prop.Pattern == "" {
				t.Fatalf("%q carries no pattern, so nothing keeps \"..\" out of a Proxmox path segment", tt.param)
			}
			if prop.MaxLength == nil {
				t.Errorf("%q carries no length bound", tt.param)
			}
			if _, err := e.Parameters.Validate(firewallParamsWith(t, e, nil)); err != nil {
				t.Fatalf("the known-good parameter set was rejected: %v", err)
			}
			for _, bad := range []string{".", "..", "a/b", `a\b`, "-leading", ""} {
				_, err := e.Parameters.Validate(firewallParamsWith(t, e, map[string]any{tt.param: bad}))
				if err == nil {
					t.Errorf("%q accepted %q", tt.param, bad)
					continue
				}
				if !strings.HasPrefix(err.Error(), tt.param+":") {
					t.Errorf("%q = %q was rejected, but the message blames something else: %v", tt.param, bad, err)
				}
			}
		})
	}
}

// TestIPSetEntryCIDRAcceptsWhatTheClientActuallySends is the counterweight
// to the traversal case above: the pattern has to keep letting through the
// value that reaches this route today.
//
// Fiber runs with UnescapePath false, so c.Params hands back the RAW
// segment — the browser client percent-encodes the slash of a CIDR and the
// handler sees "192.0.2.0%2F24". Borrowing apischema's cidr format would
// refuse exactly that and turn a live call into a 400.
func TestIPSetEntryCIDRAcceptsWhatTheClientActuallySends(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, firewallScope+"/ipset/:name/entries/:cidr")
	for _, good := range []string{
		"192.0.2.10",
		"192.0.2.0%2F24",
		"2001:db8::1",
		"%3A%3A1%2F128",
	} {
		if _, err := e.Parameters.Validate(firewallParamsWith(t, e, map[string]any{"cidr": good})); err != nil {
			t.Errorf("cidr refused %q, which the client sends today: %v", good, err)
		}
	}
	if requireDeclaredProperty(t, e, "cidr").Format != "" {
		t.Error("cidr carries a format; every registered one rejects the percent-encoded form the client sends")
	}
}

// TestGuestFirewallVMIDIsTheProxmoxVMID records the inconsistency this slice
// inherits rather than introduces.
//
// Every other /clusters/:cluster_id/vms/:vm_id route spells :vm_id as
// Nexara's row uuid. These four have always spelled it as the Proxmox VMID —
// the handlers read it with strconv.Atoi. The declaration says which, so a
// caller no longer has to find out by sending the wrong one; this pins that
// it stayed the VMID, because silently switching it to a uuid would 404
// every existing caller.
func TestGuestFirewallVMIDIsTheProxmoxVMID(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
	}{
		{fiber.MethodGet, guestFirewallScope + "/rules"},
		{fiber.MethodPost, guestFirewallScope + "/rules"},
		{fiber.MethodPut, guestFirewallScope + "/rules/:pos"},
		{fiber.MethodDelete, guestFirewallScope + "/rules/:pos"},
	} {
		t.Run(tt.method, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			prop := requireDeclaredProperty(t, e, "vm_id")
			if prop.Type != apischema.Integer {
				t.Fatalf("vm_id is declared as %s, want an integer — the handler reads it as a VMID", prop.Type)
			}
			if prop.Format != "" {
				t.Errorf("vm_id carries format %q; the uuid format would 404 every existing caller", prop.Format)
			}
			if _, err := e.Parameters.Validate(firewallParamsWith(t, e, map[string]any{"vm_id": testClusterID})); err == nil {
				t.Error("vm_id accepted a uuid, which the handler cannot use")
			}
			if _, err := e.Parameters.Validate(firewallParamsWith(t, e, map[string]any{"vm_id": 101})); err != nil {
				t.Errorf("vm_id refused a VMID: %v", err)
			}
		})
	}
}

// TestFirewallLogParametersAreDeclared covers the three query keys that were
// read with c.Query and are now declared — an undeclared one 400s, so all
// three have to be there.
func TestFirewallLogParametersAreDeclared(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodGet, firewallScope+"/log")

	node := requireDeclaredProperty(t, e, "node")
	if node.Optional {
		t.Error("node is optional; the handler answered 400 for an absent one")
	}
	if node.Format != "node-name" {
		t.Errorf("node declares format %q, want node-name", node.Format)
	}

	limit := requireDeclaredProperty(t, e, "limit")
	if limit.Default != 500 {
		t.Errorf("limit defaults to %v, want 500 — the value c.Query substituted", limit.Default)
	}
	if limit.Minimum == nil || *limit.Minimum != 0 {
		t.Error("limit has no floor of 0; 0 means \"no limit\" to Proxmox and a negative one used to pass through")
	}
	// Deliberately unbounded above: unlike the NODE firewall log handler,
	// this one never clamped, so a ceiling here would be a new rejection.
	if limit.Maximum != nil {
		t.Errorf("limit declares a maximum of %v, which this handler never enforced", *limit.Maximum)
	}

	start := requireDeclaredProperty(t, e, "start")
	if start.Minimum == nil || *start.Minimum != 0 {
		t.Error("start has no floor of 0; a negative offset used to reach Proxmox")
	}

	// An empty ?node= is refused too, which c.Query could not tell from
	// absent.
	if _, err := e.Parameters.Validate(map[string]any{"cluster_id": testClusterID, "node": ""}); err == nil {
		t.Error("an empty node was accepted")
	}
}

// TestFirewallCreateBodiesDropFieldsTheHandlerIgnored pins the two body
// fields that proxmox's params struct carries and the handler never read.
//
// c.Bind().Body accepted both and silently discarded them, which is the
// failure the registry exists to remove: a create carrying `rename` did
// nothing, and an alias update carrying `name` acted on the alias in the
// PATH instead. Both are now "unknown parameter".
func TestFirewallCreateBodiesDropFieldsTheHandlerIgnored(t *testing.T) {
	create := declaredEndpoint(t, fiber.MethodPost, firewallScope+"/aliases")
	if _, declared := create.Parameters["rename"]; declared {
		t.Error("the alias create declares rename, which CreateFirewallAlias never sends")
	}
	update := declaredEndpoint(t, fiber.MethodPut, firewallScope+"/aliases/:name")
	if _, declared := update.Parameters["rename"]; !declared {
		t.Error("the alias update does not declare rename, which UpdateFirewallAlias does send")
	}
	// `name` on the update resolves to the PATH, never the body — so it must
	// not be a body field that could disagree with it.
	if got := apischema.ResolveSource("name", update.Parameters["name"], update.Method, pathParamNames(update.Path)); got != apischema.SourcePath {
		t.Errorf("the alias update reads name from %q, want the path", got)
	}
}

// firewallValidValues is one acceptable value per REQUIRED parameter across
// this slice. See networkValidValues for why a helper like this has to fail
// rather than guess.
var firewallValidValues = map[string]any{
	"cluster_id": testClusterID,
	"node":       testNodeName,
	"node_name":  testNodeName,
	"vm_id":      101,
	"pos":        0,
	"type":       "in",
	"action":     "ACCEPT",
	"name":       "storeset",
	"group":      "storegroup",
	"cidr":       "192.0.2.0%2F24",
}

// firewallParamsWith fills every required parameter of e with a known-good
// value, then applies overrides.
//
// The cidr entry is the percent-encoded PATH form, which is what the delete
// route takes; the two BODY routes that also spell a parameter "cidr" want a
// plain address, so they get one substituted here rather than sharing the
// path spelling.
func firewallParamsWith(t *testing.T, e Endpoint, overrides map[string]any) map[string]any {
	t.Helper()
	pathParams := pathParamNames(e.Path)
	out := map[string]any{}
	for name, prop := range e.Parameters {
		if prop.Optional {
			continue
		}
		v, ok := firewallValidValues[name]
		if !ok {
			t.Fatalf("%s %s: no known-good value for required parameter %q; add one to firewallValidValues",
				e.Method, e.Path, name)
		}
		if name == "cidr" && !slices.Contains(pathParams, name) {
			v = "192.0.2.0/24"
		}
		out[name] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// TestFirewallIntegerPathParamsArriveThroughFiber drives the two path
// parameters that replaced a hand-rolled strconv.Atoi, end to end, so that
// what is asserted is what a client actually gets.
//
// :pos and :vm_id are now declared as INTEGERS in a path segment, which is a
// coercion the schema tests above exercise through Validate but not through
// Fiber. The old parse caught "abc" and never "-1"; both are a 400 naming
// the parameter now, and the handler does not run for either.
func TestFirewallIntegerPathParamsArriveThroughFiber(t *testing.T) {
	const path = guestFirewallScope + "/rules/:pos"
	for _, tt := range []struct {
		name  string
		vmID  string
		pos   string
		want  int
		reads [2]int64
	}{
		{name: "a VMID and a position", vmID: "101", pos: "3", want: fiber.StatusNoContent, reads: [2]int64{101, 3}},
		{name: "position zero", vmID: "101", pos: "0", want: fiber.StatusNoContent, reads: [2]int64{101, 0}},
		{name: "a negative position", vmID: "101", pos: "-1", want: fiber.StatusBadRequest},
		{name: "a non-numeric position", vmID: "101", pos: "abc", want: fiber.StatusBadRequest},
		{name: "a uuid where the VMID goes", vmID: testClusterID, pos: "0", want: fiber.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			e := declaredEndpoint(t, fiber.MethodPut, path)
			e.Handler = cap.handler()
			e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), e)

			target := strings.NewReplacer(
				":cluster_id", testClusterID, ":vm_id", tt.vmID, ":pos", tt.pos,
			).Replace(path)
			status, env := send(t, app, jsonRequest(http.MethodPut, target, `{}`))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.want != fiber.StatusNoContent {
				if cap.called {
					t.Error("the handler ran for a value the schema rejected")
				}
				return
			}
			if got := cap.params.Int("vm_id"); got != tt.reads[0] {
				t.Errorf("vm_id = %d, want %d", got, tt.reads[0])
			}
			if got := cap.params.Int("pos"); got != tt.reads[1] {
				t.Errorf("pos = %d, want %d", got, tt.reads[1])
			}
		})
	}
}

// TestIPSetEntryCIDRSurvivesFiberUnchanged is the end-to-end half of
// TestIPSetEntryCIDRAcceptsWhatTheClientActuallySends, and it is the case
// this slice would most easily have got wrong.
//
// Fiber runs with UnescapePath at its default of false, so c.Params hands
// back the segment exactly as it arrived — the client's encodeURIComponent
// output, slash and all. This drives a real request to prove the declared
// pattern accepts what the browser sends rather than what a CIDR looks like
// after decoding.
func TestIPSetEntryCIDRSurvivesFiberUnchanged(t *testing.T) {
	const path = firewallScope + "/ipset/:name/entries/:cidr"
	cap := &capture{}
	e := declaredEndpoint(t, fiber.MethodDelete, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), e)

	target := "/api/v1/clusters/" + testClusterID + "/firewall/ipset/storeset/entries/192.0.2.0%2F24"
	status, env := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("cidr"); got != "192.0.2.0%2F24" {
		t.Errorf("cidr = %q, want the raw percent-encoded segment — Fiber does not unescape it", got)
	}

	// And a traversal segment is still refused at the route.
	cap.called = false
	bad := "/api/v1/clusters/" + testClusterID + "/firewall/ipset/storeset/entries/.."
	status, _ = send(t, app, httptest.NewRequest(http.MethodDelete, bad, nil))
	if status == fiber.StatusNoContent {
		t.Error("a \"..\" entry id reached the handler")
	}
	if cap.called {
		t.Error("the handler ran for a traversal segment")
	}
}
