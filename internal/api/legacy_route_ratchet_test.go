package api

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"testing"
)

// Every route not yet in the registry is legacy. Rather than hand-write 550 exemption
// reasons, the CURRENT set is captured once (legacyRouteBaseline) and the live set may only
// lose entries, migrated into the registry, never gain one.

// legacyRouteBaseline is a machine-generated snapshot of every "METHOD path" setupRoutes registered
// OUTSIDE the registry: 552 on 2026-09-17, 17 after the last tranche (6j). A route not here must be
// declared, not added to router.go. Each of the 17 stays because the registry CANNOT express it, pinned by
// a test: a greedy wildcard segment (TestStorageDeleteContentIsStillLegacy); a JSON array of objects
// (TestFirewallTemplateWritesAreStillLegacy, TestAlertRuleWritesAreStillLegacy); an IdP-composed query
// string (TestOIDCCallbackIsStillLegacy); authOptional (TestAuthOptionalRoutesAreStillLegacy); a
// three-state parent_id (TestVMFolderReparentIsStillLegacy); instance-shared reads and a PUT of arbitrary
// JSON (TestAPIDocsIsStillLegacy, TestSettingsReadsAreStillLegacy); and /healthz, outside /api/v1/
// (TestHealthzIsStillLegacy). Only ever REMOVE entries; regenerate with
// NEXARA_DUMP_LEGACY_ROUTES=1 go test ./internal/api/ -run TestDumpLegacyRouteKeys -v
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

// legacyRouteRatchetViolations reports every key in actual but not in baseline: a route
// registered as legacy instead of through the registry. baseline is a parameter so a
// synthetic snapshot can prove the comparison fires.
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
	actual := legacyRouteKeys(t, sharedRouteStub(t))

	for _, key := range legacyRouteRatchetViolations(actual, legacyRouteBaseline) {
		t.Errorf("route %s is registered as legacy but is not in legacyRouteBaseline — "+
			"a new route must be declared through the registry (internal/api/registry.go), "+
			"not added to a legacy block in router.go", key)
	}
	// The real set is clean, so show the comparison bites: a key absent from the baseline is
	// reported, and one missing from actual (a migration) is not.
	const ghost = "GET /api/v1/ghost-route"
	if got := legacyRouteRatchetViolations(append(slices.Clone(actual), ghost), legacyRouteBaseline); !slices.Equal(got, []string{ghost}) {
		t.Errorf("violations = %v, want exactly [%q]", got, ghost)
	}
	if len(actual) > 0 { // the set is meant to reach zero one day
		if got := legacyRouteRatchetViolations(actual[1:], legacyRouteBaseline); len(got) != 0 {
			t.Errorf("violations = %v after one route left the legacy set, want none: shrinkage is the ratchet working", got)
		}
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

// TestDumpLegacyRouteKeys is not a guard (it is off the "TestGuard_" prefix so `-run
// TestGuard_` skips it): it prints legacyRouteBaseline's map-literal lines, only when
// NEXARA_DUMP_LEGACY_ROUTES is set.
func TestDumpLegacyRouteKeys(t *testing.T) {
	if os.Getenv("NEXARA_DUMP_LEGACY_ROUTES") == "" {
		t.Skip("set NEXARA_DUMP_LEGACY_ROUTES=1 to print the current legacy route set")
	}
	for _, key := range legacyRouteKeys(t, sharedRouteStub(t)) {
		fmt.Printf("\t%q: true,\n", key)
	}
}
