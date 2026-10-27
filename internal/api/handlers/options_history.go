package handlers

// OptionsHistory is the Options tab's kind: the cluster's datacenter options,
// notes and tags, which have no row of their own to read it from. The audit
// rows name the options changed, never their values.
func OptionsHistory() HistoryKind {
	return HistoryKind{
		Name:       "options",
		Categories: []string{"options", "notes", "tags", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "notes", Field: HistoryFieldAction, Equals: []string{"description_updated"}},
			{Category: "tags", Field: HistoryFieldAction, Equals: []string{"tags_updated"}},
			typeRule("options", "cluster_options"),
		},
		Select:        SelectTypes("cluster_options"),
		ObjectTypes:   []string{"cluster_options"},
		ObservedTypes: observedOf("cluster_options"),
	}
}
