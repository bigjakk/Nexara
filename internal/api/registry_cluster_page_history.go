package api

import (
	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The Histories of the cluster page: one per tab, and one per object in a tab's
// rows that has a stable identity. They live under the cluster's own History,
// /clusters/:cluster_id/history/<tab>[/<objects>/<id>]: a tab's is the cluster's
// History narrowed to the tab, an object's narrowed to the object. The one
// exception is the firewall template's, which belongs to no cluster, at
// /api/v1/history/firewall-templates/:id.
//
// They are not at the objects' own paths (<tab>/<objects>/<id>/history) for
// three reasons: a tab's collection path would collide with its objects'
// (/pools/history is a pool's id to /pools/:pool_id, and the collections'
// routes are declared first), the tallies that read each domain's routes by
// prefix would count them as the domain's own, and one namespace is one place
// to read what can be asked.
//
// Each is gated on view:audit and the view permission of the tab's own routes,
// which a test holds the two to: the History of an object must not open to
// someone the object's page is closed to. Rows a caller needs more for are
// filtered on it, not refused (HistoryHandler.List). Which rows each reads, and
// how it tells them apart, is the kind's: handlers/*_history.go, with the audit
// identity of each object.

// historyPath is a History route under the cluster's: rest is the tab and, for
// an object, its collection and id.
func historyPath(rest string) string { return clusterScope + "/history/" + rest }

// historyDescription is the text of a History route: what the object is, what
// its History holds, and what reading it takes.
func historyDescription(what, holds, resource string) string {
	return "List what happened to " + what + ", newest first: " + holds + ". Requires view:audit and view:" +
		resource + "; a task's exit text needs view:task too."
}

// clusterPageHistoryRoutes are the Histories of the cluster page's tabs and rows.
func clusterPageHistoryRoutes() []historyRoute {
	var routes []historyRoute
	for _, group := range [][]historyRoute{
		haHistoryRoutes(), poolHistoryRoutes(), mappingHistoryRoutes(), accessHistoryRoutes(),
		replicationHistoryRoutes(), cephHistoryRoutes(), networkHistoryRoutes(), firewallHistoryRoutes(),
		certificateHistoryRoutes(), metricsHistoryRoutes(), drsHistoryRoutes(), optionsHistoryRoutes(),
	} {
		routes = append(routes, group...)
	}
	for i := range routes {
		routes[i].Kind = handlers.WithObjectWindow(routes[i].Kind)
	}
	return routes
}

func haHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.HAHistory(), Path: historyPath("ha"), Resource: "ha",
			Description: historyDescription("the cluster's HA configuration",
				"HA resources, groups and rules created, changed and deleted, and HA armed and disarmed", "ha"),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.HAResourceHistory(), Path: historyPath("ha/resources/:sid"), Resource: "ha",
			Description: historyDescription("one HA resource",
				"its creation, changes and removal, and the starts, stops, migrations and relocations the HA manager "+
					"ordered for its guest (listed to a caller who may open the guest, with view:task for a task "+
					"recorded only in the task history)", "ha"),
			Parameters: clusterParams(apischema.Properties{"sid": haSIDParam}),
		},
		{
			Kind: handlers.HAGroupHistory(), Path: historyPath("ha/groups/:group"), Resource: "ha",
			Description: historyDescription("one HA group", "its creation, changes and removal", "ha"),
			Parameters:  clusterParams(apischema.Properties{"group": haConfigIDParam("Proxmox HA group name.")}),
		},
		{
			Kind: handlers.HARuleHistory(), Path: historyPath("ha/rules/:rule"), Resource: "ha",
			Description: historyDescription("one HA rule", "its creation, changes and removal", "ha"),
			Parameters:  clusterParams(apischema.Properties{"rule": haConfigIDParam("Proxmox HA rule name.")}),
		},
	}
}

func poolHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.PoolsHistory(), Path: historyPath("pools"), Resource: "pool",
			Description: historyDescription("the cluster's resource pools",
				"pools created, changed and deleted. A guest moved into or out of a pool is that pool's own History", "pool"),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.PoolHistory(), Path: historyPath("pools/:pool_id"), Resource: "pool",
			Description: historyDescription("one resource pool",
				"its creation, changes and removal, and the guests moved into or out of it from their own pages "+
					"(a guest's entry needs view:vm or view:container)", "pool"),
			Parameters: poolPathParams(nil),
		},
	}
}

func mappingHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.MappingsHistory(), Path: historyPath("mappings"), Resource: "cluster",
			Description: historyDescription("the cluster's USB and PCI resource mappings",
				"mappings created, changed and deleted", "cluster"),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.USBMappingHistory(), Path: historyPath("mappings/usb/:mapping_id"), Resource: "cluster",
			Description: historyDescription("one USB mapping", "its creation, changes and removal", "cluster"),
			Parameters:  clusterParams(apischema.Properties{"mapping_id": usbMappingIDParam}),
		},
		{
			Kind: handlers.PCIMappingHistory(), Path: historyPath("mappings/pci/:mapping_id"), Resource: "cluster",
			Description: historyDescription("one PCI mapping", "its creation, changes and removal", "cluster"),
			Parameters:  clusterParams(apischema.Properties{"mapping_id": pciMappingIDParam}),
		},
	}
}

func accessHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.AccessHistory(), Path: historyPath("access"), Resource: handlers.AccessResource,
			Description: historyDescription("the cluster's Proxmox access control",
				"users, API tokens, groups and roles created, changed and deleted, and permissions granted and revoked",
				handlers.AccessResource),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.AccessUserHistory(), Path: historyPath("access/users/:userid"), Resource: handlers.AccessResource,
			Description: historyDescription("one Proxmox user",
				"its creation, changes and removal, its API tokens', and the permissions granted to it or revoked from "+
					"it directly", handlers.AccessResource),
			Parameters: accessUserParams(nil),
		},
		{
			Kind: handlers.AccessGroupHistory(), Path: historyPath("access/groups/:groupid"), Resource: handlers.AccessResource,
			Description: historyDescription("one Proxmox group",
				"its creation, changes and removal, and the permissions granted to it or revoked from it",
				handlers.AccessResource),
			Parameters: clusterParams(apischema.Properties{"groupid": accessNameParam("PVE group id.")}),
		},
		{
			Kind: handlers.AccessRoleHistory(), Path: historyPath("access/roles/:roleid"), Resource: handlers.AccessResource,
			Description: historyDescription("one Proxmox role",
				"its creation, changes and removal, and the permissions that grant it or revoke it",
				handlers.AccessResource),
			Parameters: clusterParams(apischema.Properties{"roleid": accessNameParam("PVE role id.")}),
		},
	}
}

func replicationHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.ReplicationHistory(), Path: historyPath("replication"), Resource: "replication",
			Description: historyDescription("the cluster's storage replication jobs",
				"jobs created, changed, deleted and run on demand", "replication"),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.ReplicationJobHistory(), Path: historyPath("replication/:job_id"), Resource: "replication",
			Description: historyDescription("one replication job",
				"its creation, changes and removal, and the syncs run on demand", "replication"),
			Parameters: clusterParams(apischema.Properties{"job_id": replicationJobIDParam}),
		},
	}
}

func cephHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.CephHistory(), Path: historyPath("ceph"), Resource: "ceph",
			Description: historyDescription("the cluster's Ceph",
				"OSDs, pools, monitors, managers, metadata servers and filesystems created, changed and removed, and "+
					"OSDs marked in or out and started, stopped and restarted — in Nexara or in Proxmox", "ceph"),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.CephOSDHistory(), Path: historyPath("ceph/osds/:osd_id"), Resource: "ceph",
			Description: historyDescription("one Ceph OSD",
				"the in, out, start, stop and restart it was ordered and its removal — in Nexara or in Proxmox. An OSD "+
					"a node created is filed under the device it was made on, so its creation is not listed", "ceph"),
			Parameters: osdParams(nil),
		},
		{
			Kind: handlers.CephPoolHistory(), Path: historyPath("ceph/pools/:pool_name"), Resource: "ceph",
			Description: historyDescription("one Ceph pool", "its creation, changes and removal", "ceph"),
			Parameters:  clusterParams(apischema.Properties{"pool_name": cephPoolNameParam}),
		},
		{
			Kind: handlers.CephMonitorHistory(), Path: historyPath("ceph/monitors/:monitor"), Resource: "ceph",
			Description: historyDescription("one Ceph monitor", "its creation and removal", "ceph"),
			Parameters:  clusterParams(apischema.Properties{"monitor": pveObjectNameParam("Ceph monitor name.")}),
		},
	}
}

func networkHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.NetworksHistory(), Path: historyPath("networks"), Resource: "network",
			Description: historyDescription("the cluster's networks",
				"node interfaces and SDN zones, VNets, subnets, controllers, IPAMs and DNS plugins created, changed "+
					"and deleted, and the network configuration applied and reverted", "network"),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.NetworkInterfaceHistory(), Path: historyPath("networks/interfaces/:node_name/:iface"), Resource: "network",
			Description: historyDescription("one node network interface",
				"its creation, changes and removal, and the node's network configuration applied and reverted",
				"network"),
			Parameters: nodeParams(apischema.Properties{"iface": ifaceParam}),
		},
		{
			Kind: handlers.SDNZoneHistory(), Path: historyPath("networks/zones/:zone"), Resource: "network",
			Description: historyDescription("one SDN zone", "its creation, changes and removal", "network"),
			Parameters:  clusterParams(apischema.Properties{"zone": pveObjectNameParam("SDN zone id.")}),
		},
		{
			Kind: handlers.SDNVNetHistory(), Path: historyPath("networks/vnets/:vnet"), Resource: "network",
			Description: historyDescription("one SDN VNet", "its creation, changes and removal, and its subnets'", "network"),
			Parameters:  clusterParams(apischema.Properties{"vnet": pveObjectNameParam("SDN VNet id.")}),
		},
		{
			Kind: handlers.SDNSubnetHistory(), Path: historyPath("networks/vnets/:vnet/subnets/:subnet"), Resource: "network",
			Description: historyDescription("one SDN subnet", "its creation, changes and removal", "network"),
			Parameters: clusterParams(apischema.Properties{
				"vnet":   pveObjectNameParam("SDN VNet id."),
				"subnet": sdnSubnetIDParam,
			}),
		},
		{
			Kind: handlers.SDNControllerHistory(), Path: historyPath("networks/controllers/:controller"), Resource: "network",
			Description: historyDescription("one SDN controller", "its creation, changes and removal", "network"),
			Parameters:  clusterParams(apischema.Properties{"controller": pveObjectNameParam("SDN controller id.")}),
		},
		{
			Kind: handlers.SDNIPAMHistory(), Path: historyPath("networks/ipams/:ipam"), Resource: "network",
			Description: historyDescription("one SDN IPAM", "its creation, changes and removal", "network"),
			Parameters:  clusterParams(apischema.Properties{"ipam": pveObjectNameParam("SDN IPAM id.")}),
		},
		{
			Kind: handlers.SDNDNSHistory(), Path: historyPath("networks/dns/:dns"), Resource: "network",
			Description: historyDescription("one SDN DNS plugin", "its creation, changes and removal", "network"),
			Parameters:  clusterParams(apischema.Properties{"dns": pveObjectNameParam("SDN DNS plugin id.")}),
		},
	}
}

func firewallHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.FirewallHistory(), Path: historyPath("firewall"), Resource: "network",
			Description: historyDescription("the cluster's firewall",
				"its rule set and options, and its aliases, IP sets and security groups with their entries and rules. "+
					"A template applied to the cluster is a row of the rule set. A guest's and a node's rule sets are "+
					"theirs, and a template's own History (creation, change, removal and each application) belongs to "+
					"no cluster", "network"),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.FirewallAliasHistory(), Path: historyPath("firewall/aliases/:name"), Resource: "network",
			Description: historyDescription("one firewall alias",
				"its creation, changes, renames and removal. Aliases are filed under their lower-case name", "network"),
			Parameters: clusterParams(apischema.Properties{"name": pveObjectNameParam("Alias to read.")}),
		},
		{
			Kind: handlers.FirewallIPSetHistory(), Path: historyPath("firewall/ipsets/:name"), Resource: "network",
			Description: historyDescription("one firewall IP set",
				"its creation and removal, and the entries added to it and removed from it. IP sets are filed under "+
					"their lower-case name", "network"),
			Parameters: clusterParams(apischema.Properties{"name": pveObjectNameParam("IP set to read.")}),
		},
		{
			Kind: handlers.FirewallGroupHistory(), Path: historyPath("firewall/groups/:group"), Resource: "network",
			Description: historyDescription("one firewall security group",
				"its creation and removal, and the rules added to it, changed and removed. Groups are filed under "+
					"their lower-case name", "network"),
			Parameters: clusterParams(apischema.Properties{"group": pveObjectNameParam("Security group to read.")}),
		},
		{
			Kind: handlers.FirewallTemplateHistory(), Path: pathPrefix + "history/firewall-templates/:id", Resource: "network",
			Description: historyDescription("one firewall template",
				"its creation, changes and removal, and each application to a cluster. A template belongs to Nexara, "+
					"not to a cluster, so both permissions must be instance-wide", "network"),
			Parameters: apischema.Properties{"id": firewallTemplateIDParam},
		},
	}
}

func certificateHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.CertificatesHistory(), Path: historyPath("certificates"), Resource: acmeCertificateResource,
			Description: historyDescription("the cluster's certificates",
				"ACME accounts and plugins created, changed and deleted, each node's ACME settings saved, and "+
					"certificates ordered, renewed and revoked — in Nexara or in Proxmox", acmeCertificateResource),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.ACMEAccountHistory(), Path: historyPath("certificates/accounts/:name"), Resource: acmeCertificateResource,
			Description: historyDescription("one ACME account", "its creation, changes and removal", acmeCertificateResource),
			Parameters:  clusterParams(apischema.Properties{"name": acmeObjectNameParam("ACME account name.")}),
		},
		{
			Kind: handlers.ACMEPluginHistory(), Path: historyPath("certificates/plugins/:plugin_id"), Resource: acmeCertificateResource,
			Description: historyDescription("one ACME plugin", "its creation, changes and removal", acmeCertificateResource),
			Parameters:  clusterParams(apischema.Properties{"plugin_id": acmeObjectNameParam("ACME plugin id.")}),
		},
		{
			Kind: handlers.NodeCertificateHistory(), Path: historyPath("certificates/nodes/:node"), Resource: acmeCertificateResource,
			Description: historyDescription("one node's certificate",
				"the certificates ordered, renewed and revoked for it — in Nexara or in Proxmox — and the ACME "+
					"settings saved on it", acmeCertificateResource),
			Parameters: acmeNodeParams(nil),
		},
	}
}

func metricsHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.MetricsHistory(), Path: historyPath("metric-servers"), Resource: "cluster",
			Description: historyDescription("the cluster's metric servers",
				"servers created, changed and deleted", "cluster"),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.MetricServerHistory(), Path: historyPath("metric-servers/:server_id"), Resource: "cluster",
			Description: historyDescription("one metric server", "its creation, changes and removal", "cluster"),
			Parameters:  metricServerPathParams(),
		},
	}
}

func drsHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.DRSHistory(), Path: historyPath("drs"), Resource: "drs",
			Description: historyDescription("the cluster's DRS",
				"its configuration changes and evaluations, its DRS rules created and deleted, and Proxmox's HA rules "+
					"created, changed and deleted. The moves DRS made are listed by /drs/history", "drs"),
			Parameters: clusterParams(nil),
		},
		{
			Kind: handlers.DRSRuleHistory(), Path: historyPath("drs/rules/:rule_id"), Resource: "drs",
			Description: historyDescription("one DRS rule", "its creation and removal", "drs"),
			Parameters: clusterParams(apischema.Properties{"rule_id": {
				Type:        apischema.String,
				Format:      "uuid",
				Typetext:    "<uuid>",
				Description: "Nexara DRS rule identifier.",
			}}),
		},
	}
}

func optionsHistoryRoutes() []historyRoute {
	return []historyRoute{
		{
			Kind: handlers.OptionsHistory(), Path: historyPath("options"), Resource: "cluster",
			Description: historyDescription("the cluster's options",
				"the datacenter options, notes and tags changed — the options named, never their values", "cluster"),
			Parameters: clusterParams(nil),
		},
	}
}
