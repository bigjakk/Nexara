package db

import (
	"context"
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
)

// TestMigrationClusterObjectAuditKeys_RefilesOldShapesOnly is the data lock for
// the back-fill that moves existing audit rows to the key an object of the
// cluster page is filed under from now on. The statements run against a
// head-schema database, not through the migrator, so a renumbering cannot
// break it; the migration chain test proves they apply in order. Each row is
// an old shape that must move, or a shape that must not: rows Nexara writes
// today, a node's own service reload, a guest's HA task. A second run changes
// nothing.
func TestMigrationClusterObjectAuditKeys_RefilesOldShapesOnly(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	files, err := fs.Glob(migrations.FS, "*_cluster_object_audit_keys.up.sql")
	if err != nil || len(files) != 1 {
		t.Fatalf("the back-fill migration: %v, %v", files, err)
	}
	script, err := fs.ReadFile(migrations.FS, files[0])
	if err != nil {
		t.Fatalf("read %s: %v", files[0], err)
	}

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// audit_log, nodes and vms all cascade from the cluster.
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, m123Cluster)
	}
	purge()
	defer purge()

	if _, err := pool.Exec(ctx,
		`INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		 VALUES ($1, 'cluster01', 'https://192.0.2.10:8006', 'user@pam!test', 'x')`, m123Cluster); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO nodes (id, cluster_id, name) VALUES ($1, $2, 'pve-01')`,
		m123Node, m123Cluster); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// 109 is a container and 110 a VM, to resolve a bare HA sid either way.
	for vmid, kind := range map[int]string{109: "lxc", 110: "qemu"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO vms (cluster_id, node_id, vmid, type) VALUES ($1, $2, $3, $4)`,
			m123Cluster, m123Node, vmid, kind); err != nil {
			t.Fatalf("seed vm %d: %v", vmid, err)
		}
	}

	type shape struct{ rtype, rid, action string }
	const px, nx = "proxmox", "nexara"
	ids := map[string]uuid.UUID{}
	wants := map[string]shape{}
	for _, tc := range []struct {
		name         string
		source       string
		details      string
		old, want    shape
		wantStayedAs bool // the row must come out exactly as it went in
	}{
		{name: "sdn zone", source: nx, old: shape{"sdn", "dmz01", "sdn_zone_created"}, want: shape{"sdn_zone", "dmz01", "sdn_zone_created"}},
		{name: "sdn vnet, same id", source: nx, old: shape{"sdn", "dmz01", "sdn_vnet_deleted"}, want: shape{"sdn_vnet", "dmz01", "sdn_vnet_deleted"}},
		{name: "sdn subnet", source: nx, old: shape{"sdn", "vnet01/zone01-192.0.2.0-24", "sdn_subnet_updated"}, want: shape{"sdn_subnet", "vnet01/zone01-192.0.2.0-24", "sdn_subnet_updated"}},
		{name: "sdn apply stays", source: nx, old: shape{"sdn", "cluster", "sdn_applied"}, wantStayedAs: true},
		{name: "cluster rule create", source: nx, old: shape{"network", "cluster", "firewall_rule_created"}, want: shape{"firewall_rule", "cluster", "firewall_rule_created"}},
		{name: "cluster rule by position", source: nx, old: shape{"network", "cluster/rule/3", "firewall_rule_updated"}, want: shape{"firewall_rule", "cluster", "firewall_rule_updated"}},
		{name: "guest rule by position", source: nx, old: shape{"network", "vm/100/rule/0", "firewall_rule_deleted"}, want: shape{"firewall_rule", "vm/100", "firewall_rule_deleted"}},
		{name: "firewall options", source: nx, old: shape{"network", "cluster/options", "firewall_options_updated"}, want: shape{"firewall_options", "cluster", "firewall_options_updated"}},
		{name: "interface stays", source: nx, old: shape{"network", "pve-01/vmbr0", "interface_created"}, wantStayedAs: true},
		{name: "alias folded", source: nx, old: shape{"firewall_alias", "MyNet", "created"}, want: shape{"firewall_alias", "mynet", "created"}},
		{name: "acl collapsed", source: nx, old: shape{"pve_acl", "/vms//100/", "granted"}, want: shape{"pve_acl", "/vms/100", "granted"}},
		{name: "acl root stays", source: nx, old: shape{"pve_acl", "/", "granted"}, wantStayedAs: true},
		{name: "acl double root", source: nx, old: shape{"pve_acl", "//", "revoked"}, want: shape{"pve_acl", "/", "revoked"}},
		{name: "ha bare vmid of a container", source: nx, old: shape{"ha_resource", "109", "created"}, want: shape{"ha_resource", "ct:109", "created"}},
		{name: "ha bare vmid of a vm", source: nx, old: shape{"ha_resource", "110", "updated"}, want: shape{"ha_resource", "vm:110", "updated"}},
		{name: "ha bare vmid of a gone guest stays", source: nx, old: shape{"ha_resource", "999", "created"}, wantStayedAs: true},
		{name: "ha sid stays", source: nx, old: shape{"ha_resource", "vm:110", "deleted"}, wantStayedAs: true},
		{name: "osd destroy", source: px, old: shape{"proxmox_task", "osd.12", "cephdestroyosd"}, want: shape{"ceph_osd", "12", "osd_destroy"}},
		{name: "osd create names the device", source: px, old: shape{"proxmox_task", "sdb", "cephcreateosd"}, want: shape{"ceph_osd", "sdb", "osd_create"}},
		{name: "manager", source: px, old: shape{"proxmox_task", "mgr.pve-01", "cephcreatemgr"}, want: shape{"ceph_mgr", "pve-01", "mgr_create"}},
		{name: "pool", source: px, old: shape{"proxmox_task", "pool01", "cephcreatepool"}, want: shape{"ceph_pool", "pool01", "create"}},
		{name: "osd daemon restart", source: px, old: shape{"proxmox_task", "osd.3", "srvrestart"}, want: shape{"ceph_osd", "3", "osd_restart"}},
		{name: "node service restart stays", source: px, old: shape{"proxmox_task", "pveproxy", "srvrestart"}, wantStayedAs: true},
		{name: "certificate renewal", source: px, details: `{"task_type":"acmerenew"}`, old: shape{"proxmox_task", "pve-01", "acmerenew"}, want: shape{"certificate", "pve-01", "renewed"}},
		{name: "certificate order", source: px, details: `{"task_type":"acmenewcert"}`, old: shape{"node", "pve-01", "acme_new_cert"}, want: shape{"certificate", "pve-01", "ordered"}},
		{name: "sdn apply ingested", source: px, details: `{"task_type":"reloadnetworkall"}`, old: shape{"network", "pve-01", "reload"}, want: shape{"sdn", "cluster", "sdn_applied"}},
		{name: "network apply ingested", source: px, details: `{"task_type":"srvreload","node":"pve-01"}`, old: shape{"node", "networking", "service_reload"}, want: shape{"network", "pve-01", "network_applied"}},
		{name: "another service reload stays", source: px, details: `{"task_type":"srvreload","node":"pve-01"}`, old: shape{"node", "pveproxy", "service_reload"}, wantStayedAs: true},
		{name: "guest ha task stays", source: px, old: shape{"proxmox_task", "100", "hastart"}, wantStayedAs: true},
		{name: "a row Nexara files today stays", source: nx, old: shape{"ceph_osd", "12", "osd_in"}, wantStayedAs: true},
		{name: "a certificate row Nexara files today stays", source: nx, old: shape{"certificate", "pve-01", "renewed"}, wantStayedAs: true},
	} {
		ids[tc.name] = uuid.New()
		details := tc.details
		if details == "" {
			details = `{}`
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO audit_log (id, cluster_id, resource_type, resource_id, action, details, source)
			 VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)`,
			ids[tc.name], m123Cluster, tc.old.rtype, tc.old.rid, tc.old.action, details, tc.source); err != nil {
			t.Fatalf("seed %s: %v", tc.name, err)
		}
		wants[tc.name] = tc.want
		if tc.wantStayedAs {
			wants[tc.name] = tc.old
		}
	}

	read := func(name string) shape {
		var s shape
		if err := pool.QueryRow(ctx,
			`SELECT resource_type, resource_id, action FROM audit_log WHERE id = $1`, ids[name]).
			Scan(&s.rtype, &s.rid, &s.action); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return s
	}
	for pass := 1; pass <= 2; pass++ {
		if _, err := pool.Exec(ctx, string(script)); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		for name, want := range wants {
			if got := read(name); got != want {
				t.Errorf("pass %d, %s: row is %+v, want %+v", pass, name, got, want)
			}
		}
	}
}
