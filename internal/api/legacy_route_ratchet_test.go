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
// (the 33 VMHandler routes had already left), and now at 439 after Phase
// 6b moved the 62 routes of five cluster-scoped domains — Ceph 17, HA 17,
// DRS 10, CVE 10 and replication 8.
// TestGuard_LegacyRouteSetOnlyShrinks compares the live legacy set
// against it and fails if anything NEW shows up — a route that is not
// here must be added through the registry, not through router.go.
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
	"DELETE /api/v1/clusters/:cluster_id/backup-jobs/:job_id":                              true,
	"DELETE /api/v1/clusters/:cluster_id/firewall/aliases/:name":                           true,
	"DELETE /api/v1/clusters/:cluster_id/firewall/groups/:group":                           true,
	"DELETE /api/v1/clusters/:cluster_id/firewall/groups/:group/rules/:pos":                true,
	"DELETE /api/v1/clusters/:cluster_id/firewall/ipset/:name":                             true,
	"DELETE /api/v1/clusters/:cluster_id/firewall/ipset/:name/entries/:cidr":               true,
	"DELETE /api/v1/clusters/:cluster_id/firewall/rules/:pos":                              true,
	"DELETE /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/update":                  true,
	"DELETE /api/v1/clusters/:cluster_id/maintenance-windows/:id":                          true,
	"DELETE /api/v1/clusters/:cluster_id/metric-servers/:server_id":                        true,
	"DELETE /api/v1/clusters/:cluster_id/networks/:node_name/:iface":                       true,
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node/certificates/revoke":                  true,
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm/:vg_name":              true,
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin/:pool_name":        true,
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs/:pool_name":            true,
	"DELETE /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules/:pos":             true,
	"DELETE /api/v1/clusters/:cluster_id/pools/:pool_id":                                   true,
	"DELETE /api/v1/clusters/:cluster_id/schedules/:id":                                    true,
	"DELETE /api/v1/clusters/:cluster_id/sdn/controllers/:controller":                      true,
	"DELETE /api/v1/clusters/:cluster_id/sdn/dns/:dns":                                     true,
	"DELETE /api/v1/clusters/:cluster_id/sdn/ipams/:ipam":                                  true,
	"DELETE /api/v1/clusters/:cluster_id/sdn/vnets/:vnet":                                  true,
	"DELETE /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets/:subnet":                  true,
	"DELETE /api/v1/clusters/:cluster_id/sdn/zones/:zone":                                  true,
	"DELETE /api/v1/clusters/:cluster_id/ssh-credentials":                                  true,
	"DELETE /api/v1/clusters/:cluster_id/ssh-known-hosts/:id":                              true,
	"DELETE /api/v1/clusters/:cluster_id/storage/:storage_id":                              true,
	"DELETE /api/v1/clusters/:cluster_id/storage/:storage_id/content/*":                    true,
	"DELETE /api/v1/clusters/:cluster_id/vm-folders/:folder_id":                            true,
	"DELETE /api/v1/clusters/:cluster_id/vm-import-sources/:storage":                       true,
	"DELETE /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos":                   true,
	"DELETE /api/v1/clusters/:id":                                                          true,
	"DELETE /api/v1/favorites":                                                             true,
	"DELETE /api/v1/firewall-templates/:id":                                                true,
	"DELETE /api/v1/ldap/configs/:id":                                                      true,
	"DELETE /api/v1/notification-channels/:id":                                             true,
	"DELETE /api/v1/notification-dlq/:id":                                                  true,
	"DELETE /api/v1/oidc/configs/:id":                                                      true,
	"DELETE /api/v1/pbs-servers/:id":                                                       true,
	"DELETE /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots":                       true,
	"DELETE /api/v1/rbac/roles/:id":                                                        true,
	"DELETE /api/v1/rbac/users/:user_id/roles/:id":                                         true,
	"DELETE /api/v1/reports/runs/:id":                                                      true,
	"DELETE /api/v1/reports/schedules/:id":                                                 true,
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
	"GET /api/v1/backup-coverage":                                                          true,
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
	"GET /api/v1/clusters/:cluster_id/appliances":                                          true,
	"GET /api/v1/clusters/:cluster_id/audit-log":                                           true,
	"GET /api/v1/clusters/:cluster_id/backup-jobs":                                         true,
	"GET /api/v1/clusters/:cluster_id/config":                                              true,
	"GET /api/v1/clusters/:cluster_id/config/join":                                         true,
	"GET /api/v1/clusters/:cluster_id/config/nodes":                                        true,
	"GET /api/v1/clusters/:cluster_id/description":                                         true,
	"GET /api/v1/clusters/:cluster_id/firewall/aliases":                                    true,
	"GET /api/v1/clusters/:cluster_id/firewall/groups":                                     true,
	"GET /api/v1/clusters/:cluster_id/firewall/groups/:group/rules":                        true,
	"GET /api/v1/clusters/:cluster_id/firewall/ipset":                                      true,
	"GET /api/v1/clusters/:cluster_id/firewall/ipset/:name/entries":                        true,
	"GET /api/v1/clusters/:cluster_id/firewall/log":                                        true,
	"GET /api/v1/clusters/:cluster_id/firewall/options":                                    true,
	"GET /api/v1/clusters/:cluster_id/firewall/rules":                                      true,
	"GET /api/v1/clusters/:cluster_id/guest-tools/config":                                  true,
	"GET /api/v1/clusters/:cluster_id/guest-tools/guests":                                  true,
	"GET /api/v1/clusters/:cluster_id/maintenance-windows":                                 true,
	"GET /api/v1/clusters/:cluster_id/metric-servers":                                      true,
	"GET /api/v1/clusters/:cluster_id/metric-servers/:server_id":                           true,
	"GET /api/v1/clusters/:cluster_id/metrics":                                             true,
	"GET /api/v1/clusters/:cluster_id/migrations":                                          true,
	"GET /api/v1/clusters/:cluster_id/networks":                                            true,
	"GET /api/v1/clusters/:cluster_id/networks/:node_name":                                 true,
	"GET /api/v1/clusters/:cluster_id/nodes":                                               true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node/acme-config":                             true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":                        true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node/certificates":                            true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node/packages":                                true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/disks":                                true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/metrics":                              true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/network-interfaces":                   true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/pci-devices":                          true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/directory":                    true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/list":                         true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm":                          true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin":                      true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/smart":                        true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs":                          true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/dns":                                true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/log":                       true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules":                     true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/journal":                            true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/report":                             true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/sensors":                            true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/services":                           true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/syslog":                             true,
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/time":                               true,
	"GET /api/v1/clusters/:cluster_id/options":                                             true,
	"GET /api/v1/clusters/:cluster_id/pbs-servers":                                         true,
	"GET /api/v1/clusters/:cluster_id/pools/:pool_id":                                      true,
	"GET /api/v1/clusters/:cluster_id/query-url-metadata":                                  true,
	"GET /api/v1/clusters/:cluster_id/rolling-updates":                                     true,
	"GET /api/v1/clusters/:cluster_id/rolling-updates/:id":                                 true,
	"GET /api/v1/clusters/:cluster_id/rolling-updates/:id/nodes":                           true,
	"GET /api/v1/clusters/:cluster_id/scan/iscsi":                                          true,
	"GET /api/v1/clusters/:cluster_id/schedules":                                           true,
	"GET /api/v1/clusters/:cluster_id/sdn/controllers":                                     true,
	"GET /api/v1/clusters/:cluster_id/sdn/dns":                                             true,
	"GET /api/v1/clusters/:cluster_id/sdn/ipams":                                           true,
	"GET /api/v1/clusters/:cluster_id/sdn/vnets":                                           true,
	"GET /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets":                             true,
	"GET /api/v1/clusters/:cluster_id/sdn/zones":                                           true,
	"GET /api/v1/clusters/:cluster_id/ssh-credentials":                                     true,
	"GET /api/v1/clusters/:cluster_id/ssh-known-hosts":                                     true,
	"GET /api/v1/clusters/:cluster_id/storage":                                             true,
	"GET /api/v1/clusters/:cluster_id/storage/:storage_id/config":                          true,
	"GET /api/v1/clusters/:cluster_id/storage/:storage_id/content":                         true,
	"GET /api/v1/clusters/:cluster_id/tags":                                                true,
	"GET /api/v1/clusters/:cluster_id/virtio-win/config":                                   true,
	"GET /api/v1/clusters/:cluster_id/virtio-win/downloads":                                true,
	"GET /api/v1/clusters/:cluster_id/vm-folders":                                          true,
	"GET /api/v1/clusters/:cluster_id/vm-import-sources":                                   true,
	"GET /api/v1/clusters/:cluster_id/vm-import-sources/content":                           true,
	"GET /api/v1/clusters/:cluster_id/vm-imports":                                          true,
	"GET /api/v1/clusters/:cluster_id/vm-imports/:id":                                      true,
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules":                           true,
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/metrics":                                  true,
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/veeam":                                    true,
	"GET /api/v1/clusters/:id":                                                             true,
	"GET /api/v1/favorites":                                                                true,
	"GET /api/v1/firewall-templates":                                                       true,
	"GET /api/v1/firewall-templates/:id":                                                   true,
	"GET /api/v1/guest-snapshots":                                                          true,
	"GET /api/v1/ldap/configs":                                                             true,
	"GET /api/v1/ldap/configs/:id":                                                         true,
	"GET /api/v1/migrations":                                                               true,
	"GET /api/v1/migrations/:id":                                                           true,
	"GET /api/v1/notification-channels":                                                    true,
	"GET /api/v1/notification-channels/:id":                                                true,
	"GET /api/v1/notification-dlq":                                                         true,
	"GET /api/v1/notification-dlq/summary":                                                 true,
	"GET /api/v1/oidc/configs":                                                             true,
	"GET /api/v1/oidc/configs/:id":                                                         true,
	"GET /api/v1/pbs-servers":                                                              true,
	"GET /api/v1/pbs-servers/:id":                                                          true,
	"GET /api/v1/pbs-servers/:pbs_id/datastores":                                           true,
	"GET /api/v1/pbs-servers/:pbs_id/datastores/:store/config":                             true,
	"GET /api/v1/pbs-servers/:pbs_id/datastores/:store/rrd":                                true,
	"GET /api/v1/pbs-servers/:pbs_id/datastores/status":                                    true,
	"GET /api/v1/pbs-servers/:pbs_id/metrics":                                              true,
	"GET /api/v1/pbs-servers/:pbs_id/prune-jobs":                                           true,
	"GET /api/v1/pbs-servers/:pbs_id/snapshots":                                            true,
	"GET /api/v1/pbs-servers/:pbs_id/sync-jobs":                                            true,
	"GET /api/v1/pbs-servers/:pbs_id/tasks":                                                true,
	"GET /api/v1/pbs-servers/:pbs_id/tasks/:upid":                                          true,
	"GET /api/v1/pbs-servers/:pbs_id/tasks/:upid/log":                                      true,
	"GET /api/v1/pbs-servers/:pbs_id/verify-jobs":                                          true,
	"GET /api/v1/pbs-snapshots":                                                            true,
	"GET /api/v1/rbac/me/permissions":                                                      true,
	"GET /api/v1/rbac/permissions":                                                         true,
	"GET /api/v1/rbac/roles":                                                               true,
	"GET /api/v1/rbac/roles/:id":                                                           true,
	"GET /api/v1/rbac/users/:user_id/roles":                                                true,
	"GET /api/v1/reports/runs":                                                             true,
	"GET /api/v1/reports/runs/:id":                                                         true,
	"GET /api/v1/reports/runs/:id/csv":                                                     true,
	"GET /api/v1/reports/runs/:id/html":                                                    true,
	"GET /api/v1/reports/schedules":                                                        true,
	"GET /api/v1/reports/schedules/:id":                                                    true,
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
	"GET /api/v1/virtio-win/mirror":                                                        true,
	"GET /api/v1/virtio-win/releases":                                                      true,
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
	"POST /api/v1/clusters/:cluster_id/backup":                                             true,
	"POST /api/v1/clusters/:cluster_id/backup-jobs":                                        true,
	"POST /api/v1/clusters/:cluster_id/backup-jobs/:job_id/run":                            true,
	"POST /api/v1/clusters/:cluster_id/firewall-templates/:id/apply":                       true,
	"POST /api/v1/clusters/:cluster_id/firewall/aliases":                                   true,
	"POST /api/v1/clusters/:cluster_id/firewall/groups":                                    true,
	"POST /api/v1/clusters/:cluster_id/firewall/groups/:group/rules":                       true,
	"POST /api/v1/clusters/:cluster_id/firewall/ipset":                                     true,
	"POST /api/v1/clusters/:cluster_id/firewall/ipset/:name/entries":                       true,
	"POST /api/v1/clusters/:cluster_id/firewall/rules":                                     true,
	"POST /api/v1/clusters/:cluster_id/guest-snapshots/resync":                             true,
	"POST /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/detect":                    true,
	"POST /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/update":                    true,
	"POST /api/v1/clusters/:cluster_id/import-metadata":                                    true,
	"POST /api/v1/clusters/:cluster_id/maintenance-windows":                                true,
	"POST /api/v1/clusters/:cluster_id/metric-servers":                                     true,
	"POST /api/v1/clusters/:cluster_id/networks/:node_name":                                true,
	"POST /api/v1/clusters/:cluster_id/networks/:node_name/apply":                          true,
	"POST /api/v1/clusters/:cluster_id/networks/:node_name/revert":                         true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":                       true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node/certificates/order":                     true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/directory":                   true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/initgpt":                     true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvm":                         true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/lvmthin":                     true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/disks/zfs":                         true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/evacuate":                          true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules":                    true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/maintenance":                       true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/reboot":                            true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/services/:service/:action":         true,
	"POST /api/v1/clusters/:cluster_id/nodes/:node_name/shutdown":                          true,
	"POST /api/v1/clusters/:cluster_id/pools":                                              true,
	"POST /api/v1/clusters/:cluster_id/restore":                                            true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates":                                    true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/cancel":                         true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/nodes/:node_id/confirm-upgrade": true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/nodes/:node_id/skip":            true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/pause":                          true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/resume":                         true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/start":                          true,
	"POST /api/v1/clusters/:cluster_id/rolling-updates/preflight-ha":                       true,
	"POST /api/v1/clusters/:cluster_id/schedules":                                          true,
	"POST /api/v1/clusters/:cluster_id/sdn/controllers":                                    true,
	"POST /api/v1/clusters/:cluster_id/sdn/dns":                                            true,
	"POST /api/v1/clusters/:cluster_id/sdn/ipams":                                          true,
	"POST /api/v1/clusters/:cluster_id/sdn/vnets":                                          true,
	"POST /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets":                            true,
	"POST /api/v1/clusters/:cluster_id/sdn/zones":                                          true,
	"POST /api/v1/clusters/:cluster_id/ssh-credentials/test":                               true,
	"POST /api/v1/clusters/:cluster_id/ssh-known-hosts":                                    true,
	"POST /api/v1/clusters/:cluster_id/storage":                                            true,
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/appliances":                     true,
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/download-url":                   true,
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/oci-pull":                       true,
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/upload":                         true,
	"POST /api/v1/clusters/:cluster_id/virtio-win/check":                                   true,
	"POST /api/v1/clusters/:cluster_id/virtio-win/download":                                true,
	"POST /api/v1/clusters/:cluster_id/vm-folders":                                         true,
	"POST /api/v1/clusters/:cluster_id/vm-import-sources/enable-content":                   true,
	"POST /api/v1/clusters/:cluster_id/vm-import-sources/esxi":                             true,
	"POST /api/v1/clusters/:cluster_id/vm-imports":                                         true,
	"POST /api/v1/clusters/:cluster_id/vm-imports/:id/cancel":                              true,
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules":                          true,
	"POST /api/v1/clusters/:id/verify-certificate":                                         true,
	"POST /api/v1/clusters/fetch-fingerprint":                                              true,
	"POST /api/v1/favorites":                                                               true,
	"POST /api/v1/firewall-templates":                                                      true,
	"POST /api/v1/ldap/configs":                                                            true,
	"POST /api/v1/ldap/configs/:id/sync":                                                   true,
	"POST /api/v1/ldap/configs/:id/test":                                                   true,
	"POST /api/v1/migrations":                                                              true,
	"POST /api/v1/migrations/:id/cancel":                                                   true,
	"POST /api/v1/migrations/:id/check":                                                    true,
	"POST /api/v1/migrations/:id/execute":                                                  true,
	"POST /api/v1/notification-channels":                                                   true,
	"POST /api/v1/notification-channels/:id/test":                                          true,
	"POST /api/v1/notification-dlq/:id/dismiss":                                            true,
	"POST /api/v1/notification-dlq/:id/retry":                                              true,
	"POST /api/v1/oidc/configs":                                                            true,
	"POST /api/v1/oidc/configs/:id/test":                                                   true,
	"POST /api/v1/pbs-servers":                                                             true,
	"POST /api/v1/pbs-servers/:pbs_id/datastores/:store/gc":                                true,
	"POST /api/v1/pbs-servers/:pbs_id/datastores/:store/prune":                             true,
	"POST /api/v1/pbs-servers/:pbs_id/sync-jobs/:job_id/run":                               true,
	"POST /api/v1/pbs-servers/:pbs_id/verify-jobs/:job_id/run":                             true,
	"POST /api/v1/rbac/roles":                                                              true,
	"POST /api/v1/rbac/users/:user_id/roles":                                               true,
	"POST /api/v1/reports/generate":                                                        true,
	"POST /api/v1/reports/runs/:id/email":                                                  true,
	"POST /api/v1/reports/schedules":                                                       true,
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
	"PUT /api/v1/clusters/:cluster_id/backup-jobs/:job_id":                                 true,
	"PUT /api/v1/clusters/:cluster_id/description":                                         true,
	"PUT /api/v1/clusters/:cluster_id/firewall/aliases/:name":                              true,
	"PUT /api/v1/clusters/:cluster_id/firewall/groups/:group/rules/:pos":                   true,
	"PUT /api/v1/clusters/:cluster_id/firewall/options":                                    true,
	"PUT /api/v1/clusters/:cluster_id/firewall/rules/:pos":                                 true,
	"PUT /api/v1/clusters/:cluster_id/guest-tools/config":                                  true,
	"PUT /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/policy":                     true,
	"PUT /api/v1/clusters/:cluster_id/maintenance-windows/:id":                             true,
	"PUT /api/v1/clusters/:cluster_id/metric-servers/:server_id":                           true,
	"PUT /api/v1/clusters/:cluster_id/networks/:node_name/:iface":                          true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/acme-config":                             true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/apt/repositories":                        true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node/certificates/renew":                      true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node_name/disks/wipe":                         true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node_name/dns":                                true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node_name/firewall/rules/:pos":                true,
	"PUT /api/v1/clusters/:cluster_id/nodes/:node_name/time":                               true,
	"PUT /api/v1/clusters/:cluster_id/options":                                             true,
	"PUT /api/v1/clusters/:cluster_id/pools/:pool_id":                                      true,
	"PUT /api/v1/clusters/:cluster_id/schedules/:id":                                       true,
	"PUT /api/v1/clusters/:cluster_id/sdn/apply":                                           true,
	"PUT /api/v1/clusters/:cluster_id/sdn/controllers/:controller":                         true,
	"PUT /api/v1/clusters/:cluster_id/sdn/dns/:dns":                                        true,
	"PUT /api/v1/clusters/:cluster_id/sdn/ipams/:ipam":                                     true,
	"PUT /api/v1/clusters/:cluster_id/sdn/vnets/:vnet":                                     true,
	"PUT /api/v1/clusters/:cluster_id/sdn/vnets/:vnet/subnets/:subnet":                     true,
	"PUT /api/v1/clusters/:cluster_id/sdn/zones/:zone":                                     true,
	"PUT /api/v1/clusters/:cluster_id/ssh-credentials":                                     true,
	"PUT /api/v1/clusters/:cluster_id/storage/:storage_id":                                 true,
	"PUT /api/v1/clusters/:cluster_id/tags":                                                true,
	"PUT /api/v1/clusters/:cluster_id/virtio-win/config":                                   true,
	"PUT /api/v1/clusters/:cluster_id/vms/:vm_id/firewall/rules/:pos":                      true,
	"PUT /api/v1/clusters/:cluster_id/vms/:vm_id/folder":                                   true,
	"PUT /api/v1/clusters/:id":                                                             true,
	"PUT /api/v1/firewall-templates/:id":                                                   true,
	"PUT /api/v1/ldap/configs/:id":                                                         true,
	"PUT /api/v1/notification-channels/:id":                                                true,
	"PUT /api/v1/oidc/configs/:id":                                                         true,
	"PUT /api/v1/pbs-servers/:id":                                                          true,
	"PUT /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots/notes":                    true,
	"PUT /api/v1/pbs-servers/:pbs_id/datastores/:store/snapshots/protect":                  true,
	"PUT /api/v1/rbac/roles/:id":                                                           true,
	"PUT /api/v1/reports/schedules/:id":                                                    true,
	"PUT /api/v1/settings/:key":                                                            true,
	"PUT /api/v1/tasks/:upid":                                                              true,
	"PUT /api/v1/users/:id":                                                                true,
	"PUT /api/v1/veeam-servers/:id":                                                        true,
	"PUT /api/v1/veeam-servers/:id/backup-objects/:object_id/guest":                        true,
	"PUT /api/v1/veeam-servers/:id/platforms/:platform_id":                                 true,
	"PUT /api/v1/virtio-win/mirror":                                                        true,
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
