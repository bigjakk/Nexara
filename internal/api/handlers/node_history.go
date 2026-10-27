package handlers

import (
	"context"
)

// NodeHistory is the node kind: what happened to one node — its own actions
// (updates, boots and shutdowns, maintenance, options), its parts' (network,
// repositories, OSDs, storage, certificates), the tasks that ran on it, its
// status changes, and the guests that arrived on it or left it. A guest's
// task, and any other row naming a guest but a move, comes only with the
// route's include_guests: a busy node runs thousands of them, which would bury
// its own, and each guest has its History. A node re-added under its name
// continues its History: the name is the operator's, and almost always the
// same host or role.
func NodeHistory() HistoryKind {
	return HistoryKind{
		Name: "node",
		Categories: []string{"status", "power", "maintenance", "updates", "network", "storage", "ceph",
			"certificates", "options", "checks", "console", "guests", "other"},
		Default: "other",
		Rules:   nodeHistoryRules,
		Select:  selectNode,
		ObjectTypes: []string{"node", "network", "firewall_rule", "apt_repository", "ceph_osd", "storage", "backup",
			"auth", "rolling_update", "usb_mapping", "pci_mapping", "certificate", "acme_config"},
		// A task that ran on the node is the node's whoever held the VMID it
		// names then: no guest's cutoff holds it.
		ObjectsIgnoreGuestCutoff: true,
		GuestToggle:              "include_guests",
	}
}

// nodeHistoryRules are the node kind's categories, matched in order: what
// happened to the node before what ran on it, and a guest's tasks last.
var nodeHistoryRules = []HistoryRule{
	{Category: "status", Field: HistoryFieldEntry, Equals: []string{"status"}},
	{Category: "guests", Field: HistoryFieldEntry, Equals: []string{"move"}},
	// Reads that change nothing on the node, most of them daily — Proxmox's
	// package-list refresh, Nexara's memory-module read — which the UI leaves
	// out until asked. A repository change is an update, and stays in.
	{Category: "checks", Field: HistoryFieldAction, Equals: []string{"apt_update", "read_memory_modules", "download_report"}},
	{Category: "checks", Field: HistoryFieldWorker, Equals: []string{"aptupdate"}},
	{Category: "updates", Field: HistoryFieldResourceType, Equals: []string{"apt_repository"}},
	{Category: "maintenance", Field: HistoryFieldAction, Equals: []string{
		"node_maintenance_enter", "node_maintenance_exit", "evacuate"}},
	{Category: "maintenance", Field: HistoryFieldResourceType, Equals: []string{"rolling_update"}},
	{Category: "maintenance", Field: HistoryFieldWorker, Equals: []string{"migrateall"}},
	{Category: "power", Field: HistoryFieldAction, Equals: []string{"reboot", "shutdown", "start_all", "stop_all", "wakeonlan"}},
	{Category: "power", Field: HistoryFieldWorker, Equals: []string{"startall", "stopall", "wakeonlan"}},
	{Category: "network", Field: HistoryFieldResourceType, Equals: []string{"network", "firewall_rule"}},
	// A node firewall rule filed under node / <node>, as before its rows moved
	// to firewall_rule / node/<node>.
	{Category: "network", Field: HistoryFieldAction, Equals: []string{
		"create_firewall_rule", "update_firewall_rule", "delete_firewall_rule"}},
	{Category: "network", Field: HistoryFieldWorker, Equals: []string{"srvreload"}},
	{Category: "storage", Field: HistoryFieldResourceType, Equals: []string{"storage", "backup"}},
	{Category: "storage", Field: HistoryFieldWorker, Like: []string{"zfs%", "lvm%", "dircreate", "diskinit", "wipedisk",
		"imgdel", "imgcopy", "download", "vzdump"}},
	{Category: "ceph", Field: HistoryFieldResourceType, Like: []string{`ceph\_%`}},
	{Category: "ceph", Field: HistoryFieldWorker, Like: []string{"ceph%"}},
	{Category: "certificates", Field: HistoryFieldResourceType, Equals: []string{"certificate", "acme_config"}},
	{Category: "certificates", Field: HistoryFieldWorker, Like: []string{"acme%"}},
	{Category: "options", Field: HistoryFieldAction, Equals: []string{"set_options", "set_dns", "set_timezone"}},
	{Category: "options", Field: HistoryFieldResourceType, Equals: []string{"usb_mapping", "pci_mapping"}},
	{Category: "console", Field: HistoryFieldResourceType, Equals: []string{"auth"}},
	{Category: "guests", Field: HistoryFieldWorker, Like: []string{"qm%", "vz%", "ha%"}},
	{Category: "guests", Field: HistoryFieldResourceType, Equals: []string{"vm", "container", "ct"}},
}

// selectNode is the node kind's selector: the node's own rows, its parts'
// by the node they name, its firewall rules', certificate's and ACME
// config's, and the node itself for its tasks, status changes and moves.
func selectNode(_ context.Context, r HistoryRequest) (HistorySelection, error) {
	name := r.Params.String("node_name")
	sel := HistorySelection{Node: name, Objects: []HistoryObject{
		{ResourceType: "node", Value: name},
		{ResourceType: "network", Value: name},
		{ResourceType: "firewall_rule", Value: "node/" + name},
		{ResourceType: "certificate", Value: name},
		{ResourceType: "acme_config", Value: name},
	}}
	for _, t := range []string{"network", "apt_repository", "ceph_osd", "storage", "backup", "auth",
		"rolling_update", "usb_mapping", "pci_mapping"} {
		sel.Objects = append(sel.Objects, HistoryObject{ResourceType: t, DetailKey: "node", Value: name})
	}
	return sel, nil
}
