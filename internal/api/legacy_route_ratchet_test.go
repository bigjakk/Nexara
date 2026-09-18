package api

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// Every route not yet in the registry is legacy. Hand-writing 550 reasons
// for that would be exactly the rubber-stamped exemption list this file's
// sibling guards exist to avoid, so instead the CURRENT set is captured
// once below (legacyRouteBaseline) and this guard asserts the live set
// only ever loses entries — migrated into the registry — and never gains
// one. A brand new route must be declared through the registry; the
// legacy blocks in router.go are a closed set from here on.

// legacyRouteBaseline is a machine-generated snapshot of every
// "METHOD path" key setupRoutes registered OUTSIDE the declarative
// registry. It was first captured on 2026-09-17 at 552 routes and has
// shrunk with every tranche since: 501 after Phase 6a (18 containers, on top of
// the 33 VMs already migrated), 439 after 6b (Ceph 17, HA 17, DRS 10, CVE 10,
// replication 8), 402 after 6c (migrations 7, cluster options 9, guest tools 7,
// virtio-win 8, PBS 6), 352 after 6d (38 nodes, 12 of 13 storage), 288 after 6e
// (64 of NetworkHandler's 66), 237 after 6f (28 backup, 11 VM-import, 12
// reports), 192 after 6g (25 Veeam, 20 of AlertHandler's 22), 130 after 6h (25
// access control, 18 ACME, 19 rolling-update) and 76 after 6i (54 of the
// identity tranche's 57).
//
// It is now at 17 after Phase 6j — the final tranche — moved the remaining 59:
// audit 9, clusters 7, notification-DLQ 5, metric-servers 5, tasks 4, schedules
// 4, pools 4, vm-folders 4 of 5, metrics 3, apt-repositories 3, favorites 3,
// settings 3 of 9, guest-snapshots 2, search 1, changelog 1 and the build
// version probe.
//
// TestGuard_LegacyRouteSetOnlyShrinks compares the live legacy set
// against it and fails if anything NEW shows up — a route that is not
// here must be added through the registry, not through router.go.
//
// EVERY route left in this list is here because the registry CANNOT express
// it, not because nobody got to them — Phase 6j was the last tranche, and
// /healthz aside, each of the other 16 is pinned by a test so it stays a
// decision rather than a gap. They fall into five groups:
//
//   - DELETE .../storage/:storage_id/content/* takes its volume id as a
//     greedy WILDCARD segment, which checkPathParams refuses outright
//     because a parameter schema cannot describe one. See
//     registerStorageEndpoints in internal/api/registry_storage.go and
//     TestStorageDeleteContentIsStillLegacy.
//   - POST /api/v1/firewall-templates, PUT /api/v1/firewall-templates/:id,
//     POST /api/v1/alert-rules and PUT /api/v1/alert-rules/:id carry a JSON
//     array of OBJECTS (`rules`, `escalation_chain`), and apischema's
//     Property.Items is restricted to scalar element types. See
//     TestFirewallTemplateWritesAreStillLegacy and
//     TestAlertRuleWritesAreStillLegacy.
//   - GET /api/v1/auth/oidc/callback has a query string composed by the
//     IDENTITY PROVIDER, and the registry answers an undeclared key with a 400
//     (PVE's additionalProperties => 0), so the parameter set is open by
//     construction. See TestOIDCCallbackIsStillLegacy.
//   - POST /api/v1/auth/register and POST /api/v1/auth/logout are mounted with
//     authOptional, which the Permissions vocabulary has no shape for: it
//     parses a session IF one is presented and lets the request through either
//     way. See TestAuthOptionalRoutesAreStillLegacy.
//   - PATCH .../vm-folders/:folder_id carries a THREE-state parent_id — absent
//     leaves the folder where it is, an explicit null moves it to the top
//     level, a uuid moves it under that folder — and apischema's present()
//     reads an explicit JSON null as ABSENT, so a declaration would turn "move
//     to the top level" into a request that answers 200 and changes nothing.
//     See TestVMFolderReparentIsStillLegacy.
//   - GET /api/v1/api-docs and the three branding reads (GET
//     /api/v1/settings/branding{,/logo-file,/favicon-file}) are
//     instanceSharedRoutes-shaped: authenticated, serving instance data
//     identical for every caller, with no subject to authorize. Permissions has
//     no shape for that, and folding them into SelfService would make
//     selfServiceRoutes' stated invariant false. See TestAPIDocsIsStillLegacy
//     and TestSettingsReadsAreStillLegacy.
//   - GET /api/v1/settings and GET /api/v1/settings/:key perform NO permission
//     check on any path (settingScopeID gates only when `write && adminOnly`),
//     and are instance-shared for ?scope=global and self-service for
//     ?scope=user, chosen per request. PUT /api/v1/settings/:key has the same
//     conditional check as its declared sibling the DELETE, but its `value` is
//     arbitrary JSON and apischema's Type vocabulary has no "any JSON value"
//     member. See TestSettingsReadsAreStillLegacy.
//   - GET /healthz is the only entry with no test of its own beyond
//     TestHealthzIsStillLegacy: Register refuses any path outside /api/v1/, and
//     the container health check has to answer on a path that is not part of
//     the API surface.
//
// Regenerate after migrating routes into the registry (the set should
// only ever need entries REMOVED, never added):
//
//	NEXARA_DUMP_LEGACY_ROUTES=1 go test ./internal/api/ -run TestDumpLegacyRouteKeys -v
//
// and paste stdout directly in place of the map body below — it prints
// ready-to-paste `"METHOD path": true,` lines, not bare keys, so there is
// no reformatting step where a hand-edit could sneak in an addition. Do
// not hand-edit this list to ADD an entry — that defeats the ratchet;
// removing entries that were migrated away is the intended maintenance.
var legacyRouteBaseline = map[string]bool{
	"DELETE /api/v1/clusters/:cluster_id/storage/:storage_id/content/*": true,
	"GET /api/v1/api-docs":                                     true,
	"GET /api/v1/auth/oidc/callback":                           true,
	"GET /api/v1/settings":                                     true,
	"GET /api/v1/settings/:key":                                true,
	"GET /api/v1/settings/branding":                            true,
	"GET /api/v1/settings/branding/favicon-file":               true,
	"GET /api/v1/settings/branding/logo-file":                  true,
	"GET /healthz":                                             true,
	"PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id": true,
	"POST /api/v1/alert-rules":                                 true,
	"POST /api/v1/auth/logout":                                 true,
	"POST /api/v1/auth/register":                               true,
	"POST /api/v1/firewall-templates":                          true,
	"PUT /api/v1/alert-rules/:id":                              true,
	"PUT /api/v1/firewall-templates/:id":                       true,
	"PUT /api/v1/settings/:key":                                true,
}

// legacyRouteRatchetViolations reports every key present in actual but
// absent from baseline — a route that exists now but was not in the
// snapshot, which can only mean a new route was registered directly as
// legacy instead of through the registry. baseline is passed in (rather
// than read as the package var directly) so a test can hand in a
// synthetic snapshot and prove the comparison fires.
func legacyRouteRatchetViolations(actual []string, baseline map[string]bool) []string {
	var out []string
	for _, key := range actual {
		if !baseline[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// TestGuard_LegacyRouteSetOnlyShrinks is the production guard.
func TestGuard_LegacyRouteSetOnlyShrinks(t *testing.T) {
	actual := legacyRouteKeys(t, newRouteStubServer(t))

	for _, key := range legacyRouteRatchetViolations(actual, legacyRouteBaseline) {
		t.Errorf("route %s is registered as legacy but is not in legacyRouteBaseline — "+
			"a new route must be declared through the registry (internal/api/registry.go), "+
			"not added to a legacy block in router.go", key)
	}

	// Not a failure — shrinkage is the ratchet doing its job — but worth a
	// nudge to regenerate the baseline so the recorded ceiling matches
	// reality (see legacyRouteBaseline's doc comment for the command).
	actualSet := make(map[string]bool, len(actual))
	for _, key := range actual {
		actualSet[key] = true
	}
	var migrated int
	for key := range legacyRouteBaseline {
		if !actualSet[key] {
			migrated++
		}
	}
	if migrated > 0 {
		t.Logf("%d route(s) left the legacy set since legacyRouteBaseline was captured — "+
			"regenerate it (see the doc comment on legacyRouteBaseline) to tighten the ratchet", migrated)
	}
}

// TestLegacyRouteRatchetViolations_CatchesANewRoute proves the comparison
// bites: a key present in actual but absent from baseline is reported,
// and shrinkage (a baseline key no longer in actual — a migration) is
// not.
func TestLegacyRouteRatchetViolations_CatchesANewRoute(t *testing.T) {
	baseline := map[string]bool{
		"GET /api/v1/old-thing":      true,
		"GET /api/v1/migrated-thing": true, // present in baseline, absent from actual: shrinkage, not a violation
	}
	actual := []string{
		"GET /api/v1/old-thing",
		"GET /api/v1/new-thing", // not in baseline: a violation
	}

	got := legacyRouteRatchetViolations(actual, baseline)
	if len(got) != 1 || got[0] != "GET /api/v1/new-thing" {
		t.Fatalf("violations = %v, want exactly [\"GET /api/v1/new-thing\"]", got)
	}
}

// TestDumpLegacyRouteKeys is not a guard — deliberately off the
// "TestGuard_" naming convention so `-run TestGuard_` (a natural way to
// run "every guard") does not sweep it in. It is the generator for
// legacyRouteBaseline above, printing ready-to-paste map-literal lines
// rather than bare keys (see legacyRouteBaseline's doc comment) so
// regenerating it is a straight paste, not a reformat that could hide a
// hand-added entry. It only runs when explicitly requested, because
// printing 550+ lines on every `go test` run would bury the signal the
// other guards in this package exist to give.
func TestDumpLegacyRouteKeys(t *testing.T) {
	if os.Getenv("NEXARA_DUMP_LEGACY_ROUTES") == "" {
		t.Skip("set NEXARA_DUMP_LEGACY_ROUTES=1 to print the current legacy route set")
	}
	for _, key := range legacyRouteKeys(t, newRouteStubServer(t)) {
		fmt.Printf("\t%q: true,\n", key)
	}
}
