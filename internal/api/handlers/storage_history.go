package handlers

import "context"

// StorageHistory is the storage kind: a storage's History tab, everything
// that happened to it. A storage is its NAME in its cluster, as Proxmox calls
// it on every node — never a storage_pools row, which the collector drops and
// re-creates, nor another cluster's storage of the same name.
//
// Its rows are filed in five places, all read here: under the storage itself
// (its definition, an upload, a download, a delete of a volume or backups);
// under the guest whose disk was moved onto it or off it, which names the
// storage in details; under the backup a restore reads from or writes to;
// under the node an LVM, LVM-thin or directory create that added the storage
// was run on, whose storage Proxmox names after the volume; and under the
// resource pool an update added the storage to or took it out of, whose
// comma-separated storage list names it. A non-shared storage is a different
// storage on every node, so its page asks for the one node's rows
// (ObjectsOnNode).
//
// Not covered: a disk that a migration places on the storage by the migration
// itself — a remote migration's target storage, a storage map — because the
// migration's own audit rows name the nodes, not the storages. The per-disk
// moves of a storage migration are covered: each is a disk_move_running row
// that names target_storage and from_storage.
func StorageHistory() HistoryKind {
	return HistoryKind{
		Name:        "storage",
		Categories:  []string{"lifecycle", "config", "content", "disks", "backup", "other"},
		Default:     "other",
		Rules:       storageHistoryRules,
		Select:      selectStorage,
		ObjectTypes: []string{"storage", "vm", "container", "backup", "node", "pool"},
		// Its definition, edited in Proxmox, is seen by the observer.
		ObservedTypes: []HistoryObservedType{{Type: "storage"}},
	}
}

// storageHistoryRules are the type filter's categories, matched in order. A
// rule on the worker type is for a row whose action is not one Nexara names:
// a task ingested from Proxmox.
var storageHistoryRules = []HistoryRule{
	{Category: "config", Field: HistoryFieldEntry, Equals: []string{"observed"}},
	{Category: "lifecycle", Field: HistoryFieldAction, Equals: []string{
		"create", "delete", "create_import_source", "delete_import_source",
		"create_lvm", "create_lvmthin", "create_directory"}},
	{Category: "config", Field: HistoryFieldAction, Equals: []string{
		"update", "enable_import_content", "virtio_win_config_update"}},
	// A resource pool's update, which is how a storage joins or leaves one.
	{Category: "config", Field: HistoryFieldAction, Equals: []string{"updated"}},
	{Category: "content", Field: HistoryFieldAction, Equals: []string{
		"upload", "download_url", "download_appliance", "pull_oci", "virtio_win_download",
		"delete_content", "prune_backups", "image_copy", "download"}},
	{Category: "backup", Field: HistoryFieldAction, Like: []string{`backup\_%`}},
	{Category: "backup", Field: HistoryFieldWorker, Equals: []string{"vzdump", "qmrestore", "vzrestore"}},
	{Category: "disks", Field: HistoryFieldAction, Like: []string{`disk\_%`}},
	{Category: "disks", Field: HistoryFieldAction, Equals: []string{"volume_move", "move_disk"}},
}

// storageHistoryObjects are the audit rows that are one storage's, by the
// storage's name: filed under it, or naming it in details. A disk move names
// the storage it wrote to as storage (or target_storage, when a migration job
// made the move) and the one it left as from_storage; a restore names the
// storage it wrote to, and the one the archive was read through, the same way;
// a pool update names it among the storages it added or removed.
var storageHistoryObjects = []HistoryObject{
	{ResourceType: "storage"},
	{ResourceType: "vm", DetailKey: "storage"},
	{ResourceType: "vm", DetailKey: "target_storage"},
	{ResourceType: "vm", DetailKey: "from_storage"},
	{ResourceType: "container", DetailKey: "storage"},
	{ResourceType: "container", DetailKey: "target_storage"},
	{ResourceType: "container", DetailKey: "from_storage"},
	{ResourceType: "backup", DetailKey: "storage"},
	{ResourceType: "backup", DetailKey: "from_storage"},
	{ResourceType: "node", DetailKey: "storage"},
	// A pool update lists the storages it added or removed in one value, "a,b".
	{ResourceType: "pool", DetailKey: "storage", InList: true},
}

// selectStorage is the storage kind's selector: the path's storage, and, when
// the request names a node, only what did not run on another. It also reads the
// storage rows filed under the cluster's id, which are the observer's notices
// (a kind past its cap) and its rows past the cap (collector noticeTarget): it
// files them under the type its objects file under, so that this View reads
// them, and at the cluster's id, which no storage is called.
func selectStorage(ctx context.Context, r HistoryRequest) (HistorySelection, error) {
	sel, err := SelectObjects("storage", storageHistoryObjects...)(ctx, r)
	if err != nil {
		return HistorySelection{}, err
	}
	sel.Objects = append(sel.Objects, HistoryObject{ResourceType: "storage", Value: r.ClusterID.String()})
	sel.ObjectsOnNode = r.Params.String("node")
	return sel, nil
}
