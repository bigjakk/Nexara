package handlers

import "context"

// PBSHistory is the PBS server kind: a Proxmox Backup Server's History, and,
// when the request names a datastore, that datastore's. A global kind: the
// server is Nexara's own row, its uuid unique across Nexara, and its rows
// carry the cluster it is attached to, or none.
//
// Its rows are filed under the server — its own create, update and delete, and
// each datastore action and job trigger, which name the datastore and any job
// in details — and a restore from it is filed under the guest it restored
// (backup), whose cluster is not the server's, and names the server in
// details. A datastore's History is the rows that name it within the server's,
// which is its identity: two servers can each hold a datastore of one name —
// and the restores that name both the server and the datastore they read from.
func PBSHistory() HistoryKind {
	return HistoryKind{
		Name:       "pbs",
		Categories: []string{"server", "snapshots", "maintenance", "jobs", "restore", "other"},
		Default:    "other",
		Rules:      pbsHistoryRules,
		Select:     selectPBS,
		Global:     true,
		// The sync and verify jobs edited on the server are seen by the observer.
		ObjectTypes:      []string{"pbs_server", "backup"},
		KeyedObjectTypes: []string{"backup"},
		ObservedTypes:    []HistoryObservedType{{Type: "pbs_server"}},
	}
}

// pbsHistoryRules are the type filter's categories, matched in order.
var pbsHistoryRules = []HistoryRule{
	{Category: "jobs", Field: HistoryFieldEntry, Equals: []string{"observed"}},
	{Category: "server", Field: HistoryFieldAction, Like: []string{`pbs\_%`}},
	{Category: "snapshots", Field: HistoryFieldAction, Like: []string{`snapshot\_%`}},
	{Category: "maintenance", Field: HistoryFieldAction, Equals: []string{"gc_triggered", "datastore_pruned"}},
	{Category: "jobs", Field: HistoryFieldAction, Like: []string{`%\_job\_triggered`}},
	{Category: "restore", Field: HistoryFieldAction, Equals: []string{"backup_restored"}},
}

// selectPBS is the PBS kind's selector: the path's server — its own rows and
// the restores that name it — or, with a datastore, the server's rows that
// name the datastore and the restores that name both.
func selectPBS(_ context.Context, r HistoryRequest) (HistorySelection, error) {
	server := r.Params.String("pbs_id")
	if datastore := r.Params.String("datastore"); datastore != "" {
		return HistorySelection{Objects: []HistoryObject{
			{ResourceType: "pbs_server", ResourceID: server, DetailKey: "datastore", Value: datastore},
			{ResourceType: "backup", DetailKey: "pbs_server_id", Value: server, AndKey: "datastore", AndValue: datastore},
		}}, nil
	}
	return HistorySelection{Objects: []HistoryObject{
		{ResourceType: "pbs_server", Value: server},
		{ResourceType: "backup", DetailKey: "pbs_server_id", Value: server},
	}}, nil
}
