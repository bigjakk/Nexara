package api

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The Histories of the cluster page's tabs and rows (registry_cluster_page_history.go).

// clusterPageHistoryRoutesOutsideTheClusterCheckShape are the routes gated on AllOf
// view:audit and the view permission of the object's own page, not on one Check.
var clusterPageHistoryRoutesOutsideTheClusterCheckShape = func() map[string]string {
	out := map[string]string{}
	for _, r := range clusterPageHistoryRoutes() {
		out["GET "+r.Path] = "AllOf: view:audit for the rows, view:" + r.Resource + " for the object whose page it is"
	}
	return out
}()

// isClusterPageHistoryRoute reports whether e is one of them: the tallies that find
// a domain's routes by the permission they declare must not count these.
func isClusterPageHistoryRoute(e Endpoint) bool {
	_, ok := clusterPageHistoryRoutesOutsideTheClusterCheckShape[e.Method+" "+e.Path]
	return ok
}

// noHistoryDB answers every read with no rows but the path's cluster, which List
// asks for before it selects anything, so a selector that asks the database
// something (the HA resource's guest cutoff) is told "nothing yet".
type noHistoryDB struct{}

func (noHistoryDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (noHistoryDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return noHistoryRows{}, nil
}
func (noHistoryDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if strings.Contains(sql, "-- name: GetCluster :one") {
		return clusterFoundRow{}
	}
	return noHistoryRow{}
}

type clusterFoundRow struct{}

func (clusterFoundRow) Scan(...any) error { return nil }

type noHistoryRow struct{}

func (noHistoryRow) Scan(...any) error { return pgx.ErrNoRows }

type noHistoryRows struct{}

func (noHistoryRows) Close()                                       {}
func (noHistoryRows) Err() error                                   { return nil }
func (noHistoryRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (noHistoryRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (noHistoryRows) Next() bool                                   { return false }
func (noHistoryRows) Scan(...any) error                            { return pgx.ErrNoRows }
func (noHistoryRows) Values() ([]any, error)                       { return nil, nil }
func (noHistoryRows) RawValues() [][]byte                          { return nil }
func (noHistoryRows) Conn() *pgx.Conn                              { return nil }

// describeSelection writes a selection on one line: an object as type=id,
// type.key=value, type.key∋value (a value in a comma list), type[id].key=value
// or type* (every row), then the guests and the worker filter.
func describeSelection(sel handlers.HistorySelection) string {
	var parts []string
	for _, o := range sel.Objects {
		s := o.ResourceType
		switch {
		case o.WholeType:
			s += "*"
		case o.DetailKey == "":
			s += "=" + o.Value
		default:
			if o.ResourceID != "" {
				s += "[" + o.ResourceID + "]"
			}
			s += "." + o.DetailKey + map[bool]string{true: "∋", false: "="}[o.InList] + o.Value
		}
		parts = append(parts, s)
	}
	for _, g := range sel.Guests {
		parts = append(parts, fmt.Sprintf("guest %d", g.VMID))
	}
	if len(sel.GuestWorkers) > 0 {
		parts = append(parts, "workers "+strings.Join(sel.GuestWorkers, ","))
	}
	return strings.Join(parts, " ")
}

// TestClusterPageHistoryRoutes holds each of the cluster page's History routes to
// what it promises, one row per route (a few more for a spelling that needs
// decoding): the rows its selector asks for, from the request's path as the
// SPA sends it; and a gate that is view:audit and exactly the permission of the
// object's own page, read off a route of that page — a History must not open to
// someone its object's page is closed to.
func TestClusterPageHistoryRoutes(t *testing.T) {
	t.Parallel()
	const tmpl = "9d3a7c10-5f1e-4b8a-9c2d-0a1b2c3d4e5f"
	// route and url are under the cluster's History, but for the one global kind's.
	rows := []struct{ route, url, sibling, want string }{
		{"ha", "ha", haScope + "/resources", "ha_resource* ha_group* ha_rule* ha*"},
		{"ha/resources/:sid", "ha/resources/vm:100", haScope + "/resources/:sid", "ha_resource=vm:100 guest 100 workers ha%"},
		{"ha/resources/:sid", "ha/resources/ct%3A101", "", "ha_resource=ct:101 guest 101 workers ha%"},
		{"ha/resources/:sid", "ha/resources/102", "", "ha_resource=102 ha_resource=vm:102 ha_resource=ct:102 guest 102 workers ha%"},
		{"ha/groups/:group", "ha/groups/group01", haScope + "/groups", "ha_group=group01"},
		{"ha/rules/:rule", "ha/rules/rule01", haScope + "/rules", "ha_rule=rule01"},
		{"pools", "pools", clusterScope + "/pools/:pool_id", "pool*"},
		{"pools/:pool_id", "pools/pool01", clusterScope + "/pools/:pool_id", "pool=pool01 vm.old_pool=pool01 vm.new_pool=pool01"},
		{"mappings", "mappings", clusterScope + "/usb-mappings", "usb_mapping* pci_mapping*"},
		{"mappings/usb/:mapping_id", "mappings/usb/map01", clusterScope + "/usb-mappings", "usb_mapping=map01"},
		{"mappings/pci/:mapping_id", "mappings/pci/map02", clusterScope + "/pci-mappings", "pci_mapping=map02"},
		{"access", "access", accessScope + "/users", "pve_user* pve_token* pve_group* pve_role* pve_acl*"},
		{"access/users/:userid", "access/users/alice%40pve", accessScope + "/users/:userid",
			"pve_user=alice@pve pve_token.userid=alice@pve pve_acl.users∋alice@pve"},
		{"access/groups/:groupid", "access/groups/group01", accessScope + "/groups/:groupid",
			"pve_group=group01 pve_acl.groups∋group01"},
		{"access/roles/:roleid", "access/roles/role01", accessScope + "/roles/:roleid", "pve_role=role01 pve_acl.roles∋role01"},
		{"replication", "replication", replicationScope, "replication*"},
		{"replication/:job_id", "replication/100-0", replicationScope + "/:job_id", "replication=100-0"},
		{"ceph", "ceph", cephScope + "/status", "ceph_osd* ceph_pool* ceph_mon* ceph_mgr* ceph_mds* ceph_fs*"},
		{"ceph/osds/:osd_id", "ceph/osds/12", cephScope + "/osds", "ceph_osd=12"},
		{"ceph/pools/:pool_name", "ceph/pools/pool%23x", cephScope + "/pools", "ceph_pool=pool#x"},
		{"ceph/monitors/:monitor", "ceph/monitors/pve-01", cephScope + "/monitors", "ceph_mon=pve-01"},
		{"networks", "networks", networkScope, "network* sdn* sdn_zone* sdn_vnet* sdn_subnet* sdn_controller* sdn_ipam* sdn_dns*"},
		{"networks/interfaces/:node_name/:iface", "networks/interfaces/pve-01/vmbr0", networkScope + "/:node_name",
			"network=pve-01/vmbr0 network=pve-01"},
		{"networks/zones/:zone", "networks/zones/zone01", sdnScope + "/zones", "sdn_zone=zone01"},
		{"networks/vnets/:vnet", "networks/vnets/vnet01", sdnScope + "/vnets", "sdn_vnet=vnet01 sdn_subnet.vnet=vnet01"},
		{"networks/vnets/:vnet/subnets/:subnet", "networks/vnets/vnet01/subnets/zone01-192.0.2.0-24",
			sdnScope + "/vnets/:vnet/subnets", "sdn_subnet=vnet01/zone01-192.0.2.0-24"},
		{"networks/controllers/:controller", "networks/controllers/ctl01", sdnScope + "/controllers", "sdn_controller=ctl01"},
		{"networks/ipams/:ipam", "networks/ipams/ipam01", sdnScope + "/ipams", "sdn_ipam=ipam01"},
		{"networks/dns/:dns", "networks/dns/dns01", sdnScope + "/dns", "sdn_dns=dns01"},
		{"firewall", "firewall", firewallScope + "/rules",
			"firewall_rule=cluster firewall_options=cluster firewall_alias* firewall_ipset* firewall_ipset_entry* " +
				"firewall_security_group* firewall_security_group_rule*"},
		{"firewall/aliases/:name", "firewall/aliases/Web", firewallScope + "/aliases", "firewall_alias=web firewall_alias.rename=web"},
		{"firewall/ipsets/:name", "firewall/ipsets/Mgmt", firewallScope + "/ipset", "firewall_ipset=mgmt firewall_ipset_entry.set=mgmt"},
		{"firewall/groups/:group", "firewall/groups/Admins", firewallScope + "/groups",
			"firewall_security_group=admins firewall_security_group_rule=admins"},
		{"certificates", "certificates", acmeScope + "/accounts", "acme_account* acme_plugin* acme_config* certificate*"},
		{"certificates/accounts/:name", "certificates/accounts/account01", acmeScope + "/accounts", "acme_account=account01"},
		{"certificates/plugins/:plugin_id", "certificates/plugins/plugin01", acmeScope + "/plugins", "acme_plugin=plugin01"},
		{"certificates/nodes/:node", "certificates/nodes/pve-01", acmeNodeScope + "/certificates", "certificate=pve-01 acme_config=pve-01"},
		{"metric-servers", "metric-servers", metricServerScope, "metric_server*"},
		{"metric-servers/:server_id", "metric-servers/influx01", metricServerScope, "metric_server=influx01"},
		{"drs", "drs", drsScope + "/config", "drs* drs_rule* ha_rule*"},
		{"drs/rules/:rule_id", "drs/rules/" + tmpl, drsScope + "/rules", "drs_rule=" + tmpl},
		{"options", "options", clusterScope + "/options", "cluster_options*"},
		{"/api/v1/history/firewall-templates/:id", "/api/v1/history/firewall-templates/" + tmpl, firewallTemplateScope + "/:id",
			"firewall_template=" + tmpl},
	}
	full := func(rest string) string {
		if strings.HasPrefix(rest, "/") {
			return rest
		}
		return "/api/v1/clusters/" + testClusterID + "/history/" + rest
	}
	covered := map[string]bool{}
	for _, row := range rows {
		covered[strings.Replace(full(row.route), testClusterID, ":cluster_id", 1)] = true
	}
	for _, r := range clusterPageHistoryRoutes() {
		if !covered[r.Path] {
			t.Errorf("%s has no row", r.Path)
		}
	}
	for _, row := range rows {
		t.Run(row.url, func(t *testing.T) {
			t.Parallel()
			var route historyRoute
			for _, r := range clusterPageHistoryRoutes() {
				if r.Path == strings.Replace(full(row.route), testClusterID, ":cluster_id", 1) {
					route = r
				}
			}
			if route.Path == "" {
				t.Fatalf("no cluster History route at %s", row.route)
			}
			asked := ""
			orig := route.Kind.Select
			route.Kind.Select = func(ctx context.Context, r handlers.HistoryRequest) (handlers.HistorySelection, error) {
				sel, err := orig(ctx, r)
				asked = describeSelection(sel)
				return sel, err
			}
			ep := historyEndpoint(handlers.NewHistoryHandler(noHistoryDB{}, db.New(noHistoryDB{})), route)
			app := newRegistryApp(t, stubAuth(map[string]bool{"view:audit": true, "view:" + route.Resource: true}), ep)
			send(t, app, authedRequest(fiber.MethodGet, full(row.url)))
			if asked != row.want {
				t.Errorf("%s asks for %q, want %q", row.url, asked, row.want)
			}
			if row.sibling != "" {
				page := *declaredEndpoint(t, fiber.MethodGet, row.sibling).Permissions.Check
				audit := Check{Action: "view", Resource: "audit", Scope: page.Scope}
				if got := ep.Permissions.AllOf; len(got) != 2 || got[0] != audit || got[1] != page {
					t.Errorf("%s is gated on %v, want view:audit and its page's %v (GET %s)", row.url, got, page, row.sibling)
				}
			}
		})
	}
}

// The SPA asks for these Histories at the paths in cluster-histories.ts, each
// parameter an interpolation; a route renamed on one side only is a History
// that answers 404. Compared by shape — the literal segments and where the
// parameters are — one for one.
func TestClusterPageHistoryPathsAreTheSPAs(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("../../frontend/src/features/history/lib/cluster-histories.ts")
	if err != nil {
		t.Fatal(err)
	}
	templates := regexp.MustCompile("apiPath`(/api/v1/[^`?]+)").FindAllStringSubmatch(string(src), -1)
	routes := clusterPageHistoryRoutes()
	spa, server := make([]string, 0, len(templates)), make([]string, 0, len(routes))
	for _, m := range templates {
		spa = append(spa, regexp.MustCompile(`\$\{[^}]*\}`).ReplaceAllString(m[1], ":p"))
	}
	for _, r := range routes {
		server = append(server, regexp.MustCompile(`:[a-z_]+`).ReplaceAllString(r.Path, ":p"))
	}
	slices.Sort(spa)
	slices.Sort(server)
	if !slices.Equal(spa, server) {
		t.Errorf("the SPA reads %d History paths and the server declares %d; the SPA's:\n%s\nthe server's:\n%s",
			len(spa), len(server), strings.Join(spa, "\n"), strings.Join(server, "\n"))
	}
}
