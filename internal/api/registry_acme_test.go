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

// These tests drive the REAL declarations — the ones setupRoutes mounts — rather
// than a fixture shaped like them, so a change to registry_acme.go that quietly
// loosened a parameter would show up here.

// acmeRouteCount is how many endpoints registerACMEEndpoints declares. It is ALL
// 18 of ACMEHandler's routes; nothing in this domain was left legacy. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum of
// per-domain constants.
const acmeRouteCount = 18

const (
	testACMEAccountName = "default"
	testACMEPluginID    = "dns-example"
	testACMENodeName    = "pve-01"
)

func acmeRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":name", testACMEAccountName,
		":plugin_id", testACMEPluginID,
		":node", testACMENodeName,
	).Replace(path)
}

// acmeLegacyPermissions is what each handler checked with a hand-placed
// requireClusterPerm call BEFORE Phase 6h, transcribed from
// `git show HEAD:internal/api/handlers/acme.go` at commit 3ca85e9.
//
// Every entry is one call and every call hoists: each handler resolved the
// cluster from its own path with clusterIDFromParam and then made exactly one
// static requireClusterPerm(c, action, "certificate", clusterID). No route in
// this domain made two calls, deferred a decision to a loaded row, or filtered a
// listing, which is why this table is an action per route rather than the
// shape/calls/hoisted triple alertLegacyPermissions carries.
var acmeLegacyPermissions = map[string]string{
	"GET /api/v1/clusters/:cluster_id/acme/accounts":                      "view",
	"POST /api/v1/clusters/:cluster_id/acme/accounts":                     "manage",
	"GET /api/v1/clusters/:cluster_id/acme/accounts/:name":                "view",
	"PUT /api/v1/clusters/:cluster_id/acme/accounts/:name":                "manage",
	"DELETE /api/v1/clusters/:cluster_id/acme/accounts/:name":             "manage",
	"GET /api/v1/clusters/:cluster_id/acme/plugins":                       "view",
	"POST /api/v1/clusters/:cluster_id/acme/plugins":                      "manage",
	"PUT /api/v1/clusters/:cluster_id/acme/plugins/:plugin_id":            "manage",
	"DELETE /api/v1/clusters/:cluster_id/acme/plugins/:plugin_id":         "manage",
	"GET /api/v1/clusters/:cluster_id/acme/challenge-schema":              "view",
	"GET /api/v1/clusters/:cluster_id/acme/directories":                   "view",
	"GET /api/v1/clusters/:cluster_id/acme/tos":                           "view",
	"GET /api/v1/clusters/:cluster_id/nodes/:node/acme-config":            "view",
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/acme-config":            "manage",
	"GET /api/v1/clusters/:cluster_id/nodes/:node/certificates":           "view",
	"POST /api/v1/clusters/:cluster_id/nodes/:node/certificates/order":    "manage",
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/certificates/renew":     "manage",
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node/certificates/revoke": "manage",
}

// declaredACMEEndpoints returns every declaration in this domain, keyed
// "METHOD path".
//
// The membership test is STRUCTURAL — the path prefix, or the resource the
// declaration names — and deliberately not "is it in acmeLegacyPermissions".
// Filtering on the tally's own keys is how the reverse check at the bottom of
// TestACMERoutesDeclareTheSamePermissionTheyEnforced becomes vacuous: every key
// would be in the table by construction, so a nineteenth ACME route added
// alongside a bumped acmeRouteCount would drop out of the tally with nothing
// failing. That is the shape this repo has been bitten by before — a check whose
// input cannot express failure.
//
// Two clauses rather than one, because the domain spans two prefixes: twelve
// routes hang off /acme, and the six per-node certificate and acme-config routes
// hang off /nodes/:node, which is shared with NodeHandler's 38 and the
// rolling-update package preview. The resource clause is what catches those six,
// and it catches a future route wherever it is mounted.
func declaredACMEEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, acmeScope+"/") ||
			strings.Contains(e.Permissions.Describe(), acmeCertificateResource) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestACMERoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// this migration a refactor rather than a change: 18 hand-placed calls in, 18
// declared cluster-scoped Checks out, none kept in a handler.
func TestACMERoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredACMEEndpoints(t)
	if len(declared) != acmeRouteCount {
		t.Fatalf("the registry declares %d ACME routes, want %d", len(declared), acmeRouteCount)
	}
	if len(acmeLegacyPermissions) != acmeRouteCount {
		t.Fatalf("acmeLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(acmeLegacyPermissions), acmeRouteCount)
	}

	view, manage := 0, 0
	for key, action := range acmeLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s:%s before the migration but is not declared in the registry",
				key, action, acmeCertificateResource)
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check", key, e.Permissions.Describe())
			continue
		}
		if want := action + ":" + acmeCertificateResource; e.Permissions.Describe() != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration",
				key, e.Permissions.Describe(), want)
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, ScopeCluster)
		}
		switch action {
		case "view":
			view++
		case "manage":
			manage++
		}
	}
	// The split is stated rather than left to be counted, because the one
	// mistake worth catching here is a WRITE declared view: ordering a
	// certificate restarts pveproxy, and revoking one drops every client that
	// pinned it.
	if view != 8 || manage != 10 {
		t.Errorf("the tally hoists %d view and %d manage checks, want 8 / 10", view, manage)
	}

	for key := range declared {
		if _, listed := acmeLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in acmeLegacyPermissions — a new ACME route must be "+
				"added to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestACMERoutesAreGatedByTheirDeclaration proves the 18 hoisted permissions are
// the permissions the routes enforce, end to end.
func TestACMERoutesAreGatedByTheirDeclaration(t *testing.T) {
	for key, action := range acmeLegacyPermissions {
		method, path, _ := strings.Cut(key, " ")
		want := action + ":" + acmeCertificateResource
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			target := acmeRoute(path)

			unrelated := "view:" + acmeCertificateResource
			if action == "view" {
				unrelated = "manage:" + acmeCertificateResource
			}
			app := newRegistryApp(t, stubAuth(map[string]bool{unrelated: true}), gated)
			status, _ := send(t, app, authedRequest(method, target))
			if status != fiber.StatusForbidden {
				t.Fatalf("a caller holding only %q got %d, want 403", unrelated, status)
			}
			if cap.called {
				t.Error("the handler ran for a caller without the declared permission")
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want: true}), gated)
			status, _ = send(t, app, authedRequest(method, target))
			if status == fiber.StatusForbidden {
				t.Fatalf("a caller holding %q got 403", want)
			}

			cap.called = false
			app = newRegistryApp(t, stubAuth(map[string]bool{want: true}), gated)
			status, _ = send(t, app, httptest.NewRequest(method, target, nil))
			if status != fiber.StatusUnauthorized {
				t.Fatalf("an anonymous caller got %d, want 401", status)
			}
			if cap.called {
				t.Error("the handler ran for a request carrying no session")
			}
		})
	}
}

// probeACMEEndpoint is a declared ACME endpoint with its handler swapped for a
// capture and its gate removed, so a parameter test needs neither a database nor
// a session.
func probeACMEEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestACMEPathSegmentsAreAnchored is the traversal guard for this domain, and
// here the declaration is the only layer.
//
// internal/proxmox/client_acme.go builds /cluster/acme/account/<name> and
// /cluster/acme/plugins/<id> by concatenation with url.PathEscape, which escapes
// "/" but leaves "." and ".." alone — and there is NO second validator behind
// it. The access domain has validateUserID and its kin, and the PBS task reads
// have validatePBSTaskUPID; ACME has nothing, like the backup domain's store
// and job ids, which get only a non-empty check
// (TestBackupPathSegmentsAreAnchored). The declaration is the only anchor, so
// it is the only thing this test can check and the only thing that stops a
// normalising proxy in front of pveproxy from resolving DELETE
// /cluster/acme/account/. onto the account collection, or ".." onto
// /cluster/acme above it. (pveproxy itself would read either as an account
// name; see proxmox.validatePathSegment.)
func TestACMEPathSegmentsAreAnchored(t *testing.T) {
	traversals := []string{".", ".."}

	for _, seg := range []struct {
		param string
		route string
		good  []string
	}{
		{"name", acmeScope + "/accounts/:name", []string{"default", "staging", "le-prod", "acct.1"}},
		{"plugin_id", acmeScope + "/plugins/:plugin_id", []string{"dns-example", "standalone", "p1"}},
	} {
		t.Run(seg.param, func(t *testing.T) {
			method := fiber.MethodGet
			if seg.param == "plugin_id" {
				// There is no GET for a single plugin; the listing is the read.
				method = fiber.MethodDelete
			}
			e := declaredEndpoint(t, method, seg.route)
			prop, ok := e.Parameters[seg.param]
			if !ok {
				t.Fatalf("%s declares no %q parameter", seg.route, seg.param)
			}
			if prop.Pattern == "" {
				t.Fatalf("%s: %q declares no pattern, and nothing behind it checks one — \".\" and \"..\" "+
					"would pass url.PathEscape untouched, and a normalising proxy in front of pveproxy "+
					"would resolve them onto the parent collection and above it",
					seg.route, seg.param)
			}

			validate := func(value string) error {
				_, err := e.Parameters.Validate(map[string]any{
					"cluster_id": testClusterID,
					seg.param:    value,
				})
				return err
			}
			for _, bad := range traversals {
				if err := validate(bad); err == nil {
					t.Errorf("%q was accepted for %s — it is the traversal segment the anchor exists to refuse",
						bad, seg.param)
				}
			}
			for _, good := range seg.good {
				if err := validate(good); err != nil {
					t.Errorf("%q was refused for %s: %v", good, seg.param, err)
				}
			}
		})
	}

	// And end to end, on the escaped spelling that reaches routing intact.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeACMEEndpoint(t, fiber.MethodDelete, acmeScope+"/accounts/:name", cap))
	target := pathPrefix + "clusters/" + testClusterID + "/acme/accounts/%2e%2e"
	status, env := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400 for an escaped traversal segment", status, env.Message)
	}
	if cap.called {
		t.Error("the handler ran for an escaped traversal segment")
	}
}

// TestACMENodeRoutesUseTheNodeNameFormat pins the six per-node routes to
// apischema's node-name format, which is the tightest of the four spellings of
// that check in the tree.
//
// Three of the six carried a hand-rolled `if node == "" { 400 }` and three
// carried nothing at all — order, renew and revoke went straight to
// url.PathEscape. The format closes that gap for all six at once, and
// proxmox.validateNodeName stays where it is: it guards every caller of the
// client, not just these.
func TestACMENodeRoutesUseTheNodeNameFormat(t *testing.T) {
	nodeRoutes := 0
	for key := range acmeLegacyPermissions {
		method, path, _ := strings.Cut(key, " ")
		if !strings.Contains(path, "/nodes/:node") {
			continue
		}
		nodeRoutes++
		e := declaredEndpoint(t, method, path)
		prop, ok := e.Parameters["node"]
		if !ok {
			t.Errorf("%s declares no node parameter", key)
			continue
		}
		if prop.Format != "node-name" {
			t.Errorf("%s: node declares format %q, want node-name", key, prop.Format)
		}
		if prop.Optional {
			t.Errorf("%s: node is optional; every one of these routes acts on exactly one node", key)
		}
	}
	if nodeRoutes != 6 {
		t.Fatalf("found %d per-node ACME routes, want 6", nodeRoutes)
	}

	// The format refuses a traversal segment, which is what the three routes
	// that checked nothing were missing.
	e := declaredEndpoint(t, fiber.MethodGet, acmeNodeScope+"/certificates")
	for _, bad := range []string{"", ".", "..", "node/../other"} {
		if _, err := e.Parameters.Validate(map[string]any{
			"cluster_id": testClusterID, "node": bad,
		}); err == nil {
			t.Errorf("%q was accepted as a node name", bad)
		}
	}
	if _, err := e.Parameters.Validate(map[string]any{
		"cluster_id": testClusterID, "node": testACMENodeName,
	}); err != nil {
		t.Errorf("a real node name was refused: %v", err)
	}
}

// TestACMEURLParametersKeepTheirEmptySentinel is the compatibility assertion for
// the two URL-valued body fields.
//
// proxmox.CreateACMEAccount sets each form key only `if params.X != ""`, so an
// empty value means "let Proxmox use its default" and has always been accepted.
// The scheme anchor must therefore be an alternation with an anchored empty
// branch — Go's regexp is a substring search, so `^$|https?://` would match
// anything CONTAINING a URL and let file:///etc/passwd through by prefixing it
// with anything at all.
func TestACMEURLParametersKeepTheirEmptySentinel(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, acmeScope+"/accounts")

	for _, name := range []string{"directory", "tos_url"} {
		prop, ok := e.Parameters[name]
		if !ok {
			t.Fatalf("POST .../acme/accounts declares no %q", name)
		}
		if !prop.Optional {
			t.Errorf("%s is required; it has always been optional", name)
		}
		if prop.Pattern != emptyOrACMEURL {
			t.Errorf("%s declares pattern %q, want the empty-or-URL one", name, prop.Pattern)
		}
	}

	valid := map[string]any{"cluster_id": testClusterID, "contact": "admin@example.com"}
	check := func(name, value string, wantOK bool) {
		t.Helper()
		in := map[string]any{}
		for k, v := range valid {
			in[k] = v
		}
		in[name] = value
		_, err := e.Parameters.Validate(in)
		if wantOK && err != nil {
			t.Errorf("%s=%q was refused: %v", name, value, err)
		}
		if !wantOK && err == nil {
			t.Errorf("%s=%q was accepted", name, value)
		}
	}
	for _, name := range []string{"directory", "tos_url"} {
		check(name, "", true)
		check(name, "https://acme-v02.api.letsencrypt.org/directory", true)
		check(name, "http://ca.example.com/acme/directory", true)
		check(name, "file:///etc/passwd", false)
		// The substring-search trap: a value that merely CONTAINS a URL.
		check(name, "file:///etc/passwd#https://example.com", false)
		check(name, "javascript:alert(1)", false)
	}
}

// TestACMEAccountNameKeepsItsEmptySentinel is the same compatibility assertion
// for the third sentinel on this body, and the one that has to be made HERE.
//
// directory and tos_url reach their rule through emptyOrACMEURL, a constant in
// registry_acme.go, so the test above pins them by comparing against it. `name`
// reaches the CATALOGUE — apischema.Rule("pve-object-id-or-empty") — through
// pveObjectNameOrEmptyParam (registry_networks.go), and no test in apischema
// can see this site: those hold the RULE to its witnesses, never a
// declaration to the rule. registry_rule_reference_ratchet_test.go does not
// see it either: it keys on the container that spells the Rule call, which is
// the helper, and does not follow a call into it — so a Pattern reassigned to
// a literal here in createACMEAccountParams, after the helper returns,
// escapes every source-reading guard. This test is what holds the site, as
// TestFirewallAliasRenameKeepsTheEmptySentinel and
// TestSDNVNetUpdateZoneKeepsTheEmptySentinel hold the helper's other two
// callers: it reads the BUILT declaration — the pattern must be the
// catalogued rule, the MaxLength must survive, and the values it admits are
// driven through Validate — so a narrowed pattern, a dropped MaxLength, or an
// empty string some other facet turns away each fail here.
//
// "" is not a third state. proxmox.CreateACMEAccount sets the form key only
// `if params.Name != ""`, identically to directory and tos_url, so an empty name
// reaches Proxmox as no name at all and the account is created under the name
// Proxmox defaults to, which is "default". Before this route was declarative it
// validated nothing, so "" has always been accepted here.
func TestACMEAccountNameKeepsItsEmptySentinel(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, acmeScope+"/accounts")

	prop, ok := e.Parameters["name"]
	if !ok {
		t.Fatal("POST .../acme/accounts declares no name parameter")
	}
	if !prop.Optional {
		t.Error("name is required; Proxmox names the account \"default\" when it is absent")
	}
	if want := apischema.Rule("pve-object-id-or-empty"); prop.Pattern != want {
		t.Errorf("name declares pattern %q, want the catalogued %q (pve-object-id-or-empty): the "+
			"empty sentinel on the object-id rule, no narrower. A Pattern reassigned to a literal at this "+
			"call site is invisible to the guards that read the source, so this is the check that holds it.",
			prop.Pattern, want)
	}
	// The MaxLength pveObjectNameParam carries has to survive the reassignment:
	// nothing else bounds a value that becomes a Proxmox path segment.
	if prop.MaxLength == nil {
		t.Error("name declares no MaxLength, want 64 — the bound pveObjectNameParam sets")
	} else if *prop.MaxLength != 64 {
		t.Errorf("name declares MaxLength %d, want 64 — the bound pveObjectNameParam sets", *prop.MaxLength)
	}

	valid := map[string]any{"cluster_id": testClusterID, "contact": "admin@example.com"}
	check := func(value string, wantOK bool) {
		t.Helper()
		// The value under test goes in LAST, matching the helper above: seed
		// it first and a "name" key appearing in valid would silently
		// overwrite it, leaving every call below exercising one string.
		in := map[string]any{}
		for k, v := range valid {
			in[k] = v
		}
		in["name"] = value
		_, err := e.Parameters.Validate(in)
		if wantOK && err != nil {
			t.Errorf("name=%q was refused: %v", value, err)
		}
		if !wantOK && err == nil {
			t.Errorf("name=%q was accepted", value)
		}
	}
	check("", true)
	check("default", true)
	check("letsencrypt-prod", true)
	// The leading-alphanumeric anchor matters on THIS route for the reason
	// createACMEAccountParams gives rather than the traversal one: the name
	// leaves here as a form field, not a path segment. It is the routes that
	// ADDRESS the account afterwards that concatenate it into
	// /cluster/acme/account/<name> with url.PathEscape, which leaves "." and
	// ".." alone. An account created as ".." could never be read, changed or
	// deleted through this API — a dead row from the moment it was written.
	check(".", false)
	check("..", false)
	check("-lead", false)
	check("a/b", false)
	// The substring-search trap the anchored empty branch exists to avoid: an
	// unanchored `^$|…` would match any value CONTAINING a legal id.
	check("../default", false)
	check(strings.Repeat("a", 65), false)
}

// TestNodeACMEDeleteListReachesTheHandlerAsAList is what
// TestBindBody_PromotesNodeACMEDeleteList used to pin, moved to the layer that
// now does the work.
//
// Nothing else proves the link: the proxmox client tests build the struct in Go,
// and if the list arrived as anything but a []string the handler would send a
// perfectly valid request that cleared nothing — the same silent no-op the
// delete support was added to fix. The digest half is here for the same reason:
// read it from the GET, hand it back on the PUT, or the compare-and-swap is not
// one.
func TestNodeACMEDeleteListReachesTheHandlerAsAList(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, acmeNodeScope+"/acme-config")
	prop, ok := e.Parameters["delete"]
	if !ok {
		t.Fatal("PUT .../acme-config declares no delete parameter")
	}
	if prop.Type != apischema.Array {
		t.Fatalf("delete declares type %q, want %q", prop.Type, apischema.Array)
	}
	if prop.Items == nil || prop.Items.Type != apischema.String {
		t.Fatalf("delete declares items %+v, want string elements", prop.Items)
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeACMEEndpoint(t, fiber.MethodPut, acmeNodeScope+"/acme-config", cap))
	target := pathPrefix + "clusters/" + testClusterID + "/nodes/" + testACMENodeName + "/acme-config"
	body := `{"acmedomain0":"node.example.com","delete":["acmedomain1","acmedomain2"],` +
		`"digest":"da39a3ee5e6b4b0d3255bfef95601890afd80709"}`
	status, env := send(t, app, jsonRequest(http.MethodPut, target, body))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("acmedomain0"); got != "node.example.com" {
		t.Errorf("acmedomain0 = %q", got)
	}
	if got := cap.params.Strings("delete"); !slices.Equal(got, []string{"acmedomain1", "acmedomain2"}) {
		t.Errorf("delete = %v, want [acmedomain1 acmedomain2]", got)
	}
	if got := cap.params.String("digest"); got != "da39a3ee5e6b4b0d3255bfef95601890afd80709" {
		t.Errorf("digest = %q", got)
	}

	// An omitted list is nil rather than empty, which is what lets the handler
	// tell "cleared nothing" from "cleared these" in the audit row.
	cap.called = false
	status, env = send(t, app, jsonRequest(http.MethodPut, target, `{"acme":"account=default"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.Strings("delete"); got != nil {
		t.Errorf("delete = %v for a request that sent none, want nil", got)
	}
}

// TestNodeACMEConfigDeclaresEverySettableKey holds the declaration against
// PVE::NodeConfig's own set: acme plus acmedomain0..5, and no more.
//
// The allow-list this mirrors lives in proxmox.deletableNodeACMEKeys and stays
// there — it is the choke point that stops `delete` reaching the WHOLE node
// config, where description, location and wakeonlan live. What this checks is
// the other direction: a settable key the schema forgets is a key no caller can
// set any more, silently, because an undeclared parameter is now a 400.
func TestNodeACMEConfigDeclaresEverySettableKey(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, acmeNodeScope+"/acme-config")
	want := []string{"acme", "acmedomain0", "acmedomain1", "acmedomain2", "acmedomain3", "acmedomain4", "acmedomain5"}
	for _, name := range want {
		prop, ok := e.Parameters[name]
		if !ok {
			t.Errorf("declares no %q — a caller can no longer set it at all", name)
			continue
		}
		if !prop.Optional {
			t.Errorf("%q is required; every ACME key is optional in PVE's node config", name)
		}
		if prop.Default != nil {
			t.Errorf("%q declares default %v — an omitted key must leave the stored value alone",
				name, prop.Default)
		}
	}
	// acmedomain6 does not exist: $MAXDOMAINS is 5 in PVE::NodeConfig, so
	// declaring one would publish a key Proxmox refuses.
	if _, declared := e.Parameters["acmedomain6"]; declared {
		t.Error("declares acmedomain6; PVE's $MAXDOMAINS is 5")
	}
}

// TestACMECreateRequiresWhatTheHandlerDid pins the required sets against the
// explicit checks each handler made before the migration.
func TestACMECreateRequiresWhatTheHandlerDid(t *testing.T) {
	cases := []struct {
		method, path string
		required     []string
		optional     []string
	}{
		{fiber.MethodPost, acmeScope + "/accounts", []string{"contact"}, []string{"name", "directory", "tos_url"}},
		{fiber.MethodPost, acmeScope + "/plugins", []string{"plugin_id", "type"}, []string{"api", "data", "validation-delay"}},
		{fiber.MethodPut, acmeScope + "/accounts/:name", nil, []string{"contact"}},
		{fiber.MethodPut, acmeScope + "/plugins/:plugin_id", nil, []string{"api", "data", "validation-delay", "digest"}},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			e := declaredEndpoint(t, tc.method, tc.path)
			for _, name := range tc.required {
				if e.Parameters[name].Optional {
					t.Errorf("%q is optional, but the handler refused a request without it", name)
				}
			}
			for _, name := range tc.optional {
				prop, ok := e.Parameters[name]
				if !ok {
					t.Errorf("declares no %q parameter", name)
					continue
				}
				if !prop.Optional {
					t.Errorf("%q is required, but the endpoint has always accepted a request without it", name)
				}
			}
		})
	}

	// End to end on the two creates: the missing field 400s, and names itself.
	for _, tc := range []struct{ path, body, want string }{
		{acmeScope + "/accounts", `{}`, "contact"},
		{acmeScope + "/plugins", `{"type":"dns"}`, "plugin_id"},
		{acmeScope + "/plugins", `{"id":"dns-example"}`, "type"},
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeACMEEndpoint(t, fiber.MethodPost, tc.path, cap))
		target := acmeRoute(tc.path)
		status, env := send(t, app, jsonRequest(http.MethodPost, target, tc.body))
		if status != fiber.StatusBadRequest {
			t.Errorf("POST %s %s: status = %d (%q), want 400", tc.path, tc.body, status, env.Message)
		}
		if !strings.Contains(env.Message, tc.want) {
			t.Errorf("POST %s %s: the rejection does not name %q: %q", tc.path, tc.body, tc.want, env.Message)
		}
		if cap.called {
			t.Errorf("POST %s %s: the handler ran for an incomplete body", tc.path, tc.body)
		}
	}
}

// TestACMEPluginCreateAliasBindsTheBodyKeyCallersSend is the compatibility half
// of the rename that checkPathParams forced.
//
// The create body's plugin id is declared as plugin_id because a body parameter
// NAMED "id" is a name the permission middleware also reads (clusterIDFromParam
// falls back to it), and Register refuses one. The alias is what keeps the
// existing caller working: the plugin dialog sends {"id": …}. Both spellings
// must bind to the same parameter, and sending BOTH must be reported rather than
// silently resolved — otherwise the caller cannot tell which one won.
func TestACMEPluginCreateAliasBindsTheBodyKeyCallersSend(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, acmeScope+"/plugins")
	if got := e.Parameters["plugin_id"].Alias; got != "id" {
		t.Fatalf("plugin_id declares alias %q, want \"id\" — that is the key the dialog sends", got)
	}
	if _, declared := e.Parameters["id"]; declared {
		t.Fatal("\"id\" is declared as a parameter in its own right; Register refuses that on a " +
			"cluster-gated route, and the alias is what makes the spelling work instead")
	}

	target := pathPrefix + "clusters/" + testClusterID + "/acme/plugins"
	for _, spelling := range []string{"id", "plugin_id"} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeACMEEndpoint(t, fiber.MethodPost, acmeScope+"/plugins", cap))
		body := `{"` + spelling + `":"dns-example","type":"dns"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
		if status != fiber.StatusNoContent {
			t.Fatalf("%s: status = %d (%q), want 204", spelling, status, env.Message)
		}
		if got := cap.params.String("plugin_id"); got != "dns-example" {
			t.Errorf("%s: plugin_id = %q, want dns-example", spelling, got)
		}
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeACMEEndpoint(t, fiber.MethodPost, acmeScope+"/plugins", cap))
	both := `{"id":"a","plugin_id":"b","type":"dns"}`
	status, _ := send(t, app, jsonRequest(http.MethodPost, target, both))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400 when both spellings are sent", status)
	}
	if cap.called {
		t.Error("the handler ran for a body carrying both spellings of one parameter")
	}
}

// TestACMECertificateForceKeepsItsDefault records a tightening this migration
// makes deliberately.
//
// OrderNodeCertificate and RenewNodeCertificate bound their body with
// `_ = c.Bind().Body(&req)` — the error DISCARDED — so a malformed payload
// silently ordered with force=false and the operator was never told their
// request had been half-read. The declaration keeps the default and the meaning,
// and turns the malformed body into the 400 it always should have been.
func TestACMECertificateForceKeepsItsDefault(t *testing.T) {
	for _, r := range []struct {
		method, path string
	}{
		{fiber.MethodPost, acmeNodeScope + "/certificates/order"},
		{fiber.MethodPut, acmeNodeScope + "/certificates/renew"},
	} {
		t.Run(r.method, func(t *testing.T) {
			e := declaredEndpoint(t, r.method, r.path)
			prop, ok := e.Parameters["force"]
			if !ok {
				t.Fatal("declares no force parameter")
			}
			if prop.Type != apischema.Boolean || !prop.Optional || prop.Default != false {
				t.Errorf("force = %+v, want an optional boolean defaulting to false", prop)
			}

			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeACMEEndpoint(t, r.method, r.path, cap))
			target := acmeRoute(r.path)

			// No body at all: force is false, exactly as before.
			status, env := send(t, app, httptest.NewRequest(r.method, target, nil))
			if status != fiber.StatusNoContent {
				t.Fatalf("empty body: status = %d (%q), want 204", status, env.Message)
			}
			if cap.params.Bool("force") {
				t.Error("force is true for a request that sent no body")
			}

			cap.called = false
			status, env = send(t, app, jsonRequest(r.method, target, `{"force":true}`))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			if !cap.params.Bool("force") {
				t.Error("force is false although the request sent true")
			}

			// The malformed body that used to be swallowed.
			cap.called = false
			status, _ = send(t, app, jsonRequest(r.method, target, `{"force":`))
			if status != fiber.StatusBadRequest {
				t.Errorf("status = %d, want 400 for a malformed body", status)
			}
			if cap.called {
				t.Error("the handler ran for a malformed body")
			}
		})
	}
}

// TestACMEPluginCredentialIsWriteOnly pins the one parameter in this domain that
// carries a credential: the dns-01 plugin's `data`, which holds the DNS
// provider's API token.
//
// It must be declarable — that is the whole point of the endpoint — and it must
// never be read back. ListPlugins blanks it on the way out; this asserts the
// declaration does not additionally publish it as a query filter or hand a
// second route a way to echo it.
func TestACMEPluginCredentialIsWriteOnly(t *testing.T) {
	for _, r := range []struct{ method, path string }{
		{fiber.MethodPost, acmeScope + "/plugins"},
		{fiber.MethodPut, acmeScope + "/plugins/:plugin_id"},
	} {
		e := declaredEndpoint(t, r.method, r.path)
		prop, ok := e.Parameters["data"]
		if !ok {
			t.Errorf("%s %s declares no data parameter", r.method, r.path)
			continue
		}
		if src := apischema.ResolveSource("data", prop, r.method, pathParamNames(r.path)); src != apischema.SourceBody {
			t.Errorf("%s %s: data resolves to %q, want the body — a credential must not travel in a URL "+
				"that proxies and access logs record", r.method, r.path, src)
		}
	}

	// And it is not on any READ route, which is what would let it come back.
	for key := range acmeLegacyPermissions {
		method, path, _ := strings.Cut(key, " ")
		if method != fiber.MethodGet {
			continue
		}
		if _, declared := declaredEndpoint(t, method, path).Parameters["data"]; declared {
			t.Errorf("%s declares a data parameter on a read route", key)
		}
	}
}

// TestEveryACMEEndpointIsDocumented holds the
// declaration-is-the-documentation rule for this domain.
func TestEveryACMEEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredACMEEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		if e.Group != "Certificates" {
			t.Errorf("%s declares group %q, want %q", key, e.Group, "Certificates")
		}
	}
}
