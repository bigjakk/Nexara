package handlers

// replicationCategories are the replication Histories': a job's creation and
// removal, its settings, and the syncs run on demand.
var replicationCategories = []string{"lifecycle", "config", "sync", "other"}

// replicationRules are replicationCategories' rules. A job runs on its own
// schedule, which files nothing; only the run-now trigger is a row.
var replicationRules = []HistoryRule{
	{Category: "sync", Field: HistoryFieldAction, Equals: []string{"triggered"}},
	ruleCreatedDeleted, ruleUpdated, ruleObserved,
}

// ReplicationHistory is the Replication tab's kind: the cluster's replication
// jobs.
func ReplicationHistory() HistoryKind {
	return HistoryKind{
		Name:          "replication",
		Categories:    replicationCategories,
		Default:       "other",
		Rules:         replicationRules,
		Select:        SelectTypes("replication"),
		ObjectTypes:   []string{"replication"},
		ObservedTypes: observedOf("replication"),
	}
}

// ReplicationJobHistory is one replication job's kind.
func ReplicationJobHistory() HistoryKind {
	k := objectHistory("replication-job", "job_id", []string{"replication"}, "replication")
	k.Categories, k.Rules = replicationCategories, replicationRules
	return k
}
