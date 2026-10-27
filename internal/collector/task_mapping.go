package collector

import "strings"

type taskMapping struct {
	ResourceType string
	Action       string
	// Fixed is the audit resource_id of a task whose UPID names no object,
	// where the object is the cluster itself ("cluster" for an SDN apply).
	// Without it such a task is filed under its node.
	Fixed string
	// StripPrefix comes off the UPID's id field to leave the resource_id:
	// Proxmox calls a Ceph daemon "osd.12", Nexara keys it "12".
	StripPrefix string
}

var proxmoxTaskMap = map[string]taskMapping{
	// QEMU VM actions
	"qmstart":    {ResourceType: "vm", Action: "start"},
	"qmstop":     {ResourceType: "vm", Action: "stop"},
	"qmshutdown": {ResourceType: "vm", Action: "shutdown"},
	"qmreboot":   {ResourceType: "vm", Action: "reboot"},
	"qmreset":    {ResourceType: "vm", Action: "reset"},
	"qmsuspend":  {ResourceType: "vm", Action: "suspend"},
	"qmresume":   {ResourceType: "vm", Action: "resume"},
	"qmclone":    {ResourceType: "vm", Action: "clone"},
	"qmdestroy":  {ResourceType: "vm", Action: "destroy"},
	"qmcreate":   {ResourceType: "vm", Action: "create"},
	"qmmigrate":  {ResourceType: "vm", Action: "migrate"},
	"qmigrate":   {ResourceType: "vm", Action: "migrate"},
	"qmmove":     {ResourceType: "vm", Action: "move_disk"},
	"qmconfig":   {ResourceType: "vm", Action: "config_change"},
	"qmtemplate": {ResourceType: "vm", Action: "convert_template"},
	"qmsnapshot": {ResourceType: "vm", Action: "snapshot"},
	"qmrollback": {ResourceType: "vm", Action: "rollback"},

	// LXC container actions
	"vzcreate":   {ResourceType: "container", Action: "create"},
	"vzstart":    {ResourceType: "container", Action: "start"},
	"vzstop":     {ResourceType: "container", Action: "stop"},
	"vzshutdown": {ResourceType: "container", Action: "shutdown"},
	"vzreboot":   {ResourceType: "container", Action: "reboot"},
	"vzdestroy":  {ResourceType: "container", Action: "destroy"},
	"vzmigrate":  {ResourceType: "container", Action: "migrate"},
	"vzclone":    {ResourceType: "container", Action: "clone"},

	// Backup
	"vzdump": {ResourceType: "backup", Action: "backup"},

	// Node-level actions
	"startall":  {ResourceType: "node", Action: "start_all"},
	"stopall":   {ResourceType: "node", Action: "stop_all"},
	"aptupdate": {ResourceType: "node", Action: "apt_update"},

	// Storage
	"download": {ResourceType: "storage", Action: "download"},
	"imgcopy":  {ResourceType: "storage", Action: "image_copy"},

	// Network / service management
	"srvreload": {ResourceType: "node", Action: "service_reload"},

	// ACME. A certificate task names its node and no account, and files under
	// the identity the ACME handlers record for one Nexara dispatched:
	// certificate, keyed by node. The account tasks (acmeregister, acmeupdate,
	// acmerefresh, acmedeactivate) name no account either, so there is nothing to
	// key them on; they stay as they were.
	"acmenewcert":  {ResourceType: "certificate", Action: "ordered"},
	"acmerenew":    {ResourceType: "certificate", Action: "renewed"},
	"acmerevoke":   {ResourceType: "certificate", Action: "revoked"},
	"acmeregister": {ResourceType: "node", Action: "acme_register"},

	// SDN apply: the whole cluster's, whichever node ran it.
	"reloadnetworkall": {ResourceType: "sdn", Action: "sdn_applied", Fixed: "cluster"},

	// Ceph. Filed under the object's own identity — the one ceph.go and
	// ceph_osd.go record for a task Nexara dispatched — so a change made in the
	// Proxmox GUI lands in the same History. A new OSD's id is not known until
	// the task has run (its UPID names the device), so a create can only be
	// filed under that.
	"cephcreateosd":   {ResourceType: "ceph_osd", Action: "osd_create"},
	"cephdestroyosd":  {ResourceType: "ceph_osd", Action: "osd_destroy", StripPrefix: "osd."},
	"cephcreatemon":   {ResourceType: "ceph_mon", Action: "mon_create", StripPrefix: "mon."},
	"cephdestroymon":  {ResourceType: "ceph_mon", Action: "mon_destroy", StripPrefix: "mon."},
	"cephcreatemgr":   {ResourceType: "ceph_mgr", Action: "mgr_create", StripPrefix: "mgr."},
	"cephdestroymgr":  {ResourceType: "ceph_mgr", Action: "mgr_destroy", StripPrefix: "mgr."},
	"cephcreatemds":   {ResourceType: "ceph_mds", Action: "mds_create", StripPrefix: "mds."},
	"cephdestroymds":  {ResourceType: "ceph_mds", Action: "mds_destroy", StripPrefix: "mds."},
	"cephfscreate":    {ResourceType: "ceph_fs", Action: "fs_create"},
	"cephdestroyfs":   {ResourceType: "ceph_fs", Action: "fs_destroy"},
	"cephcreatepool":  {ResourceType: "ceph_pool", Action: "create"},
	"cephdestroypool": {ResourceType: "ceph_pool", Action: "delete"},
	"cephsetpool":     {ResourceType: "ceph_pool", Action: "update"},
}

// cephDaemonKinds maps the prefix of a Ceph daemon's service name to the audit
// resource_type of its kind.
var cephDaemonKinds = map[string]string{
	"osd.": "ceph_osd",
	"mon.": "ceph_mon",
	"mgr.": "ceph_mgr",
	"mds.": "ceph_mds",
}

// serviceTaskVerbs are the node-service worker types that start, stop or
// restart a service.
var serviceTaskVerbs = map[string]string{
	"srvstart":   "start",
	"srvstop":    "stop",
	"srvrestart": "restart",
}

// mapTask resolves a Proxmox task to the audit identity it is filed under:
// resource_type, resource_id and action. ok is false for a task nothing maps,
// which ingestTask files as a generic proxmox_task.
//
// Most worker types map one to one (proxmoxTaskMap). Two cannot: srvstart,
// srvstop and srvrestart are the node's services in general and a Ceph daemon
// in particular, and srvreload is every service reload and the node network
// apply. Only the ones whose id names an object of ours are mapped — a Ceph
// daemon "osd.12", the service "networking" — and the rest fall through, so a
// pveproxy restart is not filed as anything it is not.
func mapTask(taskType, taskID, node string) (mapping taskMapping, resourceID string, ok bool) {
	if verb, isService := serviceTaskVerbs[taskType]; isService {
		for prefix, resourceType := range cephDaemonKinds {
			if rest, found := strings.CutPrefix(taskID, prefix); found && rest != "" {
				kind := strings.TrimSuffix(prefix, ".")
				return taskMapping{ResourceType: resourceType, Action: kind + "_" + verb}, rest, true
			}
		}
	}
	if taskType == "srvreload" && taskID == "networking" {
		return taskMapping{ResourceType: "network", Action: "network_applied"}, node, true
	}

	mapping, ok = proxmoxTaskMap[taskType]
	if !ok {
		return taskMapping{}, "", false
	}
	switch {
	case mapping.Fixed != "":
		resourceID = mapping.Fixed
	case taskID == "":
		resourceID = node
	default:
		resourceID = strings.TrimPrefix(taskID, mapping.StripPrefix)
	}
	return mapping, resourceID, true
}

// skipTaskTypes contains Proxmox task types that should be silently ignored
// (too noisy or not actionable for audit purposes).
var skipTaskTypes = map[string]bool{
	"vncproxy":   true,
	"vncshell":   true,
	"qmmonitor":  true,
	"spiceproxy": true,
	"login":      true,
}

// guestTaskPrefixes / guestTaskExact are the Proxmox worker types whose UPID id
// field holds a guest VMID.
//
// The collector mirror of handlers.vmidFromUPID and the SQL is_guest_upid()
// from migrations/000084 — all three must agree, because they decide the same
// question about the same field. Kept as three because they run in three places
// (Go request path, Go collector, and SQL backfill/insert) with no shared
// vocabulary between them.
//
// Deliberately an allow-list, not "any numeric id": a worker type not known to
// carry a VMID yields none, since a row naming the wrong guest is worse than
// one naming none. A Ceph daemon's tasks carry its name ("osd.12", never 12;
// OSD.pm) and are mapped to the daemon below.
var (
	guestTaskPrefixes = []string{"qm", "vz", "ha"}
	guestTaskExact    = map[string]bool{"resize": true, "move_volume": true}
)

// isGuestTaskType reports whether a Proxmox worker type's UPID id field is a
// guest VMID.
func isGuestTaskType(taskType string) bool {
	if guestTaskExact[taskType] {
		return true
	}
	for _, p := range guestTaskPrefixes {
		if strings.HasPrefix(taskType, p) {
			return true
		}
	}
	return false
}
