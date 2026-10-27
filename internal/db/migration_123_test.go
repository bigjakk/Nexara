package db

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/migrations"
)

// Fixed ids so a run that aborts before its purge leaves rows the next run's
// up-front purge can find.
var (
	m123Cluster = uuid.MustParse("12300000-0000-4000-8000-000000000001")
	m123Node    = uuid.MustParse("12300000-0000-4000-8000-000000000002")
	m123Other   = uuid.MustParse("12300000-0000-4000-8000-000000000003")
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

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// audit_log, nodes and vms all cascade from the cluster.
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, []uuid.UUID{m123Cluster, m123Other})
	}
	purge()
	defer purge()

	for _, id := range []uuid.UUID{m123Cluster, m123Other} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			 VALUES ($1, $2, 'https://192.0.2.10:8006', 'user@pam!test', 'x')`, id, "cluster-"+id.String()[:12]); err != nil {
			t.Fatalf("seed cluster: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`,
		m123Node, m123Cluster); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// 109 is a container the stale HA row names by its bare VMID.
	if _, err := pool.Exec(ctx, `INSERT INTO vms (cluster_id, node_id, vmid, type) VALUES ($1, $2, 109, 'lxc')`,
		m123Cluster, m123Node); err != nil {
		t.Fatalf("seed vm: %v", err)
	}

	type shape struct{ rtype, rid, action string }
	const px, nx = "proxmox", "nexara"
	const (
		upidSDN  = "UPID:pve-01:0000A1B2:00000001:6A8CE407:reloadnetworkall::root@pam:"
		upidSDN2 = "UPID:pve-01:0000A1B3:00000002:6A8CE408:reloadnetworkall::root@pam!nexara:"
	)
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
		{name: "subnet create it cannot resolve", details: `{"vnet":"vnet03","subnet":"203.0.113.0/24"}`,
			old: shape{"sdn", "vnet03/203.0.113.0/24", "sdn_subnet_created"}, want: shape{"sdn_subnet", "vnet03/203.0.113.0/24", "sdn_subnet_created"}},

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
		{name: "a task with no id, filed under its node, stays", source: px, details: `{"resource_id":""}`, old: shape{"proxmox_task", "pve-01", "cephcreatepool"}},
		{name: "a UPID of another task type stays", source: px, details: `{"resource_id":"osd.5","upid":"UPID:pve-01:0000A1B2:00000001:6A8CE407:qmstart:osd.5:root@pam:"}`,
			old: shape{"proxmox_task", "osd.5", "cephdestroyosd"}},
		{name: "a daemon restart naming no OSD number stays", source: px, details: `{"resource_id":"osd.x"}`, old: shape{"proxmox_task", "osd.x", "srvrestart"}},
		{name: "guest ha task stays", source: px, details: `{"resource_id":"100"}`, old: shape{"proxmox_task", "100", "hastart"}},
		{name: "a row Nexara files today stays", old: shape{"ceph_osd", "12", "osd_in"}},

		// Certificates are the node's.
		{name: "certificate renewal", source: px, details: `{"task_type":"acmerenew","node":"pve-01"}`,
			old: shape{"proxmox_task", "other-id", "acmerenew"}, want: shape{"certificate", "pve-01", "renewed"}},
		{name: "certificate order", source: px, details: `{"task_type":"acmenewcert","node":"pve-01"}`,
			old: shape{"node", "pve-01", "acme_new_cert"}, want: shape{"certificate", "pve-01", "ordered"}},
		{name: "a certificate row Nexara files today stays", old: shape{"certificate", "pve-01", "renewed"}},

		// The SDN apply as the collector ingested it, and Nexara's own beside it.
		{name: "an SDN apply made in Proxmox", source: px, offset: 1 * time.Hour,
			details: `{"task_type":"reloadnetworkall","proxmox_user":"root@pam","node":"pve-01","upid":"` + upidSDN + `"}`,
			old:     shape{"network", "pve-01", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "the nodes' reloads of that apply are parts of it", source: px, offset: 1*time.Hour + 20*time.Second,
			details: `{"task_type":"srvreload","proxmox_user":"root@pam","node":"pve-02"}`,
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-02", "network_reloaded_by_sdn"},
			wantDetail: map[string]string{"parent_upid": upidSDN}},
		{name: "a reload an hour after it is a network apply", source: px, offset: 2*time.Hour + 20*time.Second,
			details: `{"task_type":"srvreload","proxmox_user":"root@pam","node":"pve-03"}`,
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-03", "network_applied"}},
		{name: "another service reload stays", source: px, details: `{"task_type":"srvreload","node":"pve-01"}`, old: shape{"node", "pveproxy", "service_reload"}},

		// Nexara's own applies, and the copies the collector made of them.
		{name: "Nexara's SDN apply", offset: 3 * time.Hour, old: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "its copy, made by Nexara's token, stays", source: px, offset: 3*time.Hour + time.Minute,
			details: `{"task_type":"reloadnetworkall","proxmox_user":"root@pam!nexara","node":"pve-01","upid":"` + upidSDN2 + `"}`,
			old:     shape{"network", "pve-01", "reload"}},
		{name: "a copy made by a person is theirs and moves", source: px, offset: 3*time.Hour + 30*time.Second,
			details: `{"task_type":"reloadnetworkall","proxmox_user":"root@pam","node":"pve-02"}`,
			old:     shape{"network", "pve-02", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "a copy five minutes from Nexara's apply moves", source: px, offset: 3*time.Hour + 5*time.Minute,
			details: `{"task_type":"reloadnetworkall","proxmox_user":"root@pam!nexara","node":"pve-01"}`,
			old:     shape{"network", "pve-01", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "Nexara's network apply of a node", offset: 4 * time.Hour, old: shape{"network", "pve-04", "network_applied"}},
		{name: "its copy, made by Nexara's token, stays too", source: px, offset: 4*time.Hour + 30*time.Second,
			details: `{"task_type":"srvreload","proxmox_user":"root@pam!nexara","node":"pve-04"}`,
			old:     shape{"node", "networking", "service_reload"}},
		{name: "a node apply by a person moves", source: px, offset: 4*time.Hour + 30*time.Second,
			details: `{"task_type":"srvreload","proxmox_user":"root@pam","node":"pve-04"}`,
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-04", "network_applied"}},
		{name: "a copy of another node's apply moves", source: px, offset: 4*time.Hour + 30*time.Second,
			details: `{"task_type":"srvreload","proxmox_user":"root@pam!nexara","node":"pve-05"}`,
			old:     shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-05", "network_applied"}},
		// An apply of Nexara's on ANOTHER cluster is nobody's twin here.
		{name: "another cluster's apply", cluster: m123Other, offset: 5 * time.Hour, old: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "a copy whose only twin is on another cluster moves", source: px, offset: 5*time.Hour + 10*time.Second,
			details: `{"task_type":"reloadnetworkall","proxmox_user":"root@pam!nexara","node":"pve-01"}`,
			old:     shape{"network", "pve-01", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
	}

	ids := make([]uuid.UUID, len(rows))
	for i, tc := range rows {
		ids[i] = uuid.New()
		source, cluster, details := tc.source, tc.cluster, tc.details
		if source == "" {
			source = nx
		}
		if cluster == uuid.Nil {
			cluster = m123Cluster
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

	for pass := 1; pass <= 2; pass++ {
		run("up", up)
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
			switch {
			case moved && (got.rekeyed["resource_type"] != tc.old.rtype || got.rekeyed["resource_id"] != tc.old.rid || got.rekeyed["action"] != tc.old.action):
				t.Errorf("pass %d, %s: rekeyed_from = %v, want the original %+v", pass, tc.name, got.rekeyed, tc.old)
			case !moved && got.rekeyed != nil:
				t.Errorf("pass %d, %s: a row that stayed carries rekeyed_from %v", pass, tc.name, got.rekeyed)
			}
			for k, v := range tc.wantDetail {
				if got.details[k] != v {
					t.Errorf("pass %d, %s: details.%s = %v, want %q", pass, tc.name, k, got.details[k], v)
				}
			}
		}
	}

	// The down puts every moved row back, and takes its marks out.
	run("down", down)
	for i, tc := range rows {
		got := load(i)
		if got.shape != tc.old || got.rekeyed != nil || got.details["parent_upid"] != nil {
			t.Errorf("after the down, %s: %+v (rekeyed %v, parent %v), want %+v with no marks", tc.name, got.shape, got.rekeyed, got.details["parent_upid"], tc.old)
		}
	}
}
