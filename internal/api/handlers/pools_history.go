package handlers

import "context"

// PoolsHistory is the Pools tab's kind: the cluster's resource pools created,
// changed and deleted. A guest moved into or out of a pool is the pool's own
// History, not the tab's: those rows are filed under the guest.
func PoolsHistory() HistoryKind {
	return HistoryKind{
		Name:          "pools",
		Categories:    objectCategories,
		Default:       "other",
		Rules:         append(append([]HistoryRule{}, objectRules...), ruleObserved),
		Select:        SelectTypes("pool"),
		ObjectTypes:   []string{"pool"},
		ObservedTypes: observedOf("pool"),
	}
}

// PoolHistory is one pool's kind: its own rows, and the guests moved into or
// out of it from their own pages (vm set_pool, which names the pool it left
// and the one it joined). Which of those a caller sees is the guest's: the
// rows name the guest.
func PoolHistory() HistoryKind {
	return HistoryKind{
		Name:       "pool",
		Categories: []string{"lifecycle", "config", "members", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("members", "vm"),
			ruleCreatedDeleted, ruleUpdated, ruleObserved,
		},
		Select:        selectPool,
		ObjectTypes:   []string{"pool", "vm"},
		ObservedTypes: observedOf("pool"),
	}
}

func selectPool(_ context.Context, r HistoryRequest) (HistorySelection, error) {
	id := r.Params.String("pool_id")
	return HistorySelection{Objects: []HistoryObject{
		{ResourceType: "pool", Value: id},
		{ResourceType: "vm", DetailKey: "old_pool", Value: id},
		{ResourceType: "vm", DetailKey: "new_pool", Value: id},
	}}, nil
}
