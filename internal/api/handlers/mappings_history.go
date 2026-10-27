package handlers

// MappingsHistory is the Resource Mappings tab's kind: the cluster's USB and
// PCI mappings, told apart by type.
func MappingsHistory() HistoryKind {
	return HistoryKind{
		Name:       "mappings",
		Categories: []string{"usb", "pci", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("usb", "usb_mapping"),
			typeRule("pci", "pci_mapping"),
		},
		Select:        SelectTypes("usb_mapping", "pci_mapping"),
		ObjectTypes:   []string{"usb_mapping", "pci_mapping"},
		ObservedTypes: observedOf("usb_mapping", "pci_mapping"),
	}
}

// USBMappingHistory is one USB mapping's kind.
func USBMappingHistory() HistoryKind {
	return objectHistory("usb-mapping", "mapping_id", []string{"usb_mapping"}, "usb_mapping")
}

// PCIMappingHistory is one PCI mapping's kind.
func PCIMappingHistory() HistoryKind {
	return objectHistory("pci-mapping", "mapping_id", []string{"pci_mapping"}, "pci_mapping")
}
