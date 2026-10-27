package handlers

import (
	"context"
	"time"
)

// ClusterHistory is the cluster kind: everything that happened in one
// cluster, newest first — every audit row filed under it, any task with no
// audit row, its guests' status changes no task explains and parts of backup
// jobs — with its sync failures read as runs. Each guest's rows are the
// caller's only under the guest's own view permission, as everywhere.
func ClusterHistory() HistoryKind {
	return HistoryKind{
		Name: "cluster",
		Categories: []string{"guests", "nodes", "storage", "backup", "ceph", "ha", "network", "access",
			"certificates", "replication", "drs", "sync", "options", "other"},
		Default:      "other",
		Rules:        clusterHistoryRules,
		Select:       selectCluster,
		WholeCluster: true,
		// An unreachable cluster files a sync failure every pass, five minutes
		// apart; a quarter of an hour without one ends the run.
		Collapse: []HistoryCollapse{
			{ResourceType: "cluster", Action: "sync_failed", Gap: 15 * time.Minute},
			{ResourceType: "cluster", Action: "tls_fingerprint_mismatch", Gap: 15 * time.Minute},
		},
		RowCap: clusterHistoryRowCap,
	}
}

// clusterHistoryRowCap is how many rows of each source an unfiltered read of a
// cluster's History reads, newest first: 40 pages of the newest, against a
// trail that runs to millions of rows on a busy install. At 1M audit rows a
// week of the cluster reads in 110–130 ms so, and 450–520 ms at 10,000.
const clusterHistoryRowCap = 2_000

// clusterHistoryRules are the cluster kind's categories, by resource type
// family and, for a task with no row of its own type, by worker type. A
// guest's backup part and a Proxmox backup task are backup before the guest
// workers (vz%) can take them.
var clusterHistoryRules = []HistoryRule{
	{Category: "sync", Field: HistoryFieldEntry, Equals: []string{"run"}},
	{Category: "backup", Field: HistoryFieldEntry, Equals: []string{"backup"}},
	{Category: "guests", Field: HistoryFieldEntry, Equals: []string{"status"}},
	{Category: "sync", Field: HistoryFieldAction, Equals: []string{
		"sync_failed", "tls_fingerprint_mismatch", "cluster_endpoint_failover", "tls_fingerprint_verified"}},
	{Category: "backup", Field: HistoryFieldResourceType, Equals: []string{
		"backup", "backup_job", "pbs_server", "veeam_server", "veeam_job", "veeam_session"}},
	{Category: "backup", Field: HistoryFieldWorker, Equals: []string{"vzdump", "qmrestore", "vzrestore"}},
	{Category: "ceph", Field: HistoryFieldResourceType, Like: []string{`ceph\_%`}},
	{Category: "ceph", Field: HistoryFieldWorker, Like: []string{"ceph%"}},
	{Category: "ha", Field: HistoryFieldResourceType, Equals: []string{"ha", "ha_resource", "ha_group", "ha_rule"}},
	{Category: "ha", Field: HistoryFieldWorker, Like: []string{"ha%"}},
	{Category: "network", Field: HistoryFieldResourceType, Equals: []string{"network", "sdn"}},
	{Category: "network", Field: HistoryFieldResourceType, Like: []string{`sdn\_%`, `firewall\_%`}},
	{Category: "network", Field: HistoryFieldWorker, Equals: []string{"srvreload"}},
	{Category: "access", Field: HistoryFieldResourceType, Equals: []string{"auth"}},
	{Category: "access", Field: HistoryFieldResourceType, Like: []string{`pve\_%`}},
	{Category: "certificates", Field: HistoryFieldResourceType, Equals: []string{
		"certificate", "acme_account", "acme_plugin", "acme_config"}},
	{Category: "certificates", Field: HistoryFieldWorker, Like: []string{"acme%"}},
	{Category: "replication", Field: HistoryFieldResourceType, Equals: []string{"replication"}},
	{Category: "drs", Field: HistoryFieldResourceType, Equals: []string{"drs", "drs_rule"}},
	{Category: "drs", Field: HistoryFieldAction, Like: []string{`drs\_%`}},
	{Category: "storage", Field: HistoryFieldResourceType, Equals: []string{"storage"}},
	{Category: "storage", Field: HistoryFieldWorker, Equals: []string{"imgdel", "imgcopy", "download", "ociregistrypull"}},
	{Category: "nodes", Field: HistoryFieldResourceType, Equals: []string{"node", "apt_repository", "rolling_update"}},
	{Category: "nodes", Field: HistoryFieldWorker, Equals: []string{
		"aptupdate", "startall", "stopall", "migrateall", "srvstart", "srvstop", "srvrestart"}},
	{Category: "guests", Field: HistoryFieldResourceType, Equals: []string{
		"vm", "container", "ct", "vm_folder", "vm_folder_membership", "schedule"}},
	{Category: "guests", Field: HistoryFieldWorker, Like: []string{"qm%", "vz%"}},
	{Category: "guests", Field: HistoryFieldWorker, Equals: []string{"resize", "move_volume"}},
	{Category: "options", Field: HistoryFieldResourceType, Equals: []string{
		"cluster", "cluster_options", "metric_server", "usb_mapping", "pci_mapping", "pool"}},
}

// selectCluster is the cluster kind's selector: nothing to name, since the
// kind reads its whole cluster (HistoryKind.WholeCluster).
func selectCluster(context.Context, HistoryRequest) (HistorySelection, error) {
	return HistorySelection{}, nil
}
