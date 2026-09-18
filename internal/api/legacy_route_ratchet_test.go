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
// firewall, 25 SDN and 4 of the 6 firewall-template routes. It is now at
// 237 after Phase 6f moved 51: 28 backup — 19 of them the routes nested
// under /pbs-servers/:pbs_id — 11 VM-import, and all 12 report routes.
//
// TestGuard_LegacyRouteSetOnlyShrinks compares the live legacy set
// against it and fails if anything NEW shows up — a route that is not
// here must be added through the registry, not through router.go.
//
// THREE routes in this list are here because the registry CANNOT express
// them, not because nobody got to them, and each is pinned by a test so it
// stays a decision rather than a gap:
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
	"DELETE /api/v1/admin/api-keys/:id":                                                    true,
	"DELETE /api/v1/alert-rules/:id":                                                       true,
	"DELETE /api/v1/api-keys":                                                              true,
	"DELETE /api/v1/api-keys/:id":                                                          true,
	"DELETE /api/v1/auth/sessions/:id":                                                     true,
	"DELETE /api/v1/auth/totp":                                                             true,
	"DELETE /api/v1/clusters/:cluster_id/access/groups/:groupid":                           true,
	"DELETE /api/v1/clusters/:cluster_id/access/roles/:roleid":                             true,
	"DELETE /api/v1/clusters/:cluster_id/access/users/:userid":                             true,
	"DELETE /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":             true,
	"DELETE /api/v1/clusters/:cluster_id/acme/accounts/:name":                              true,
	"DELETE /api/v1/clusters/:cluster_id/acme/plugins/:plugin_id":                          true,
	"DELETE /api/v1/clusters/:cluster_id/maintenance-windows/:id":                          true,
	"DELETE /api/v1/clusters/:cluster_id/metric-servers/:server_id":                        true,
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node/certificates/revoke":                  true,
	"DELETE /api/v1/clusters/:cluster_id/pools/:pool_id":                                   true,
	"DELETE /api/v1/clusters/:cluster_id/schedules/:id":                                    true,
	"DELETE /api/v1/clusters/:cluster_id/ssh-credentials":                                  true,
	"DELETE /api/v1/clusters/:cluster_id/ssh-known-hosts/:id":                              true,
	"DELETE /api/v1/clusters/:cluster_id/storage/:storage_id/content/*":                    true,
	"DELETE /api/v1/clusters/:cluster_id/vm-folders/:folder_id":                            true,
	"DELETE /api/v1/clusters/:id":                                                          true,
	"DELETE /api/v1/favorites":                                                             true,
	"DELETE /api/v1/ldap/configs/:id":                                                      true,
	"DELETE /api/v1/notification-channels/:id":                                             true,
	"DELETE /api/v1/notification-dlq/:id":                                                  true,
	"DELETE /api/v1/oidc/configs/:id":                                                      true,
	"DELETE /api/v1/rbac/roles/:id":                                                        true,
	"DELETE /api/v1/rbac/users/:user_id/roles/:id":                                         true,
	"DELETE /api/v1/settings/:key":                                                         true,
	"DELETE /api/v1/tasks":                                                                 true,
	"DELETE /api/v1/users/:id":                                                             true,
	"DELETE /api/v1/users/:id/totp":                                                        true,
	"DELETE /api/v1/veeam-servers/:id":                                                     true,
	"GET /api/v1/admin/api-keys":                                                           true,
	"GET /api/v1/alert-rules":                                                              true,
	"GET /api/v1/alert-rules/:id":                                                          true,
	"GET /api/v1/alerts":                                                                   true,
	"GET /api/v1/alerts/:id":                                                               true,
	"GET /api/v1/alerts/summary":                                                           true,
	"GET /api/v1/api-docs":                                                                 true,
	"GET /api/v1/api-keys":                                                                 true,
	"GET /api/v1/audit-log":                                                                true,
	"GET /api/v1/audit-log/actions":                                                        true,
	"GET /api/v1/audit-log/export":                                                         true,
	"GET /api/v1/audit-log/recent":                                                         true,
	"GET /api/v1/audit-log/syslog-config":                                                  true,
	"GET /api/v1/audit-log/users":                                                          true,
	"GET /api/v1/auth/me":                                                                  true,
	"GET /api/v1/auth/oidc/authorize":                                                      true,
	"GET /api/v1/auth/oidc/callback":                                                       true,
	"GET /api/v1/auth/sessions":                                                            true,
	"GET /api/v1/auth/setup-status":                                                        true,
	"GET /api/v1/auth/sso-status":                                                          true,
	"GET /api/v1/auth/totp/status":                                                         true,
	"GET /api/v1/changelog":                                                                true,
	"GET /api/v1/clusters":                                                                 true,
	"GET /api/v1/clusters/:cluster_id/access/acl":                                          true,
	"GET /api/v1/clusters/:cluster_id/access/domains":                                      true,
	"GET /api/v1/clusters/:cluster_id/access/domains/:realm":                               true,
	"GET /api/v1/clusters/:cluster_id/access/groups":                                       true,
	"GET /api/v1/clusters/:cluster_id/access/groups/:groupid":                              true,
	"GET /api/v1/clusters/:cluster_id/access/permissions":                                  true,
	"GET /api/v1/clusters/:cluster_id/access/roles":                                        true,
	"GET /api/v1/clusters/:cluster_id/access/roles/:roleid":                                true,
	"GET /api/v1/clusters/:cluster_id/access/users":                                        true,
	"GET /api/v1/clusters/:cluster_id/access/users/:userid":                                true,
	"GET /api/v1/clusters/:cluster_id/access/users/:userid/tokens":                         true,
	"GET /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":                true,
	"GET /api/v1/clusters/:cluster_id/acme/accounts":                                       true,
	"GET /api/v1/clusters/:cluster_id/acme/accounts/:name":                                 true,
	"GET /api/v1/clusters/:cluster_id/acme/challenge-schema":                               true,
	"GET /api/v1/clusters/:cluster_id/acme/directories":                                    true,
	"GET /api/v1/clusters/:cluster_id/acme/plugins":                                        true,
	"GET /api/v1/clusters/:cluster_id/acme/tos":                                            true,
	"GET /api/v1/clusters/:cluster_id/alerts":                                              true,
	"GET /api/v1/clusters/:cluster_id/alerts/count":                                        true,
	"GET /api/v1/clusters/:cluster_id/audit-log":                                           true,
	"GET /api/v1/clusters/:cluster_id/maintenance-windows":                                 true,
	"GET /api/v1/clusters/:cluster_id/metric-servers":                                      true,
	"GET /api/v1/clusters/:cluster_id/metric-servers/:server_id":                           true,
	"GET /api/v1/clusters/:cluster_id/metrics":                                             true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node/acme-config":                             true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":                        true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node/certificates":                            true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node/packages":                                true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/metrics":                              true,
	"GET /api/v1/clusters/:cluster_id/pools/:pool_id":                                      true,
	"GET /api/v1/clusters/:cluster_id/rolling-updates":                                     true,
	"GET /api/v1/clusters/:cluster_id/rolling-updates/:id":                                 true,
	"GET /api/v1/clusters/:cluster_id/rolling-updates/:id/nodes":                           true,
	"GET /api/v1/clusters/:cluster_id/schedules":                                           true,
	"GET /api/v1/clusters/:cluster_id/ssh-credentials":                                     true,
	"GET /api/v1/clusters/:cluster_id/ssh-known-hosts":                                     true,
	"GET /api/v1/clusters/:cluster_id/vm-folders":                                          true,
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/metrics":                                  true,
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/veeam":                                    true,
	"GET /api/v1/clusters/:id":                                                             true,
	"GET /api/v1/favorites":                                                                true,
	"GET /api/v1/guest-snapshots":                                                          true,
	"GET /api/v1/ldap/configs":                                                             true,
	"GET /api/v1/ldap/configs/:id":                                                         true,
	"GET /api/v1/notification-channels":                                                    true,
	"GET /api/v1/notification-channels/:id":                                                true,
	"GET /api/v1/notification-dlq":                                                         true,
	"GET /api/v1/notification-dlq/summary":                                                 true,
	"GET /api/v1/oidc/configs":                                                             true,
	"GET /api/v1/oidc/configs/:id":                                                         true,
	"GET /api/v1/rbac/me/permissions":                                                      true,
	"GET /api/v1/rbac/permissions":                                                         true,
	"GET /api/v1/rbac/roles":                                                               true,
	"GET /api/v1/rbac/roles/:id":                                                           true,
	"GET /api/v1/rbac/users/:user_id/roles":                                                true,
	"GET /api/v1/search":                                                                   true,
	"GET /api/v1/settings":                                                                 true,
	"GET /api/v1/settings/:key":                                                            true,
	"GET /api/v1/settings/branding":                                                        true,
	"GET /api/v1/settings/branding/favicon-file":                                           true,
	"GET /api/v1/settings/branding/logo-file":                                              true,
	"GET /api/v1/tasks":                                                                    true,
	"GET /api/v1/users":                                                                    true,
	"GET /api/v1/users/:id":                                                                true,
	"GET /api/v1/veeam-servers":                                                            true,
	"GET /api/v1/veeam-servers/:id":                                                        true,
	"GET /api/v1/veeam-servers/:id/backup-objects":                                         true,
	"GET /api/v1/veeam-servers/:id/backup-objects/:object_id/restore-points":               true,
	"GET /api/v1/veeam-servers/:id/infrastructure":                                         true,
	"GET /api/v1/veeam-servers/:id/jobs":                                                   true,
	"GET /api/v1/veeam-servers/:id/orphaned-objects":                                       true,
	"GET /api/v1/veeam-servers/:id/platforms":                                              true,
	"GET /api/v1/veeam-servers/:id/repositories":                                           true,
	"GET /api/v1/veeam-servers/:id/repositories/:repository_id/metrics":                    true,
	"GET /api/v1/veeam-servers/:id/sessions":                                               true,
	"GET /api/v1/veeam-servers/:id/sessions/:session_id/logs":                              true,
	"GET /api/v1/veeam-servers/:id/sessions/:session_id/tasks":                             true,
	"GET /api/v1/version":                                                                  true,
	"GET /healthz":                                                                         true,
	"PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id":                             true,
	"POST /api/v1/alert-rules":                                                             true,
	"POST /api/v1/alerts/:id/acknowledge":                                                  true,
	"POST /api/v1/alerts/:id/resolve":                                                      true,
	"POST /api/v1/api-keys":                                                                true,
	"POST /api/v1/audit-log/syslog-test":                                                   true,
	"POST /api/v1/auth/change-password":                                                    true,
	"POST /api/v1/auth/console-token":                                                      true,
	"POST /api/v1/auth/login":                                                              true,
	"POST /api/v1/auth/logout":                                                             true,
	"POST /api/v1/auth/logout-all":                                                         true,
	"POST /api/v1/auth/oidc/token-exchange":                                                true,
	"POST /api/v1/auth/refresh":                                                            true,
	"POST /api/v1/auth/register":                                                           true,
	"POST /api/v1/auth/totp/recovery-codes/regenerate":                                     true,
	"POST /api/v1/auth/totp/setup":                                                         true,
	"POST /api/v1/auth/totp/setup/verify":                                                  true,
	"POST /api/v1/auth/totp/verify-login":                                                  true,
	"POST /api/v1/auth/ws-token":                                                           true,
	"POST /api/v1/clusters":                                                                true,
	"POST /api/v1/clusters/:cluster_id/access/groups":                                      true,
	"POST /api/v1/clusters/:cluster_id/access/roles":                                       true,
	"POST /api/v1/clusters/:cluster_id/access/users":                                       true,
	"POST /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":               true,
	"POST /api/v1/clusters/:cluster_id/acme/accounts":                                      true,
	"POST /api/v1/clusters/:cluster_id/acme/plugins":                                       true,
	"POST /api/v1/clusters/:cluster_id/guest-snapshots/resync":                             true,
	"POST /api/v1/clusters/:cluster_id/maintenance-windows":                                true,
	"POST /api/v1/clusters/:cluster_id/metric-servers":                                     true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":                       true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node/certificates/order":                     true,
	"POST /api/v1/clusters/:cluster_id/pools":                                              true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates":                                    true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/cancel":                         true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/nodes/:node_id/confirm-upgrade": true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/nodes/:node_id/skip":            true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/pause":                          true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/resume":                         true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/start":                          true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/preflight-ha":                       true,
	"POST /api/v1/clusters/:cluster_id/schedules":                                          true,
	"POST /api/v1/clusters/:cluster_id/ssh-credentials/test":                               true,
	"POST /api/v1/clusters/:cluster_id/ssh-known-hosts":                                    true,
	"POST /api/v1/clusters/:cluster_id/vm-folders":                                         true,
	"POST /api/v1/clusters/:id/verify-certificate":                                         true,
	"POST /api/v1/clusters/fetch-fingerprint":                                              true,
	"POST /api/v1/favorites":                                                               true,
	"POST /api/v1/firewall-templates":                                                      true,
	"POST /api/v1/ldap/configs":                                                            true,
	"POST /api/v1/ldap/configs/:id/sync":                                                   true,
	"POST /api/v1/ldap/configs/:id/test":                                                   true,
	"POST /api/v1/notification-channels":                                                   true,
	"POST /api/v1/notification-channels/:id/test":                                          true,
	"POST /api/v1/notification-dlq/:id/dismiss":                                            true,
	"POST /api/v1/notification-dlq/:id/retry":                                              true,
	"POST /api/v1/oidc/configs":                                                            true,
	"POST /api/v1/oidc/configs/:id/test":                                                   true,
	"POST /api/v1/rbac/roles":                                                              true,
	"POST /api/v1/rbac/users/:user_id/roles":                                               true,
	"POST /api/v1/settings/branding/favicon":                                               true,
	"POST /api/v1/settings/branding/logo":                                                  true,
	"POST /api/v1/tasks":                                                                   true,
	"POST /api/v1/veeam-servers":                                                           true,
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/disable":                                  true,
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/enable":                                   true,
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/start":                                    true,
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/stop":                                     true,
	"POST /api/v1/veeam-servers/:id/sessions/:session_id/stop":                             true,
	"POST /api/v1/veeam-servers/:id/test":                                                  true,
	"PUT /api/v1/alert-rules/:id":                                                          true,
	"PUT /api/v1/audit-log/syslog-config":                                                  true,
	"PUT /api/v1/auth/profile":                                                             true,
	"PUT /api/v1/clusters/:cluster_id/access/acl":                                          true,
	"PUT /api/v1/clusters/:cluster_id/access/groups/:groupid":                              true,
	"PUT /api/v1/clusters/:cluster_id/access/roles/:roleid":                                true,
	"PUT /api/v1/clusters/:cluster_id/access/users/:userid":                                true,
	"PUT /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":                true,
	"PUT /api/v1/clusters/:cluster_id/acme/accounts/:name":                                 true,
	"PUT /api/v1/clusters/:cluster_id/acme/plugins/:plugin_id":                             true,
	"PUT /api/v1/clusters/:cluster_id/maintenance-windows/:id":                             true,
	"PUT /api/v1/clusters/:cluster_id/metric-servers/:server_id":                           true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/acme-config":                             true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":                        true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/certificates/renew":                      true,
	"PUT /api/v1/clusters/:cluster_id/pools/:pool_id":                                      true,
	"PUT /api/v1/clusters/:cluster_id/schedules/:id":                                       true,
	"PUT /api/v1/clusters/:cluster_id/ssh-credentials":                                     true,
	"PUT /api/v1/clusters/:cluster_id/vms/:vm_id/folder":                                   true,
	"PUT /api/v1/clusters/:id":                                                             true,
	"PUT /api/v1/firewall-templates/:id":                                                   true,
	"PUT /api/v1/ldap/configs/:id":                                                         true,
	"PUT /api/v1/notification-channels/:id":                                                true,
	"PUT /api/v1/oidc/configs/:id":                                                         true,
	"PUT /api/v1/rbac/roles/:id":                                                           true,
	"PUT /api/v1/settings/:key":                                                            true,
	"PUT /api/v1/tasks/:upid":                                                              true,
	"PUT /api/v1/users/:id":                                                                true,
	"PUT /api/v1/veeam-servers/:id":                                                        true,
	"PUT /api/v1/veeam-servers/:id/backup-objects/:object_id/guest":                        true,
	"PUT /api/v1/veeam-servers/:id/platforms/:platform_id":                                 true,
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
