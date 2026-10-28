package api

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Each case is a way one object of the cluster page once filed under two keys,
// or a task Proxmox ran that no row tied to the request. The requests go through
// the real declarations and handlers, with the HA create harness's stand-ins for
// Proxmox and the database, and read the row each one leaves.
func TestClusterObjectAuditRowsFileUnderOneKey(t *testing.T) {
	t.Parallel()
	// A UPID as Proxmox answers a task-starting request: the JSON string.
	taskUPID := func(taskType, id string) string {
		return "UPID:pve-01:00001A2B:00C0FFEE:6540A1B2:" + taskType + ":" + id + ":root@pam:"
	}
	reply := func(taskType, id string) string { return strconv.Quote(taskUPID(taskType, id)) }

	ha := func(q *db.Queries) *handlers.HAHandler { return handlers.NewHAHandler(q, haCreateEncKey, nil) }
	net := func(q *db.Queries) *handlers.NetworkHandler {
		return handlers.NewNetworkHandler(q, haCreateEncKey, nil)
	}

	for _, tc := range []struct {
		name         string
		method, path string // the declared route
		target, body string // the request
		bind         func(*db.Queries) Handler
		replies      map[string]string
		sequence     map[string][]string // successive answers to one request
		wantType     string
		wantID       string
		wantAction   string
		wantDetails  map[string]any // each must equal the row's detail of that name
		wantAbsent   []string       // details the row must not carry
		forbidden    string         // text that must appear nowhere in the details
		wantTask     bool           // a task_history row was written for it
		vm           *db.Vm         // the guest the inventory holds, if any
		wantNoProbe  bool           // Proxmox was not asked for the resource
		wantSent     bool           // details.fields are the names Proxmox was sent
		wantFields   []string       // details.fields, of an update that read its object first
	}{
		{
			// Proxmox resolves a bare VMID to vm: or ct: by the guest's type, and
			// every other route and the guest's History know the resource by that.
			name: "HA create with a bare VMID files under the sid Proxmox gave it", method: fiber.MethodPost, path: haScope + "/resources",
			target: "/ha/resources", body: `{"sid":"109"}`,
			bind:     func(q *db.Queries) Handler { return ha(q).CreateResource },
			replies:  map[string]string{"GET /api2/json/cluster/ha/resources/109": `{"sid":"ct:109","type":"ct"}`},
			wantType: "ha_resource", wantID: "ct:109", wantAction: "created", wantDetails: map[string]any{"sid": "ct:109"},
		},
		{
			// The inventory knows the guest's type, so Proxmox is not asked.
			name: "HA create with a bare VMID of a container in the inventory", method: fiber.MethodPost, path: haScope + "/resources",
			target: "/ha/resources", body: `{"sid":"109"}`,
			bind:     func(q *db.Queries) Handler { return ha(q).CreateResource },
			vm:       &db.Vm{Vmid: 109, Type: "lxc", Name: "linux01"},
			wantType: "ha_resource", wantID: "ct:109", wantAction: "created", wantNoProbe: true,
		},
		{
			// What Proxmox reports is taken only if it is this VMID's own sid.
			name: "HA update with a bare VMID, Proxmox naming another resource", method: fiber.MethodPut, path: haScope + "/resources/:sid",
			target: "/ha/resources/109", body: `{"state":"started"}`,
			bind:     func(q *db.Queries) Handler { return ha(q).UpdateResource },
			replies:  map[string]string{"GET /api2/json/cluster/ha/resources/109": `{"sid":"vm:999","type":"vm"}`},
			wantType: "ha_resource", wantID: "109", wantAction: "updated",
		},
		{
			name: "HA update with a bare VMID", method: fiber.MethodPut, path: haScope + "/resources/:sid",
			target: "/ha/resources/109", body: `{"state":"started"}`,
			bind:     func(q *db.Queries) Handler { return ha(q).UpdateResource },
			replies:  map[string]string{"GET /api2/json/cluster/ha/resources/109": `{"sid":"vm:109","type":"vm"}`},
			wantType: "ha_resource", wantID: "vm:109", wantAction: "updated",
		},
		{
			name: "HA delete with a bare VMID takes the sid from the snapshot", method: fiber.MethodDelete, path: haScope + "/resources/:sid",
			target:   "/ha/resources/109",
			bind:     func(q *db.Queries) Handler { return ha(q).DeleteResource },
			replies:  map[string]string{"GET /api2/json/cluster/ha/resources/109": `{"sid":"vm:109","type":"vm"}`},
			wantType: "ha_resource", wantID: "vm:109", wantAction: "deleted",
		},
		{
			name: "HA delete with a bare VMID, the snapshot naming another resource", method: fiber.MethodDelete, path: haScope + "/resources/:sid",
			target:   "/ha/resources/109",
			bind:     func(q *db.Queries) Handler { return ha(q).DeleteResource },
			replies:  map[string]string{"GET /api2/json/cluster/ha/resources/109": `{"sid":"vm:999","type":"vm"}`},
			wantType: "ha_resource", wantID: "109", wantAction: "deleted",
		},
		{
			// A zone and a VNet are separate namespaces in Proxmox, so one id can
			// be both: the kind is the type, not a word in the action.
			name: "an SDN zone files under its own kind", method: fiber.MethodPost, path: sdnScope + "/zones",
			target: "/sdn/zones", body: `{"zone":"dmz01","type":"simple"}`,
			bind:     func(q *db.Queries) Handler { return net(q).CreateSDNZone },
			wantType: "sdn_zone", wantID: "dmz01", wantAction: "sdn_zone_created",
		},
		{
			name: "and an SDN VNet of the same id under its own", method: fiber.MethodPost, path: sdnScope + "/vnets",
			target: "/sdn/vnets", body: `{"vnet":"dmz01","zone":"dmz01"}`,
			bind:     func(q *db.Queries) Handler { return net(q).CreateSDNVNet },
			wantType: "sdn_vnet", wantID: "dmz01", wantAction: "sdn_vnet_created",
		},
		{
			// Update and delete address a subnet by the id Proxmox derived from the
			// CIDR; the create takes the CIDR and has to be filed under that id.
			name: "an SDN subnet create files under the subnet's id, not its CIDR", method: fiber.MethodPost, path: sdnScope + "/vnets/:vnet/subnets",
			target: "/sdn/vnets/vnet01/subnets", body: `{"subnet":"192.0.2.0/24"}`,
			bind:     func(q *db.Queries) Handler { return net(q).CreateSDNSubnet },
			replies:  map[string]string{"GET /api2/json/cluster/sdn/vnets/vnet01/subnets": `[{"subnet":"zone01-192.0.2.0-24"}]`},
			wantType: "sdn_subnet", wantID: "vnet01/zone01-192.0.2.0-24", wantAction: "sdn_subnet_created",
		},
		{
			// The apply is a task (reloadnetworkall). Untracked, the collector
			// would ingest it again as one Nexara never dispatched.
			name: "an SDN apply is a tracked task", method: fiber.MethodPut, path: sdnScope + "/apply",
			target:   "/sdn/apply",
			bind:     func(q *db.Queries) Handler { return net(q).ApplySDN },
			replies:  map[string]string{"PUT /api2/json/cluster/sdn": reply("reloadnetworkall", "")},
			wantType: "sdn", wantID: "cluster", wantAction: "sdn_applied",
			wantDetails: map[string]any{"upid": taskUPID("reloadnetworkall", "")},
			wantTask:    true,
		},
		{
			name: "a node network apply is a tracked task", method: fiber.MethodPost, path: networkScope + "/:node_name/apply",
			target:   "/networks/pve-01/apply",
			bind:     func(q *db.Queries) Handler { return net(q).ApplyNetworkConfig },
			replies:  map[string]string{"PUT /api2/json/nodes/pve-01/network": reply("srvreload", "networking")},
			wantType: "network", wantID: "pve-01", wantAction: "network_applied",
			wantDetails: map[string]any{"upid": taskUPID("srvreload", "networking")},
			wantTask:    true,
		},
		{
			// Alias names are folded to lower case by Proxmox on every route.
			name: "a firewall alias files under its lower-case name", method: fiber.MethodPost, path: firewallScope + "/aliases",
			target: "/firewall/aliases", body: `{"name":"MyNet","cidr":"192.0.2.0/24"}`,
			bind:     func(q *db.Queries) Handler { return net(q).CreateFirewallAlias },
			wantType: "firewall_alias", wantID: "mynet", wantAction: "created",
		},
		{
			name: "and a rename says where it went", method: fiber.MethodPut, path: firewallScope + "/aliases/:name",
			target: "/firewall/aliases/MyNet", body: `{"rename":"Other"}`,
			bind:     func(q *db.Queries) Handler { return net(q).UpdateFirewallAlias },
			wantType: "firewall_alias", wantID: "mynet", wantAction: "updated", wantDetails: map[string]any{"rename": "other"},
		},
		{
			// IP sets and security groups are folded to lower case too, and an
			// entry is stored as the set cleaned it: host bits cleared.
			name: "an IP set files under its lower-case name", method: fiber.MethodPost, path: firewallScope + "/ipset",
			target: "/firewall/ipset", body: `{"name":"MySet"}`,
			bind:     func(q *db.Queries) Handler { return net(q).CreateFirewallIPSet },
			wantType: "firewall_ipset", wantID: "myset", wantAction: "created",
		},
		{
			name: "and its entry under the set and the cleaned network", method: fiber.MethodPost, path: firewallScope + "/ipset/:name/entries",
			target: "/firewall/ipset/MySet/entries", body: `{"cidr":"192.0.2.5/24"}`,
			bind:     func(q *db.Queries) Handler { return net(q).AddFirewallIPSetEntry },
			wantType: "firewall_ipset_entry", wantID: "myset/192.0.2.0/24", wantAction: "created",
		},
		{
			// A delete names the entry as the set lists it.
			name: "and its delete under the entry as listed", method: fiber.MethodDelete, path: firewallScope + "/ipset/:name/entries/:cidr",
			target:   "/firewall/ipset/MySet/entries/192.0.2.5%2F24",
			bind:     func(q *db.Queries) Handler { return net(q).DeleteFirewallIPSetEntry },
			wantType: "firewall_ipset_entry", wantID: "myset/192.0.2.5/24", wantAction: "deleted",
		},
		{
			name: "a security group files under its lower-case name", method: fiber.MethodPost, path: firewallScope + "/groups",
			target: "/firewall/groups", body: `{"group":"Web01"}`,
			bind:     func(q *db.Queries) Handler { return net(q).CreateSecurityGroup },
			wantType: "firewall_security_group", wantID: "web01", wantAction: "created",
		},
		{
			name: "and so do its rules", method: fiber.MethodPost, path: firewallScope + "/groups/:group/rules",
			target: "/firewall/groups/Web01/rules", body: `{"type":"in","action":"ACCEPT"}`,
			bind:     func(q *db.Queries) Handler { return net(q).CreateSecurityGroupRule },
			wantType: "firewall_security_group_rule", wantID: "web01", wantAction: "rule_created",
		},
		{
			// A rule's position moves with every insert above it, so the row files
			// under the rule set it belongs to and the position is a detail.
			name: "a firewall rule files under its scope, not its position", method: fiber.MethodPut, path: firewallScope + "/rules/:pos",
			target: "/firewall/rules/3", body: `{"type":"in","action":"ACCEPT"}`,
			bind:     func(q *db.Queries) Handler { return net(q).UpdateClusterFirewallRule },
			wantType: "firewall_rule", wantID: "cluster", wantAction: "firewall_rule_updated", wantDetails: map[string]any{"position": json.Number("3")},
		},
		{
			// Proxmox collapses repeated slashes and drops a trailing one.
			name: "an ACL files under the path Proxmox normalised it to", method: fiber.MethodPut, path: accessScope + "/acl",
			target: "/access/acl", body: `{"path":"/vms//100/","roles":"PVEVMUser","users":"alice@pve"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewAccessHandler(q, haCreateEncKey, nil).UpdateACL
			},
			wantType: "pve_acl", wantID: "/vms/100", wantAction: "granted",
		},
		{
			// The propagate a grant sent is recorded, so it explains only the
			// grants it made.
			name: "an ACL grant records the propagate it sent", method: fiber.MethodPut, path: accessScope + "/acl",
			target: "/access/acl", body: `{"path":"/vms/100","roles":"PVEVMUser","users":"alice@pve","propagate":false}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewAccessHandler(q, haCreateEncKey, nil).UpdateACL
			},
			wantType: "pve_acl", wantID: "/vms/100", wantAction: "granted", wantDetails: map[string]any{"propagate": false},
		},
		// An update names the fields it changes, never a value, so the change
		// observer explains only those: of what it sends, what differs from the
		// object it read just before (a form sent whole re-sends the rest), and
		// of what it clears, what the object had.
		{
			name: "a backup job update names what it changed, not the storage it re-sent", method: fiber.MethodPut,
			path: clusterScope + "/backup-jobs/:job_id", target: "/backup-jobs/backup-0a1b2c3d-4e5f",
			body: `{"schedule":"daily","storage":"store01","vmid":"100,101"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewBackupHandler(q, haCreateEncKey, nil).UpdateBackupJob
			},
			// From every guest to two: all, which it clears, the job had.
			replies: map[string]string{"GET /api2/json/cluster/backup/backup-0a1b2c3d-4e5f": `{"id":"backup-0a1b2c3d-4e5f",` +
				`"type":"vzdump","schedule":"daily","storage":"store01","all":1}`},
			wantType: "backup", wantID: "backup-0a1b2c3d-4e5f", wantAction: "backup_job_updated", wantFields: []string{"all", "vmid"},
		},
		{
			name: "a metric server update", method: fiber.MethodPut, path: metricServerScope + "/:server_id",
			target: "/metric-servers/influx01", body: `{"server":"192.0.2.30","port":8086,"token":"s3cret-token"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewMetricServerHandler(q, haCreateEncKey, nil).UpdateServer
			},
			replies:  map[string]string{"GET /api2/json/cluster/metrics/server/influx01": `{"type":"influxdb","server":"192.0.2.30","port":8089}`},
			wantType: "metric_server", wantID: "influx01", wantAction: "updated", wantFields: []string{"port", "token"}, forbidden: "s3cret",
		},
		{
			name: "a network interface update", method: fiber.MethodPut, path: networkScope + "/:node_name/:iface",
			target: "/networks/pve-01/vmbr0", body: `{"type":"bridge","cidr":"192.0.2.10/24","delete":["gateway"]}`,
			bind: func(q *db.Queries) Handler { return net(q).UpdateNetworkInterface },
			// The client sends autostart whatever the body says: 0 here, and the
			// interface started on boot.
			replies: map[string]string{"GET /api2/json/nodes/pve-01/network/vmbr0": `{"iface":"vmbr0","type":"bridge",` +
				`"cidr":"192.0.2.10/24","gateway":"192.0.2.1","autostart":1}`},
			wantType: "network", wantID: "pve-01/vmbr0", wantAction: "interface_updated", wantFields: []string{"autostart", "gateway"},
		},
		{
			// And one that does not, listing no autostart: 0 is what it has.
			name: "a network interface update re-sending autostart off", method: fiber.MethodPut, path: networkScope + "/:node_name/:iface",
			target: "/networks/pve-01/vmbr1", body: `{"type":"bridge","cidr":"192.0.2.21/24"}`,
			bind:     func(q *db.Queries) Handler { return net(q).UpdateNetworkInterface },
			replies:  map[string]string{"GET /api2/json/nodes/pve-01/network/vmbr1": `{"iface":"vmbr1","type":"bridge","cidr":"192.0.2.20/24"}`},
			wantType: "network", wantID: "pve-01/vmbr1", wantAction: "interface_updated", wantFields: []string{"cidr"},
		},
		{
			name: "an SDN zone update", method: fiber.MethodPut, path: sdnScope + "/zones/:zone",
			target: "/sdn/zones/zone01", body: `{"mtu":1450,"nodes":"pve-01,pve-02","peers":"192.0.2.21"}`,
			bind: func(q *db.Queries) Handler { return net(q).UpdateSDNZone },
			replies: map[string]string{"GET /api2/json/cluster/sdn/zones/zone01": `{"zone":"zone01","type":"vxlan","mtu":1450,` +
				`"nodes":"pve-02,pve-01"}`},
			wantType: "sdn_zone", wantID: "zone01", wantAction: "sdn_zone_updated", wantFields: []string{"peers"},
		},
		{
			name: "an SDN VNet update", method: fiber.MethodPut, path: sdnScope + "/vnets/:vnet",
			target: "/sdn/vnets/vnet01", body: `{"zone":"zone01","alias":"lab2","tag":100}`,
			bind:     func(q *db.Queries) Handler { return net(q).UpdateSDNVNet },
			replies:  map[string]string{"GET /api2/json/cluster/sdn/vnets/vnet01": `{"vnet":"vnet01","zone":"zone01","alias":"lab","tag":100}`},
			wantType: "sdn_vnet", wantID: "vnet01", wantAction: "sdn_vnet_updated", wantFields: []string{"alias"},
		},
		{
			name: "an SDN subnet update", method: fiber.MethodPut, path: sdnScope + "/vnets/:vnet/subnets/:subnet",
			target: "/sdn/vnets/vnet01/subnets/zone01-192.0.2.0-24", body: `{"gateway":"192.0.2.1","snat":1,"dhcp-dns-server":"192.0.2.53"}`,
			bind: func(q *db.Queries) Handler { return net(q).UpdateSDNSubnet },
			replies: map[string]string{"GET /api2/json/cluster/sdn/vnets/vnet01/subnets/zone01-192.0.2.0-24": `{"subnet":` +
				`"zone01-192.0.2.0-24","gateway":"192.0.2.1","snat":1}`},
			wantType: "sdn_subnet", wantID: "vnet01/zone01-192.0.2.0-24", wantAction: "sdn_subnet_updated",
			wantFields: []string{"dhcp-dns-server"},
		},
		{
			name: "an SDN controller update", method: fiber.MethodPut, path: sdnScope + "/controllers/:controller",
			target: "/sdn/controllers/evpn01", body: `{"asn":65001,"peers":"192.0.2.22,192.0.2.21"}`,
			bind: func(q *db.Queries) Handler { return net(q).UpdateSDNController },
			replies: map[string]string{"GET /api2/json/cluster/sdn/controllers/evpn01": `{"controller":"evpn01","type":"evpn",` +
				`"asn":65000,"peers":"192.0.2.21,192.0.2.22"}`},
			wantType: "sdn_controller", wantID: "evpn01", wantAction: "sdn_controller_updated", wantFields: []string{"asn"},
		},
		{
			name: "an SDN IPAM update", method: fiber.MethodPut, path: sdnScope + "/ipams/:ipam",
			target: "/sdn/ipams/ipam01", body: `{"url":"https://ipam.example.com","token":"s3cret-token"}`,
			bind: func(q *db.Queries) Handler { return net(q).UpdateSDNIPAM },
			replies: map[string]string{"GET /api2/json/cluster/sdn/ipams/ipam01": `{"ipam":"ipam01","type":"netbox",` +
				`"url":"https://ipam.example.com"}`},
			wantType: "sdn_ipam", wantID: "ipam01", wantAction: "sdn_ipam_updated", wantFields: []string{"token"}, forbidden: "s3cret",
		},
		{
			name: "an SDN DNS update", method: fiber.MethodPut, path: sdnScope + "/dns/:dns",
			target: "/sdn/dns/dns01", body: `{"url":"https://dns2.example.com","key":"s3cret-key"}`,
			bind: func(q *db.Queries) Handler { return net(q).UpdateSDNDNS },
			replies: map[string]string{"GET /api2/json/cluster/sdn/dns/dns01": `{"dns":"dns01","type":"powerdns",` +
				`"url":"https://dns.example.com","key":"s3cret-key"}`},
			wantType: "sdn_dns", wantID: "dns01", wantAction: "sdn_dns_updated", wantFields: []string{"url"}, forbidden: "s3cret",
		},
		{
			name: "a firewall alias update", method: fiber.MethodPut, path: firewallScope + "/aliases/:name",
			target: "/firewall/aliases/mynet", body: `{"cidr":"192.0.2.0/24","comment":"lab"}`,
			bind:     func(q *db.Queries) Handler { return net(q).UpdateFirewallAlias },
			wantType: "firewall_alias", wantID: "mynet", wantAction: "updated", wantSent: true,
		},
		{
			name: "an ACME plugin update", method: fiber.MethodPut, path: acmeScope + "/plugins/:plugin_id",
			target: "/acme/plugins/cf01", body: `{"api":"cf","data":"CF_Token=s3cret-token","validation-delay":30}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewACMEHandler(q, haCreateEncKey, nil).UpdatePlugin
			},
			// The data it sends base64-encoded, as Proxmox keeps it.
			replies: map[string]string{"GET /api2/json/cluster/acme/plugins/cf01": `{"plugin":"cf01","type":"dns","api":"cf",` +
				`"data":"Q0ZfVG9rZW49czNjcmV0LXRva2Vu"}`},
			wantType: "acme_plugin", wantID: "cf01", wantAction: "updated", wantFields: []string{"validation-delay"}, forbidden: "s3cret",
		},
		{
			// An account update or deactivation is a task too (acmeupdate,
			// acmerefresh, acmedeactivate) whose UPID names no account.
			name: "an ACME account update is a tracked task", method: fiber.MethodPut, path: acmeScope + "/accounts/:name",
			target: "/acme/accounts/default", body: `{"contact":"admin@example.com"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewACMEHandler(q, haCreateEncKey, nil).UpdateAccount
			},
			replies:  map[string]string{"PUT /api2/json/cluster/acme/account/default": reply("acmeupdate", "")},
			wantType: "acme_account", wantID: "default", wantAction: "updated",
			wantDetails: map[string]any{"upid": taskUPID("acmeupdate", "")},
			wantTask:    true,
			forbidden:   "admin@",
		},
		{
			name: "and so is its deactivation", method: fiber.MethodDelete, path: acmeScope + "/accounts/:name",
			target: "/acme/accounts/default",
			bind: func(q *db.Queries) Handler {
				return handlers.NewACMEHandler(q, haCreateEncKey, nil).DeleteAccount
			},
			replies:  map[string]string{"DELETE /api2/json/cluster/acme/account/default": reply("acmedeactivate", "")},
			wantType: "acme_account", wantID: "default", wantAction: "deleted",
			wantDetails: map[string]any{"upid": taskUPID("acmedeactivate", "")},
			wantTask:    true,
		},
		{
			name: "a pool update names the members it touched", method: fiber.MethodPut, path: clusterScope + "/pools/:pool_id",
			target: "/pools/pool01", body: `{"vms":"100,101","delete":"1"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewPoolHandler(q, haCreateEncKey, nil).UpdatePool
			},
			wantType: "pool", wantID: "pool01", wantAction: "updated",
			wantDetails: map[string]any{"vms": "100,101", "delete": "1"},
		},
		{
			// The audit table is never trimmed, so each list a pool update records is capped.
			name: "a pool update keeps a long member list to the audit text cap", method: fiber.MethodPut, path: clusterScope + "/pools/:pool_id",
			target: "/pools/pool01", body: `{"delete":"` + strings.Repeat("a", 1100) + `"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewPoolHandler(q, haCreateEncKey, nil).UpdatePool
			},
			wantType: "pool", wantID: "pool01", wantAction: "updated",
			wantDetails: map[string]any{"delete": strings.Repeat("a", 1024) + "…"},
		},
		{
			// Names only: http_proxy can carry credentials and view:audit is a
			// default Viewer grant.
			name: "a cluster options write names the options, never their values", method: fiber.MethodPut, path: clusterScope + "/options",
			target: "/options", body: `{"http_proxy":"http://user:secret@proxy.example.com:3128","delete":"bwlimit"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewClusterOptionsHandler(q, haCreateEncKey, nil).UpdateOptions
			},
			wantType: "cluster_options", wantID: testClusterID, wantAction: "updated",
			wantDetails: map[string]any{"settings": []any{"http_proxy"}, "cleared": []any{"bwlimit"}},
			forbidden:   "secret",
		},
		{
			// A node's firewall rules file under the node's rule set, as the cluster's and a
			// guest's do, with the position where there is one.
			name: "a node firewall rule create files under the node's rule set", method: fiber.MethodPost, path: clusterScope + "/nodes/:node_name/firewall/rules",
			target: "/nodes/pve-01/firewall/rules", body: `{"type":"in","action":"ACCEPT"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewNodeHandler(q, haCreateEncKey, nil).CreateNodeFirewallRule
			},
			wantType: "firewall_rule", wantID: "node/pve-01", wantAction: "firewall_rule_created",
			wantAbsent: []string{"position"},
		},
		{
			name: "and an update", method: fiber.MethodPut, path: clusterScope + "/nodes/:node_name/firewall/rules/:pos",
			target: "/nodes/pve-01/firewall/rules/3", body: `{"action":"DROP"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewNodeHandler(q, haCreateEncKey, nil).UpdateNodeFirewallRule
			},
			wantType: "firewall_rule", wantID: "node/pve-01", wantAction: "firewall_rule_updated",
			wantDetails: map[string]any{"position": json.Number("3"), "action": "DROP"},
		},
		{
			name: "and a delete", method: fiber.MethodDelete, path: clusterScope + "/nodes/:node_name/firewall/rules/:pos",
			target: "/nodes/pve-01/firewall/rules/3",
			bind: func(q *db.Queries) Handler {
				return handlers.NewNodeHandler(q, haCreateEncKey, nil).DeleteNodeFirewallRule
			},
			wantType: "firewall_rule", wantID: "node/pve-01", wantAction: "firewall_rule_deleted",
			wantDetails: map[string]any{"position": json.Number("3")},
		},
		{
			// A change seen in Proxmox is explained by an update that names its field. The
			// edit dialog sends the comment and the disable flag as they were; the rate is
			// typed 10.0 and kept as 10.
			name: "a replication update names the fields Proxmox stored differently, not the ones it sent as they were", method: fiber.MethodPut, path: replicationScope + "/:job_id",
			target: "/replication/100-0", body: `{"schedule":"*/5","rate":"20.0","comment":"nightly","disable":0}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewReplicationHandler(q, haCreateEncKey, nil).UpdateJob
			},
			sequence: map[string][]string{"GET /api2/json/cluster/replication/100-0": {
				`{"id":"100-0","schedule":"*/15","rate":10,"comment":"nightly","disable":0}`,
				`{"id":"100-0","schedule":"*/5","rate":20,"comment":"nightly","disable":0}`,
			}},
			wantType: "replication", wantID: "100-0", wantAction: "updated",
			wantDetails: map[string]any{"fields": []any{"schedule", "rate"}},
		},
		{
			name: "a replication update whose job cannot be read first names no field", method: fiber.MethodPut, path: replicationScope + "/:job_id",
			target: "/replication/100-0", body: `{"schedule":"*/5"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewReplicationHandler(q, haCreateEncKey, nil).UpdateJob
			},
			sequence: map[string][]string{"GET /api2/json/cluster/replication/100-0": {"refuse"}},
			wantType: "replication", wantID: "100-0", wantAction: "updated",
			wantDetails: map[string]any{"fields": []any{}},
		},
		{
			name: "nor does one that cannot be read after the edit", method: fiber.MethodPut, path: replicationScope + "/:job_id",
			target: "/replication/100-0", body: `{"schedule":"*/5"}`,
			bind: func(q *db.Queries) Handler {
				return handlers.NewReplicationHandler(q, haCreateEncKey, nil).UpdateJob
			},
			sequence: map[string][]string{"GET /api2/json/cluster/replication/100-0": {`{"id":"100-0","schedule":"*/15"}`, "refuse"}},
			wantType: "replication", wantID: "100-0", wantAction: "updated",
			wantDetails: map[string]any{"fields": []any{}},
		},
		{
			// schedule_now spawns no task and answers with nothing; tracking that
			// would leave a task row with no UPID "running" for a day.
			name: "a replication trigger Proxmox answers with no task is an audit row only", method: fiber.MethodPost, path: replicationScope + "/:job_id/trigger",
			target: "/replication/100-0/trigger?node=pve-01",
			bind: func(q *db.Queries) Handler {
				return handlers.NewReplicationHandler(q, haCreateEncKey, nil).TriggerSync
			},
			wantType: "replication", wantID: "100-0", wantAction: "triggered",
			wantAbsent: []string{"upid"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := declaredEndpoint(t, tc.method, tc.path)
			dbtx := &haCreateDBTX{}
			if tc.vm != nil {
				dbtx.models = map[string]any{"GetVMByClusterAndVmid": *tc.vm}
			}
			e.Handler = tc.bind(db.New(dbtx))
			pve := &haCreatePVE{replies: tc.replies, sequence: tc.sequence}
			app := newHAStandInApp(t, e, pve, dbtx, map[string]bool{
				e.Permissions.Check.Action + ":" + e.Permissions.Check.Resource: true,
			})

			target := strings.Replace(clusterScope, ":cluster_id", testClusterID, 1) + tc.target
			req := jsonRequest(tc.method, target, tc.body)
			req.Header.Set("X-Test-User", "yes")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			reply, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				t.Fatalf("status = %d (%s), want 2xx", resp.StatusCode, reply)
			}

			if len(dbtx.audits) != 1 {
				t.Fatalf("the request wrote %d audit rows, want exactly 1", len(dbtx.audits))
			}
			args := dbtx.audits[0] // InsertAuditLog: cluster, user, type, id, action, details
			gotType, _ := args[2].(string)
			gotID, _ := args[3].(string)
			gotAction, _ := args[4].(string)
			if gotType != tc.wantType || gotID != tc.wantID || gotAction != tc.wantAction {
				t.Errorf("the row is (%q, %q, %q), want (%q, %q, %q)",
					gotType, gotID, gotAction, tc.wantType, tc.wantID, tc.wantAction)
			}
			raw, _ := args[5].(json.RawMessage)
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			var details map[string]any
			if err := dec.Decode(&details); err != nil {
				t.Fatalf("the details are not a JSON object (%s): %v", raw, err)
			}
			for key, want := range tc.wantDetails {
				if !reflect.DeepEqual(details[key], want) {
					t.Errorf("detail %s = %v, want %v", key, details[key], want)
				}
			}
			if tc.wantTask {
				// The UI follows the task by the UPID in the reply.
				var body struct {
					UPID string `json:"upid"`
				}
				if err := json.Unmarshal(reply, &body); err != nil || body.UPID != tc.wantDetails["upid"] {
					t.Errorf("the reply is %s, want the task's UPID %v", reply, tc.wantDetails["upid"])
				}
			}
			tasked := false
			for _, sql := range dbtx.rows {
				tasked = tasked || strings.Contains(sql, "INSERT INTO task_history")
			}
			if tasked != tc.wantTask {
				t.Errorf("a task_history row was written = %v, want %v", tasked, tc.wantTask)
			}
			if tc.wantNoProbe {
				pve.mu.Lock()
				for _, r := range pve.seen {
					if r.method == fiber.MethodGet {
						t.Errorf("Proxmox was asked %s %s, which the inventory answers", r.method, r.path)
					}
				}
				pve.mu.Unlock()
			}
			for _, key := range tc.wantAbsent {
				if _, ok := details[key]; ok {
					t.Errorf("the row carries %s, which Proxmox never gave: %s", key, raw)
				}
			}
			if tc.wantSent {
				// Each name with a value, and each its delete list takes out.
				var sent []string
				pve.mu.Lock()
				for _, r := range pve.seen {
					for key, values := range r.form {
						for _, v := range values {
							switch {
							case key == "delete":
								sent = append(sent, strings.Split(v, ",")...)
							case key != "digest" && v != "":
								sent = append(sent, key)
							}
						}
					}
				}
				pve.mu.Unlock()
				slices.Sort(sent)
				fields, _ := details["fields"].([]any)
				got := make([]string, 0, len(fields))
				for _, f := range fields {
					name, _ := f.(string)
					got = append(got, name)
				}
				if sent = slices.Compact(sent); !slices.Equal(got, sent) {
					t.Errorf("the row names the fields %q, and Proxmox was sent %q", got, sent)
				}
			}
			if tc.wantFields != nil {
				fields, _ := details["fields"].([]any)
				got := make([]string, 0, len(fields))
				for _, f := range fields {
					name, _ := f.(string)
					got = append(got, name)
				}
				if !slices.Equal(got, tc.wantFields) {
					t.Errorf("the row names the fields %q, want %q", got, tc.wantFields)
				}
			}
			if tc.forbidden != "" && bytes.Contains(raw, []byte(tc.forbidden)) {
				t.Errorf("the row carries %q: %s", tc.forbidden, raw)
			}
		})
	}
}

// Applying a template writes rules into the cluster's firewall, so the apply is
// filed under the template and, when it made any rule, under the cluster's rules
// too — where the cluster's firewall History reads them.
func TestApplyingATemplateFilesTheClusterRulesToo(t *testing.T) {
	t.Parallel()
	tmpl := db.FirewallTemplate{
		ID: uuid.New(), Name: "base",
		Rules: []byte(`[{"type":"in","action":"ACCEPT"},{"type":"in","action":"DROP"}]`),
	}
	for _, tc := range []struct {
		name     string
		refuse   bool // Proxmox refuses every rule
		wantRows [][2]string
	}{
		{"rules made", false, [][2]string{{"firewall_template", tmpl.ID.String()}, {"firewall_rule", "cluster"}}},
		{"every rule refused", true, [][2]string{{"firewall_template", tmpl.ID.String()}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := declaredEndpoint(t, fiber.MethodPost, clusterScope+"/firewall-templates/:id/apply")
			dbtx := &haCreateDBTX{models: map[string]any{"GetFirewallTemplate": tmpl}}
			e.Handler = handlers.NewNetworkHandler(db.New(dbtx), haCreateEncKey, nil).ApplyTemplate
			pve := &haCreatePVE{refuse: tc.refuse}
			app := newHAStandInApp(t, e, pve, dbtx, map[string]bool{"manage:network": true})
			req := jsonRequest(fiber.MethodPost, strings.Replace(clusterScope, ":cluster_id", testClusterID, 1)+"/firewall-templates/"+tmpl.ID.String()+"/apply", "")
			req.Header.Set("X-Test-User", "yes")
			if status, env := send(t, app, req); status != 200 {
				t.Fatalf("status = %d (%q), want 200", status, env.Message)
			}
			got := make([][2]string, 0, len(dbtx.audits))
			for _, args := range dbtx.audits {
				rt, _ := args[2].(string)
				rid, _ := args[3].(string)
				got = append(got, [2]string{rt, rid})
			}
			if !reflect.DeepEqual(got, tc.wantRows) {
				t.Errorf("audit rows = %v, want %v", got, tc.wantRows)
			}
		})
	}
}
