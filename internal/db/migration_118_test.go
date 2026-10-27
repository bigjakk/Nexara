package db

import (
	"context"
	"encoding/json"
	"io/fs"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/migrations"
)

// Fixed ids so a run that aborts before its purge leaves rows the next run's
// up-front purge can find.
var (
	m118Cluster = uuid.MustParse("12300000-0000-4000-8000-000000000001")
	m118Node    = uuid.MustParse("12300000-0000-4000-8000-000000000002")
	m118Other   = uuid.MustParse("12300000-0000-4000-8000-000000000003")
)

// The back-fill that re-files old audit rows under the key objects of the cluster
// page now file under. Each row is an old shape that must move or one that must
// not; a mover records its first original in details.rekeyed_from, a second run
// changes nothing, and the down restores the originals.
func TestMigrationClusterObjectAuditKeys_RefilesOldShapesOnly(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	read := func(pattern string) string {
		t.Helper()
		files, err := fs.Glob(migrations.FS, pattern)
		if err != nil || len(files) != 1 {
			t.Fatalf("the back-fill migration %s: %v, %v", pattern, files, err)
		}
		script, err := fs.ReadFile(migrations.FS, files[0])
		if err != nil {
			t.Fatalf("read %s: %v", files[0], err)
		}
		return string(script)
	}
	up, down := read("*_cluster_object_audit_keys.up.sql"), read("*_cluster_object_audit_keys.down.sql")

	// Each statement that re-files rows leaves one that carries a trace of another origin alone.
	guard := regexp.MustCompile(`\(NOT (\w\.)?details \? 'rekeyed_from' OR (\w\.)?details->'rekeyed_from'->>'by' = 'cluster_object_audit_keys'\)`)
	if updates, guards := strings.Count(up, "UPDATE audit_log"), len(guard.FindAllString(up, -1)); guards != updates {
		t.Errorf("%d update statements and %d guards for a row that carries another origin's rekeyed_from", updates, guards)
	}

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// The audit rows go first: a cluster's delete may only detach them (audit_log.cluster_id is SET NULL);
		// nodes and vms cascade from the cluster.
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE cluster_id = ANY($1)`, []uuid.UUID{m118Cluster, m118Other})
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE resource_type = 'system' AND action = 'migration_notice' AND details->>'migration' = 'cluster_object_audit_keys'`)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{m118Cluster, m118Other})
	}
	purge()
	defer purge()

	for _, id := range []uuid.UUID{m118Cluster, m118Other} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			 VALUES ($1, $2, 'https://192.0.2.10:8006', 'user@pam!test', 'x')`, id, "cluster-"+id.String()[:12]); err != nil {
			t.Fatalf("seed cluster: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`,
		m118Node, m118Cluster); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// 109 is a container the stale HA row names by its bare VMID.
	if _, err := pool.Exec(ctx, `INSERT INTO vms (cluster_id, node_id, vmid, type) VALUES ($1, $2, 109, 'lxc')`,
		m118Cluster, m118Node); err != nil {
		t.Fatalf("seed vm: %v", err)
	}

	type shape struct{ rtype, rid, action string }
	const px, nx = "proxmox", "nexara"
	// A UPID carries the user a task ran as, token included; the task list
	// reports a finished task's user bare, so a row's proxmox_user is bare here.
	upidOf := func(taskType, id, user string) string {
		return "UPID:pve-01:0000A1B2:00000001:6A8CE407:" + taskType + ":" + id + ":" + user + ":"
	}
	taskDetails := func(taskType, id, node, bareUser, upidUser string) string {
		return `{"task_type":"` + taskType + `","proxmox_user":"` + bareUser + `","node":"` + node + `","upid":"` + upidOf(taskType, id, upidUser) + `"}`
	}
	upidSDN := upidOf("reloadnetworkall", "", "root@pam")
	base := time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
	at := func(d time.Duration) time.Time { return base.Add(d) }

	type testRow struct {
		name       string
		source     string // default nexara
		cluster    uuid.UUID
		details    string        // default {}
		offset     time.Duration // created_at, from the start of the scenario
		old, want  shape         // want is zero for a row that must stay
		wantDetail map[string]string
		noDetail   []string // details the row must not carry once re-filed
	}
	rows := []testRow{
		// SDN: a kind per type, and the subnet create filed under the id Proxmox gave.
		{name: "sdn zone", old: shape{"sdn", "dmz01", "sdn_zone_created"}, want: shape{"sdn_zone", "dmz01", "sdn_zone_created"}},
		{name: "sdn vnet, same id", old: shape{"sdn", "dmz01", "sdn_vnet_deleted"}, want: shape{"sdn_vnet", "dmz01", "sdn_vnet_deleted"}},
		{name: "sdn subnet update", offset: time.Hour, old: shape{"sdn", "vnet01/zone01-192.0.2.0-24", "sdn_subnet_updated"}, want: shape{"sdn_subnet", "vnet01/zone01-192.0.2.0-24", "sdn_subnet_updated"}},
		{name: "sdn apply stays", old: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "subnet create, from a later update", details: `{"vnet":"vnet01","subnet":"192.0.2.0/24"}`,
			old: shape{"sdn", "vnet01/192.0.2.0/24", "sdn_subnet_created"}, want: shape{"sdn_subnet", "vnet01/zone01-192.0.2.0-24", "sdn_subnet_created"}},
		{name: "vnet create names its zone", details: `{"vnet":"vnet02","zone":"zone02"}`,
			old: shape{"sdn", "vnet02", "sdn_vnet_created"}, want: shape{"sdn_vnet", "vnet02", "sdn_vnet_created"}},
		{name: "subnet create, from its VNet's zone", offset: time.Minute, details: `{"vnet":"vnet02","subnet":"198.51.100.0/24"}`,
			old: shape{"sdn", "vnet02/198.51.100.0/24", "sdn_subnet_created"}, want: shape{"sdn_subnet", "vnet02/zone02-198.51.100.0-24", "sdn_subnet_created"}},
		{name: "a VNet's create from after the subnet's is not its zone", offset: 2 * time.Hour, details: `{"vnet":"vnet04","zone":"zone04"}`,
			old: shape{"sdn", "vnet04", "sdn_vnet_created"}, want: shape{"sdn_vnet", "vnet04", "sdn_vnet_created"}},
		{name: "subnet create before its VNet's row stays unresolved", details: `{"vnet":"vnet04","subnet":"198.51.100.0/24"}`,
			old: shape{"sdn", "vnet04/198.51.100.0/24", "sdn_subnet_created"}, want: shape{"sdn_subnet", "vnet04/198.51.100.0/24", "sdn_subnet_created"}},
		{name: "subnet create it cannot resolve", details: `{"vnet":"vnet03","subnet":"203.0.113.0/24"}`,
			old: shape{"sdn", "vnet03/203.0.113.0/24", "sdn_subnet_created"}, want: shape{"sdn_subnet", "vnet03/203.0.113.0/24", "sdn_subnet_created"}},
		// A subnet's id is its own edit's, else the zone of its VNet's latest create before it; this cluster's rows only.
		{name: "an edit of another subnet of the VNet is not this one's id", details: `{"vnet":"vnet05","subnet":"192.0.2.0/24"}`,
			old: shape{"sdn", "vnet05/192.0.2.0/24", "sdn_subnet_created"}, want: shape{"sdn_subnet", "vnet05/192.0.2.0/24", "sdn_subnet_created"}},
		{name: "that other subnet's edit", offset: time.Minute,
			old: shape{"sdn", "vnet05/zone05-198.51.100.0-24", "sdn_subnet_updated"}, want: shape{"sdn_subnet", "vnet05/zone05-198.51.100.0-24", "sdn_subnet_updated"}},
		{name: "another cluster's subnet create has neither this cluster's edit nor its VNet", cluster: m118Other, offset: 2 * time.Minute, details: `{"vnet":"vnet06","subnet":"192.0.2.0/24"}`,
			old: shape{"sdn", "vnet06/192.0.2.0/24", "sdn_subnet_created"}, want: shape{"sdn_subnet", "vnet06/192.0.2.0/24", "sdn_subnet_created"}},
		{name: "this cluster's VNet of that name", details: `{"vnet":"vnet06","zone":"zone06"}`,
			old: shape{"sdn", "vnet06", "sdn_vnet_created"}, want: shape{"sdn_vnet", "vnet06", "sdn_vnet_created"}},
		{name: "this cluster's edit of that subnet", offset: time.Minute,
			old: shape{"sdn", "vnet06/zone06-192.0.2.0-24", "sdn_subnet_updated"}, want: shape{"sdn_subnet", "vnet06/zone06-192.0.2.0-24", "sdn_subnet_updated"}},
		{name: "a VNet's first create", offset: 10 * time.Minute, details: `{"vnet":"vnet07","zone":"zone07a"}`,
			old: shape{"sdn", "vnet07", "sdn_vnet_created"}, want: shape{"sdn_vnet", "vnet07", "sdn_vnet_created"}},
		{name: "its create in another zone", offset: 11 * time.Minute, details: `{"vnet":"vnet07","zone":"zone07b"}`,
			old: shape{"sdn", "vnet07", "sdn_vnet_created"}, want: shape{"sdn_vnet", "vnet07", "sdn_vnet_created"}},
		{name: "its latest create names no zone", offset: 11*time.Minute + 30*time.Second, details: `{"vnet":"vnet07"}`,
			old: shape{"sdn", "vnet07", "sdn_vnet_created"}, want: shape{"sdn_vnet", "vnet07", "sdn_vnet_created"}},
		{name: "a subnet after all three takes the latest zone named", offset: 12 * time.Minute, details: `{"vnet":"vnet07","subnet":"192.0.2.0/24"}`,
			old: shape{"sdn", "vnet07/192.0.2.0/24", "sdn_subnet_created"}, want: shape{"sdn_subnet", "vnet07/zone07b-192.0.2.0-24", "sdn_subnet_created"}},
		{name: "a VNet whose subnet was later edited under another zone", offset: 10 * time.Minute, details: `{"vnet":"vnet08","zone":"zone08a"}`,
			old: shape{"sdn", "vnet08", "sdn_vnet_created"}, want: shape{"sdn_vnet", "vnet08", "sdn_vnet_created"}},
		{name: "its subnet takes the id the edit carries, not the VNet's zone", offset: 12 * time.Minute, details: `{"vnet":"vnet08","subnet":"192.0.2.0/24"}`,
			old: shape{"sdn", "vnet08/192.0.2.0/24", "sdn_subnet_created"}, want: shape{"sdn_subnet", "vnet08/zone08b-192.0.2.0-24", "sdn_subnet_created"}},
		{name: "the edit", offset: 13 * time.Minute,
			old: shape{"sdn", "vnet08/zone08b-192.0.2.0-24", "sdn_subnet_updated"}, want: shape{"sdn_subnet", "vnet08/zone08b-192.0.2.0-24", "sdn_subnet_updated"}},

		// Firewall: rules by their set, the cluster's options, names folded to lower case.
		{name: "cluster rule create", old: shape{"network", "cluster", "firewall_rule_created"}, want: shape{"firewall_rule", "cluster", "firewall_rule_created"}},
		{name: "cluster rule by position", old: shape{"network", "cluster/rule/3", "firewall_rule_updated"}, want: shape{"firewall_rule", "cluster", "firewall_rule_updated"}},
		{name: "guest rule by position", old: shape{"network", "vm/100/rule/0", "firewall_rule_deleted"}, want: shape{"firewall_rule", "vm/100", "firewall_rule_deleted"}},
		{name: "a rule of the first releases", old: shape{"firewall", "cluster/rule/1", "firewall_rule_updated"}, want: shape{"firewall_rule", "cluster", "firewall_rule_updated"}},
		{name: "a rule id it does not know stays", old: shape{"network", "pve-01/rule/0", "firewall_rule_created"}},
		{name: "cluster firewall options", old: shape{"network", "cluster/options", "firewall_options_updated"}, want: shape{"firewall_options", "cluster", "firewall_options_updated"}},
		{name: "options of the first releases", old: shape{"firewall", "cluster/options", "firewall_options_updated"}, want: shape{"firewall_options", "cluster", "firewall_options_updated"}},
		{name: "a guest's options stay", old: shape{"network", "vm/100/options", "firewall_options_updated"}},
		{name: "interface stays", old: shape{"network", "pve-01/vmbr0", "interface_created"}},
		{name: "node firewall rule create", old: shape{"node", "pve-01", "create_firewall_rule"}, want: shape{"firewall_rule", "node/pve-01", "firewall_rule_created"}},
		{name: "node firewall rule update", old: shape{"node", "pve-01", "update_firewall_rule"}, want: shape{"firewall_rule", "node/pve-01", "firewall_rule_updated"}},
		{name: "node firewall rule delete", old: shape{"node", "pve-02", "delete_firewall_rule"}, want: shape{"firewall_rule", "node/pve-02", "firewall_rule_deleted"}},
		{name: "another action on a node stays", old: shape{"node", "pve-01", "start_all"}},
		{name: "alias folded", old: shape{"firewall_alias", "MyNet", "created"}, want: shape{"firewall_alias", "mynet", "created"}},
		{name: "ip set folded", old: shape{"firewall_ipset", "MySet", "created"}, want: shape{"firewall_ipset", "myset", "created"}},
		{name: "ip set entry, the set folded", old: shape{"firewall_ipset_entry", "MySet/192.0.2.5/24", "created"}, want: shape{"firewall_ipset_entry", "myset/192.0.2.5/24", "created"}},
		{name: "ip set entry naming an alias", old: shape{"firewall_ipset_entry", "MySet/dc/MyNet", "created"}, want: shape{"firewall_ipset_entry", "myset/dc/MyNet", "created"}},
		{name: "security group folded", old: shape{"firewall_security_group", "Web01", "created"}, want: shape{"firewall_security_group", "web01", "created"}},
		{name: "security group rule folded", old: shape{"firewall_security_group_rule", "Web01", "rule_created"}, want: shape{"firewall_security_group_rule", "web01", "rule_created"}},
		{name: "a name already lower case stays", old: shape{"firewall_ipset", "myset", "deleted"}},

		// Access control.
		{name: "acl collapsed", old: shape{"pve_acl", "/vms//100/", "granted"}, want: shape{"pve_acl", "/vms/100", "granted"}},
		{name: "acl root stays", old: shape{"pve_acl", "/", "granted"}},
		{name: "acl double root", old: shape{"pve_acl", "//", "revoked"}, want: shape{"pve_acl", "/", "revoked"}},

		// HA: a bare VMID is not resolved from today's guest, which may be another one.
		{name: "ha bare vmid of a guest that exists stays", old: shape{"ha_resource", "109", "created"}},
		{name: "ha sid stays", old: shape{"ha_resource", "vm:110", "deleted"}},

		// Tasks the collector ingested: Ceph objects, only under ids Proxmox mints.
		{name: "osd destroy", source: px, details: `{"resource_id":"osd.12","upid":"UPID:pve-01:0000A1B2:00000001:6A8CE407:cephdestroyosd:osd.12:root@pam:"}`,
			old: shape{"proxmox_task", "osd.12", "cephdestroyosd"}, want: shape{"ceph_osd", "12", "osd_destroy"}},
		{name: "osd create names the device", source: px, details: `{"resource_id":"sdb"}`,
			old: shape{"proxmox_task", "sdb", "cephcreateosd"}, want: shape{"ceph_osd", "sdb", "osd_create"}},
		{name: "manager", source: px, details: `{"resource_id":"mgr.pve-01"}`,
			old: shape{"proxmox_task", "mgr.pve-01", "cephcreatemgr"}, want: shape{"ceph_mgr", "pve-01", "mgr_create"}},
		{name: "pool", source: px, details: `{"resource_id":"pool01"}`,
			old: shape{"proxmox_task", "pool01", "cephcreatepool"}, want: shape{"ceph_pool", "pool01", "create"}},
		{name: "osd daemon restart", source: px, details: `{"resource_id":"osd.3"}`,
			old: shape{"proxmox_task", "osd.3", "srvrestart"}, want: shape{"ceph_osd", "3", "osd_restart"}},
		{name: "node service restart stays", source: px, details: `{"resource_id":"pveproxy"}`, old: shape{"proxmox_task", "pveproxy", "srvrestart"}},
		{name: "an osd number that is not one stays", source: px, details: `{"resource_id":"osd.abc"}`, old: shape{"proxmox_task", "osd.abc", "cephdestroyosd"}},
		{name: "a manager id outside Proxmox's pattern stays", source: px, details: `{"resource_id":"mgr.pve_01"}`, old: shape{"proxmox_task", "mgr.pve_01", "cephcreatemgr"}},
		{name: "a pool name Proxmox would not let be created stays", source: px, details: `{"resource_id":"pool/01"}`, old: shape{"proxmox_task", "pool/01", "cephcreatepool"}},
		{name: "a pool name with a format character stays", source: px, details: `{"resource_id":"pool\u200b01"}`, old: shape{"proxmox_task", "pool\u200b01", "cephcreatepool"}},
		{name: "an osd made on a device under a directory", source: px, details: `{"resource_id":"cciss/c0d0"}`,
			old: shape{"proxmox_task", "cciss/c0d0", "cephcreateosd"}, want: shape{"ceph_osd", "cciss/c0d0", "osd_create"}},
		{name: "a task with no id, filed under its node, stays", source: px, details: `{"resource_id":""}`, old: shape{"proxmox_task", "pve-01", "cephcreatepool"}},
		{name: "a UPID of another task type stays", source: px, details: `{"resource_id":"osd.5","upid":"UPID:pve-01:0000A1B2:00000001:6A8CE407:qmstart:osd.5:root@pam:"}`,
			old: shape{"proxmox_task", "osd.5", "cephdestroyosd"}},
		{name: "a manager restart with an id outside Proxmox's pattern stays", source: px, details: `{"resource_id":"mgr.pve_01"}`, old: shape{"proxmox_task", "mgr.pve_01", "srvrestart"}},
		{name: "a daemon restart naming no OSD number stays", source: px, details: `{"resource_id":"osd.x"}`, old: shape{"proxmox_task", "osd.x", "srvrestart"}},
		{name: "guest ha task stays", source: px, details: `{"resource_id":"100"}`, old: shape{"proxmox_task", "100", "hastart"}},
		{name: "a row Nexara files today stays", old: shape{"ceph_osd", "12", "osd_in"}},

		// Certificates are the node's.
		{name: "certificate renewal", source: px, details: `{"task_type":"acmerenew","node":"pve-01"}`,
			old: shape{"proxmox_task", "other-id", "acmerenew"}, want: shape{"certificate", "pve-01", "renewed"}},
		{name: "certificate order", source: px, details: `{"task_type":"acmenewcert","node":"pve-01"}`,
			old: shape{"node", "pve-01", "acme_new_cert"}, want: shape{"certificate", "pve-01", "ordered"}},
		{name: "a certificate row Nexara files today stays", old: shape{"certificate", "pve-01", "renewed"}},

		// The SDN apply as the collector ingested it, and the nodes' reloads of it.
		{name: "an SDN apply made in Proxmox", source: px, offset: time.Hour,
			details: taskDetails("reloadnetworkall", "", "pve-01", "root@pam", "root@pam"),
			old:     shape{"network", "pve-01", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "a reload by root@pam during it is annotated with it", source: px, offset: time.Hour + 20*time.Second,
			details: taskDetails("srvreload", "networking", "pve-02", "root@pam", "root@pam"),
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-02", "network_applied"},
			wantDetail: map[string]string{"parent_upid": upidSDN}},
		{name: "a person's reload in the same minute is not", source: px, offset: time.Hour + 30*time.Second,
			details: taskDetails("srvreload", "networking", "pve-03", "admin@pam", "admin@pam"),
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-03", "network_applied"}, noDetail: []string{"parent_upid"}},
		{name: "a token's reload in the same minute is not", source: px, offset: time.Hour + 40*time.Second,
			details: taskDetails("srvreload", "networking", "pve-03", "root@pam", "root@pam!tool"),
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-03", "network_applied"}, noDetail: []string{"parent_upid"}},
		{name: "a reload just before the apply started is not", source: px, offset: time.Hour - 5*time.Second,
			details: taskDetails("srvreload", "networking", "pve-03", "root@pam", "root@pam"),
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-03", "network_applied"}, noDetail: []string{"parent_upid"}},
		{name: "a reload just inside the apply's assumed end is annotated", source: px, offset: time.Hour + 15*time.Minute + 5*time.Second,
			details: taskDetails("srvreload", "networking", "pve-02", "root@pam", "root@pam"),
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-02", "network_applied"},
			wantDetail: map[string]string{"parent_upid": upidSDN}},
		{name: "a reload just past it is not", source: px, offset: time.Hour + 15*time.Minute + 30*time.Second,
			details: taskDetails("srvreload", "networking", "pve-03", "root@pam", "root@pam"),
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-03", "network_applied"}, noDetail: []string{"parent_upid"}},
		{name: "a reload an hour after it is not", source: px, offset: 2*time.Hour + 20*time.Second,
			details: taskDetails("srvreload", "networking", "pve-03", "root@pam", "root@pam"),
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-03", "network_applied"}, noDetail: []string{"parent_upid"}},
		{name: "another service reload stays", source: px, details: `{"task_type":"srvreload","node":"pve-01"}`, old: shape{"node", "pveproxy", "service_reload"}},

		// Nexara's own applies, and the copies the collector made of them: a copy by a
		// token of the user the cluster is configured with (user@pam!test), whose user
		// is in the UPID and bare in the row.
		{name: "Nexara's SDN apply", offset: 3 * time.Hour, old: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "its copy, by a token of the cluster's user, stays", source: px, offset: 3*time.Hour + time.Minute,
			details: taskDetails("reloadnetworkall", "", "pve-01", "user@pam", "user@pam!token01"),
			old:     shape{"network", "pve-01", "reload"}},
		{name: "its copy, by the configured token itself, stays", source: px, offset: 3*time.Hour + 10*time.Second,
			details: taskDetails("reloadnetworkall", "", "pve-01", "user@pam", "user@pam!test"),
			old:     shape{"network", "pve-01", "reload"}},
		{name: "a copy by an earlier token of that user stays too", source: px, offset: 3*time.Hour + 90*time.Second,
			details: taskDetails("reloadnetworkall", "", "pve-01", "user@pam", "user@pam!earlier"),
			old:     shape{"network", "pve-01", "reload"}},
		{name: "a copy by a person is theirs and moves", source: px, offset: 3*time.Hour + 30*time.Second,
			details: taskDetails("reloadnetworkall", "", "pve-02", "root@pam", "root@pam"),
			old:     shape{"network", "pve-02", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "a copy by the cluster's user without a token moves", source: px, offset: 3*time.Hour + 40*time.Second,
			details: taskDetails("reloadnetworkall", "", "pve-02", "user@pam", "user@pam"),
			old:     shape{"network", "pve-02", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "a copy by another tool's token moves", source: px, offset: 3*time.Hour + 20*time.Second,
			details: taskDetails("reloadnetworkall", "", "pve-02", "other@pam", "other@pam!tool"),
			old:     shape{"network", "pve-02", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "a copy five minutes from Nexara's apply moves", source: px, offset: 3*time.Hour + 5*time.Minute,
			details: taskDetails("reloadnetworkall", "", "pve-01", "user@pam", "user@pam!token01"),
			old:     shape{"network", "pve-01", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "Nexara's network apply of a node", offset: 4 * time.Hour, old: shape{"network", "pve-04", "network_applied"}},
		{name: "its copy, by a token of the cluster's user, stays too", source: px, offset: 4*time.Hour + 30*time.Second,
			details: taskDetails("srvreload", "networking", "pve-04", "user@pam", "user@pam!token01"),
			old:     shape{"node", "networking", "service_reload"}},
		{name: "its copy, by the configured token itself, stays as well", source: px, offset: 4*time.Hour + 10*time.Second,
			details: taskDetails("srvreload", "networking", "pve-04", "user@pam", "user@pam!test"),
			old:     shape{"node", "networking", "service_reload"}},
		{name: "a node apply by a person moves", source: px, offset: 4*time.Hour + 30*time.Second,
			details: taskDetails("srvreload", "networking", "pve-04", "root@pam", "root@pam"),
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-04", "network_applied"}},
		{name: "a copy of another node's apply moves", source: px, offset: 4*time.Hour + 30*time.Second,
			details: taskDetails("srvreload", "networking", "pve-05", "user@pam", "user@pam!token01"),
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-05", "network_applied"}},
		// An apply of Nexara's on ANOTHER cluster is nobody's twin here.
		{name: "another cluster's apply", cluster: m118Other, offset: 5 * time.Hour, old: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "a copy whose only twin is on another cluster moves", source: px, offset: 5*time.Hour + 10*time.Second,
			details: taskDetails("reloadnetworkall", "", "pve-01", "user@pam", "user@pam!token01"),
			old:     shape{"network", "pve-01", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},

		// A trace of another origin is the row's first original: the row stays as it is, trace and all.
		{name: "a row another migration's trace is on is not re-filed",
			details: `{"rekeyed_from":{"by":"another_migration","resource_type":"x","resource_id":"y","action":"z"}}`,
			old:     shape{"sdn", "dmz02", "sdn_zone_created"}},
		{name: "nor is one with a trace that names no migration",
			details: `{"rekeyed_from":{"resource_type":"x","resource_id":"y","action":"z"}}`,
			old:     shape{"sdn", "dmz03", "sdn_zone_created"}},
		{name: "an apply's copy with another origin's trace and no twin stays, and is not counted", source: px, offset: 6 * time.Hour,
			details: strings.TrimSuffix(taskDetails("reloadnetworkall", "", "pve-01", "user@pam", "user@pam!token01"), "}") +
				`,"rekeyed_from":{"by":"another_migration","resource_type":"x","resource_id":"y","action":"z"}}`,
			old: shape{"network", "pve-01", "reload"}},
		{name: "an apply's copy by another tool with another origin's trace stays, and is not counted", source: px, offset: 3*time.Hour + 15*time.Second,
			details: strings.TrimSuffix(taskDetails("reloadnetworkall", "", "pve-02", "other@pam", "other@pam!tool"), "}") +
				`,"rekeyed_from":{"by":"another_migration","resource_type":"x","resource_id":"y","action":"z"}}`,
			old: shape{"network", "pve-02", "reload"}},
		{name: "a row this one has no business with keeps its trace",
			details: `{"rekeyed_from":{"by":"another_migration","resource_type":"x","resource_id":"y","action":"z"}}`,
			old:     shape{"vm", "100", "start"}},
	}

	ids := make([]uuid.UUID, len(rows))
	for i, tc := range rows {
		ids[i] = uuid.New()
		source, cluster, details := tc.source, tc.cluster, tc.details
		if source == "" {
			source = nx
		}
		if cluster == uuid.Nil {
			cluster = m118Cluster
		}
		if details == "" {
			details = `{}`
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (id, cluster_id, resource_type, resource_id, action, details, source, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)`,
			ids[i], cluster, tc.old.rtype, tc.old.rid, tc.old.action, details, source, at(tc.offset)); err != nil {
			t.Fatalf("seed %s: %v", tc.name, err)
		}
	}

	type state struct {
		shape
		rekeyed map[string]string
		details map[string]any
	}
	load := func(i int) state {
		t.Helper()
		var s state
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT resource_type, resource_id, action, details FROM audit_log WHERE id = $1`, ids[i]).
			Scan(&s.rtype, &s.rid, &s.action, &raw); err != nil {
			t.Fatalf("read %s: %v", rows[i].name, err)
		}
		if err := json.Unmarshal(raw, &s.details); err != nil {
			t.Fatalf("details of %s: %v", rows[i].name, err)
		}
		if r, ok := s.details["rekeyed_from"].(map[string]any); ok {
			s.rekeyed = map[string]string{}
			for k, v := range r {
				s.rekeyed[k], _ = v.(string)
			}
		}
		return s
	}
	run := func(what, script string) {
		t.Helper()
		if _, err := pool.Exec(ctx, script); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	// What the migration left unmoved on the user's word alone it counts in one audit row, for
	// global auditors: no cluster, no user. The count is -1 when there is no such row.
	noticeRows := func() (rows, copies int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*), COALESCE(max((details->>'copies')::int), -1)
			FROM audit_log WHERE resource_type = 'system' AND action = 'migration_notice' AND cluster_id IS NULL AND user_id IS NULL
			AND details->>'migration' = 'cluster_object_audit_keys'`).Scan(&rows, &copies); err != nil {
			t.Fatalf("read the migration's notice: %v", err)
		}
		return rows, copies
	}

	// A trace a row came with is the row's first original: it survives the up and the down as it was.
	seededTrace := func(tc testRow) any {
		var seeded map[string]any
		_ = json.Unmarshal([]byte(tc.details), &seeded)
		return seeded["rekeyed_from"]
	}
	for pass := 1; pass <= 2; pass++ {
		run("up", up)
		// Two copies of an SDN apply and one of a node's stay on the user's word alone (a token of
		// the cluster's user that is not the configured one); the configured token's do not count.
		// A second run adds no second row.
		if rows, copies := noticeRows(); rows != 1 || copies != 3 {
			t.Errorf("pass %d: %d notice rows counting %d copies, want one counting 3", pass, rows, copies)
		}
		for i, tc := range rows {
			got, moved := load(i), tc.want != (shape{})
			want := tc.old
			if moved {
				want = tc.want
			}
			if got.shape != want {
				t.Errorf("pass %d, %s: row is %+v, want %+v", pass, tc.name, got.shape, want)
			}
			// A row that moved says what it was, from its first filing; one that did not carries no mark.
			// Tagged with this migration's name; one that stayed carries none of ours.
			ours := got.rekeyed["by"] == "cluster_object_audit_keys"
			switch {
			case moved && (!ours || got.rekeyed["resource_type"] != tc.old.rtype || got.rekeyed["resource_id"] != tc.old.rid || got.rekeyed["action"] != tc.old.action):
				t.Errorf("pass %d, %s: rekeyed_from = %v, want the original %+v tagged by the migration", pass, tc.name, got.rekeyed, tc.old)
			case !moved && ours:
				t.Errorf("pass %d, %s: a row that stayed carries this migration's rekeyed_from %v", pass, tc.name, got.rekeyed)
			}
			if seeded := seededTrace(tc); seeded != nil && !reflect.DeepEqual(got.details["rekeyed_from"], seeded) {
				t.Errorf("pass %d, %s: rekeyed_from = %v, want the one the row came with, %v", pass, tc.name, got.details["rekeyed_from"], seeded)
			}
			for k, v := range tc.wantDetail {
				if got.details[k] != v {
					t.Errorf("pass %d, %s: details.%s = %v, want %q", pass, tc.name, k, got.details[k], v)
				}
			}
			for _, k := range tc.noDetail {
				if _, has := got.details[k]; has {
					t.Errorf("pass %d, %s: details carries %s, which this row must not", pass, tc.name, k)
				}
			}
		}
	}

	// The down puts every moved row back, and takes its marks out.
	run("down", down)
	if rows, _ := noticeRows(); rows != 0 {
		t.Errorf("%d notice rows after the down, want none", rows)
	}
	for i, tc := range rows {
		got := load(i)
		if got.shape != tc.old || got.details["parent_upid"] != nil || !reflect.DeepEqual(got.details["rekeyed_from"], seededTrace(tc)) {
			t.Errorf("after the down, %s: %+v (rekeyed %v, parent %v), want %+v with no mark of this migration and a trace the row came with kept", tc.name, got.shape, got.details["rekeyed_from"], got.details["parent_upid"], tc.old)
		}
	}
}
