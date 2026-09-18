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
// registry. It was first captured on 2026-09-17 at 552 routes, then at 501
// after Phase 6a moved the 18 ContainerHandler routes into the registry
// (the 33 VMHandler routes had already left), then at 439 after Phase
// 6b moved the 62 routes of five cluster-scoped domains — Ceph 17, HA 17,
// DRS 10, CVE 10 and replication 8 — then at 402 after Phase 6c moved
// 37 more: migrations 7, cluster options 9, guest tools 7, virtio-win 8
// and PBS servers 6, then at 352 after Phase 6d moved 50: all 38
// NodeHandler routes and 12 of the 13 StorageHandler ones, then at 288
// after Phase 6e moved 64 of NetworkHandler's 66: 7 node-interface, 28
// firewall, 25 SDN and 4 of the 6 firewall-template routes, then at 237
// after Phase 6f moved 51: 28 backup — 19 of them the routes nested under
// /pbs-servers/:pbs_id — 11 VM-import, and all 12 report routes, then at
// 192 after Phase 6g moved 45: all 25 Veeam routes — 24 under
// /veeam-servers and the last one the VM detail page's Veeam card — and 20
// of AlertHandler's 22. It is now at 130 after Phase 6h moved 62: all 25
// Proxmox access-control routes, all 18 ACME routes and all 19
// rolling-update routes — the last of those counting the 7 SSH credential
// and known-host routes the same handler owns. It is now at 76 after Phase 6i
// moved 54 of the identity tranche's 57: 10 RBAC, 4 user-management, 6 API
// key, 7 LDAP, 7 OIDC (6 admin plus /auth/oidc/authorize), 7 TOTP (including
// the admin reset under /users/:id) and 13 of AuthHandler's 15.
//
// TestGuard_LegacyRouteSetOnlyShrinks compares the live legacy set
// against it and fails if anything NEW shows up — a route that is not
// here must be added through the registry, not through router.go.
//
// EIGHT routes in this list are here because the registry CANNOT express
// them, not because nobody got to them, and each is pinned by a test so it
// stays a decision rather than a gap. The first five are blocked by a
// PARAMETER TYPE; the last three, added in Phase 6i, are the first blocked by
// something else:
//
//   - DELETE .../storage/:storage_id/content/* takes its volume id as a
//     greedy WILDCARD segment, which checkPathParams refuses outright
//     because a parameter schema cannot describe one. See
//     registerStorageEndpoints in internal/api/registry_storage.go and
//     TestStorageDeleteContentIsStillLegacy.
//   - POST /api/v1/firewall-templates and PUT /api/v1/firewall-templates/:id
//     carry `rules`, a JSON array of OBJECTS, and apischema's Property.Items
//     is restricted to scalar element types. See
//     registerFirewallTemplateEndpoints in
//     internal/api/registry_firewall_templates.go and
//     TestFirewallTemplateWritesAreStillLegacy.
//   - POST /api/v1/alert-rules and PUT /api/v1/alert-rules/:id carry
//     `escalation_chain`, an array of objects for the same reason. See
//     registerAlertEndpoints in internal/api/registry_alerts.go and
//     TestAlertRuleWritesAreStillLegacy.
//   - GET /api/v1/auth/oidc/callback has a query string composed by the
//     IDENTITY PROVIDER, and the registry answers an undeclared key with a 400
//     (PVE's additionalProperties => 0). RFC 9207 adds `iss`, session
//     management adds `session_state`, an error response carries
//     `error`/`error_description`, and a provider may add its own — so the
//     parameter set is open by construction. See registerOIDCEndpoints in
//     internal/api/registry_oidc.go and TestOIDCCallbackIsStillLegacy.
//   - POST /api/v1/auth/register and POST /api/v1/auth/logout are mounted with
//     authOptional, which the Permissions vocabulary has no shape for: it
//     parses a session IF one is presented and lets the request through either
//     way. Public installs no authentication at all — Register READS
//     c.Locals("role") to decide whether the caller may create an account —
//     and every other shape requires a session, which would 401 the logout a
//     valid refresh cookie must still be able to perform. See
//     registerAuthEndpoints in internal/api/registry_auth.go and
//     TestAuthOptionalRoutesAreStillLegacy.
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
	"DELETE /api/v1/clusters/:cluster_id/metric-servers/:server_id":     true,
	"DELETE /api/v1/clusters/:cluster_id/pools/:pool_id":                true,
	"DELETE /api/v1/clusters/:cluster_id/schedules/:id":                 true,
	"DELETE /api/v1/clusters/:cluster_id/storage/:storage_id/content/*": true,
	"DELETE /api/v1/clusters/:cluster_id/vm-folders/:folder_id":         true,
	"DELETE /api/v1/clusters/:id":                                       true,
	"DELETE /api/v1/favorites":                                          true,
	"DELETE /api/v1/notification-dlq/:id":                               true,
	"DELETE /api/v1/settings/:key":                                      true,
	"DELETE /api/v1/tasks":                                              true,
	"GET /api/v1/api-docs":                                              true,
	"GET /api/v1/audit-log":                                             true,
	"GET /api/v1/audit-log/actions":                                     true,
	"GET /api/v1/audit-log/export":                                      true,
	"GET /api/v1/audit-log/recent":                                      true,
	"GET /api/v1/audit-log/syslog-config":                               true,
	"GET /api/v1/audit-log/users":                                       true,
	"GET /api/v1/auth/oidc/callback":                                    true,
	"GET /api/v1/changelog":                                             true,
	"GET /api/v1/clusters":                                              true,
	"GET /api/v1/clusters/:cluster_id/audit-log":                        true,
	"GET /api/v1/clusters/:cluster_id/metric-servers":                   true,
	"GET /api/v1/clusters/:cluster_id/metric-servers/:server_id":        true,
	"GET /api/v1/clusters/:cluster_id/metrics":                          true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":     true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/metrics":           true,
	"GET /api/v1/clusters/:cluster_id/pools/:pool_id":                   true,
	"GET /api/v1/clusters/:cluster_id/schedules":                        true,
	"GET /api/v1/clusters/:cluster_id/vm-folders":                       true,
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/metrics":               true,
	"GET /api/v1/clusters/:id":                                          true,
	"GET /api/v1/favorites":                                             true,
	"GET /api/v1/guest-snapshots":                                       true,
	"GET /api/v1/notification-dlq":                                      true,
	"GET /api/v1/notification-dlq/summary":                              true,
	"GET /api/v1/search":                                                true,
	"GET /api/v1/settings":                                              true,
	"GET /api/v1/settings/:key":                                         true,
	"GET /api/v1/settings/branding":                                     true,
	"GET /api/v1/settings/branding/favicon-file":                        true,
	"GET /api/v1/settings/branding/logo-file":                           true,
	"GET /api/v1/tasks":                                                 true,
	"GET /api/v1/version":                                               true,
	"GET /healthz":                                                      true,
	"PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id":          true,
	"POST /api/v1/alert-rules":                                          true,
	"POST /api/v1/audit-log/syslog-test":                                true,
	"POST /api/v1/auth/logout":                                          true,
	"POST /api/v1/auth/register":                                        true,
	"POST /api/v1/clusters":                                             true,
	"POST /api/v1/clusters/:cluster_id/guest-snapshots/resync":          true,
	"POST /api/v1/clusters/:cluster_id/metric-servers":                  true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":    true,
	"POST /api/v1/clusters/:cluster_id/pools":                           true,
	"POST /api/v1/clusters/:cluster_id/schedules":                       true,
	"POST /api/v1/clusters/:cluster_id/vm-folders":                      true,
	"POST /api/v1/clusters/:id/verify-certificate":                      true,
	"POST /api/v1/clusters/fetch-fingerprint":                           true,
	"POST /api/v1/favorites":                                            true,
	"POST /api/v1/firewall-templates":                                   true,
	"POST /api/v1/notification-dlq/:id/dismiss":                         true,
	"POST /api/v1/notification-dlq/:id/retry":                           true,
	"POST /api/v1/settings/branding/favicon":                            true,
	"POST /api/v1/settings/branding/logo":                               true,
	"POST /api/v1/tasks":                                                true,
	"PUT /api/v1/alert-rules/:id":                                       true,
	"PUT /api/v1/audit-log/syslog-config":                               true,
	"PUT /api/v1/clusters/:cluster_id/metric-servers/:server_id":        true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":     true,
	"PUT /api/v1/clusters/:cluster_id/pools/:pool_id":                   true,
	"PUT /api/v1/clusters/:cluster_id/schedules/:id":                    true,
	"PUT /api/v1/clusters/:cluster_id/vms/:vm_id/folder":                true,
	"PUT /api/v1/clusters/:id":                                          true,
	"PUT /api/v1/firewall-templates/:id":                                true,
	"PUT /api/v1/settings/:key":                                         true,
	"PUT /api/v1/tasks/:upid":                                           true,
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
