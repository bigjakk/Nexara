package handlers

// UpsDeviceHistory is a UPS's: its power events (ups_events — an outage, low
// battery, lost contact, a self-test), each an entry of kind power at its
// start, and what was done to it through Nexara — commands run and variables
// set (ups_command_*), what it powers changed (ups_feed_*). Global: a UPS is
// read through a NUT server that belongs to no one cluster, its command rows
// name none, and a feed's row is on the cluster of the node it names.
func UpsDeviceHistory() HistoryKind {
	return HistoryKind{
		Name:       "ups-device",
		Categories: []string{"power", "commands", "feeds", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "power", Field: HistoryFieldEntry, Equals: []string{"power"}},
			{Category: "commands", Field: HistoryFieldAction, Like: []string{`ups\_command\_%`}},
			{Category: "feeds", Field: HistoryFieldAction, Like: []string{`ups\_feed\_%`}},
		},
		Select:      SelectObjects("id", HistoryObject{ResourceType: "ups_device"}),
		Global:      true,
		ObjectTypes: []string{"ups_device"},
	}
}
