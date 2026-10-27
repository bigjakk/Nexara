package handlers

// DRSHistory is the DRS tab's kind: the cluster's DRS configuration and
// evaluations, its DRS rules, and Proxmox's own HA rules, which the tab
// manages beside them. The moves DRS made are the Migrations sub-tab's.
func DRSHistory() HistoryKind {
	types := []string{"drs", "drs_rule", "ha_rule"}
	return HistoryKind{
		Name:       "drs",
		Categories: []string{"config", "evaluations", "rules", "ha_rules", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "evaluations", Field: HistoryFieldAction, Like: []string{`evaluate\_%`}},
			typeRule("config", "drs"),
			typeRule("rules", "drs_rule"),
			typeRule("ha_rules", "ha_rule"),
		},
		Select:      SelectTypes(types...),
		ObjectTypes: types,
		// Proxmox's HA rule is the HA tab's object, read under its own permission.
		ObservedTypes: observedOf("ha_rule"),
	}
}

// DRSRuleHistory is one DRS rule's kind: its creation and removal.
func DRSRuleHistory() HistoryKind {
	return HistoryKind{
		Name:        "drs-rule",
		Categories:  []string{"lifecycle", "other"},
		Default:     "other",
		Rules:       []HistoryRule{ruleCreatedDeleted},
		Select:      SelectObjects("rule_id", HistoryObject{ResourceType: "drs_rule"}),
		ObjectTypes: []string{"drs_rule"},
	}
}
