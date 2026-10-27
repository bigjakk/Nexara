package handlers

import (
	"context"

	"github.com/bigjakk/nexara/internal/auditkey"
)

// networksTypes are the audit types the Networks tab holds: the nodes'
// interfaces and the SDN's objects, and the SDN apply.
var networksTypes = []string{"network", "sdn", "sdn_zone", "sdn_vnet", "sdn_subnet", "sdn_controller", "sdn_ipam", "sdn_dns"}

// NetworksHistory is the Networks tab's kind: the nodes' interfaces and the
// SDN's zones, VNets, subnets, controllers, IPAMs and DNS plugins, told apart
// by type, and what was applied — a node's network configuration or the SDN.
func NetworksHistory() HistoryKind {
	return HistoryKind{
		Name: "networks",
		Categories: []string{"interfaces", "applied", "zones", "vnets", "subnets", "controllers", "ipams", "dns",
			"other"},
		Default: "other",
		Rules: []HistoryRule{
			{Category: "applied", Field: HistoryFieldAction, Equals: []string{"network_applied", "network_reverted", "sdn_applied"}},
			typeRule("interfaces", "network"),
			typeRule("zones", "sdn_zone"),
			typeRule("vnets", "sdn_vnet"),
			typeRule("subnets", "sdn_subnet"),
			typeRule("controllers", "sdn_controller"),
			typeRule("ipams", "sdn_ipam"),
			typeRule("dns", "sdn_dns"),
		},
		Select:        SelectTypes(networksTypes...),
		ObjectTypes:   networksTypes,
		ObservedTypes: observedOf("network", "sdn_zone", "sdn_vnet", "sdn_subnet", "sdn_controller", "sdn_ipam", "sdn_dns"),
	}
}

// NetworkInterfaceHistory is one node interface's kind: its own rows, filed
// under the node and its name, and the node's applies and reverts of its
// pending network configuration, which take effect on every interface of the
// node.
func NetworkInterfaceHistory() HistoryKind {
	return HistoryKind{
		Name:       "network-interface",
		Categories: []string{"lifecycle", "config", "applied", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "applied", Field: HistoryFieldAction, Equals: []string{"network_applied", "network_reverted"}},
			ruleCreatedDeleted, ruleUpdated, ruleObserved,
		},
		Select: func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
			node := r.Params.String("node_name")
			return HistorySelection{Objects: []HistoryObject{
				{ResourceType: "network", Value: node + "/" + r.Params.String("iface")},
				{ResourceType: "network", Value: node},
			}}, nil
		},
		ObjectTypes:   []string{"network"},
		ObservedTypes: observedOf("network"),
	}
}

// SDNZoneHistory is one SDN zone's kind.
func SDNZoneHistory() HistoryKind {
	return objectHistory("sdn-zone", "zone", []string{"sdn_zone"}, "sdn_zone")
}

// SDNVNetHistory is one SDN VNet's kind: its own rows and its subnets', which
// name it.
func SDNVNetHistory() HistoryKind {
	return HistoryKind{
		Name:       "sdn-vnet",
		Categories: []string{"lifecycle", "config", "subnets", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("subnets", "sdn_subnet"),
			ruleCreatedDeleted, ruleUpdated, ruleObserved,
		},
		Select: func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
			vnet := r.Params.String("vnet")
			return HistorySelection{Objects: []HistoryObject{
				{ResourceType: "sdn_vnet", Value: vnet},
				{ResourceType: "sdn_subnet", DetailKey: "vnet", Value: vnet},
			}}, nil
		},
		ObjectTypes:   []string{"sdn_vnet", "sdn_subnet"},
		ObservedTypes: observedOf("sdn_vnet"),
	}
}

// SDNSubnetHistory is one SDN subnet's kind. A subnet is filed under its VNet
// and the id Proxmox derived from its CIDR (auditkey.SDNSubnet).
func SDNSubnetHistory() HistoryKind {
	k := objectHistory("sdn-subnet", "subnet", []string{"sdn_subnet"}, "sdn_subnet")
	k.Select = func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
		key := auditkey.SDNSubnet(r.Params.String("vnet"), r.Params.String("subnet"))
		return HistorySelection{Objects: []HistoryObject{{ResourceType: "sdn_subnet", Value: key}}}, nil
	}
	return k
}

// SDNControllerHistory is one SDN controller's kind.
func SDNControllerHistory() HistoryKind {
	return objectHistory("sdn-controller", "controller", []string{"sdn_controller"}, "sdn_controller")
}

// SDNIPAMHistory is one SDN IPAM's kind.
func SDNIPAMHistory() HistoryKind {
	return objectHistory("sdn-ipam", "ipam", []string{"sdn_ipam"}, "sdn_ipam")
}

// SDNDNSHistory is one SDN DNS plugin's kind.
func SDNDNSHistory() HistoryKind {
	return objectHistory("sdn-dns", "dns", []string{"sdn_dns"}, "sdn_dns")
}
