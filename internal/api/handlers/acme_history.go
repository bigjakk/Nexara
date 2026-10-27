package handlers

import "context"

// CertificatesHistory is the Certificates tab's kind: the cluster's ACME
// accounts and plugins, and each node's ACME settings and certificate, told
// apart by type.
func CertificatesHistory() HistoryKind {
	types := []string{"acme_account", "acme_plugin", "acme_config", "certificate"}
	return HistoryKind{
		Name:       "certificates",
		Categories: []string{"accounts", "plugins", "nodes", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("accounts", "acme_account"),
			typeRule("plugins", "acme_plugin"),
			typeRule("nodes", "acme_config", "certificate"),
		},
		Select:        SelectTypes(types...),
		ObjectTypes:   types,
		ObservedTypes: observedOf("acme_account", "acme_plugin", "acme_config"),
	}
}

// ACMEAccountHistory is one ACME account's kind.
func ACMEAccountHistory() HistoryKind {
	return objectHistory("acme-account", "name", []string{"acme_account"}, "acme_account")
}

// ACMEPluginHistory is one ACME plugin's kind.
func ACMEPluginHistory() HistoryKind {
	return objectHistory("acme-plugin", "plugin_id", []string{"acme_plugin"}, "acme_plugin")
}

// NodeCertificateHistory is one node's certificate kind: the certificates
// ordered, renewed and revoked for it — in Nexara or in Proxmox — and the ACME
// settings saved on it.
func NodeCertificateHistory() HistoryKind {
	return HistoryKind{
		Name:       "node-certificate",
		Categories: []string{"certificate", "config", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("certificate", "certificate"),
			ruleUpdated, ruleObserved,
		},
		Select: func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
			node := r.Params.String("node")
			return HistorySelection{Objects: []HistoryObject{
				{ResourceType: "certificate", Value: node},
				{ResourceType: "acme_config", Value: node},
			}}, nil
		},
		ObjectTypes:   []string{"certificate", "acme_config"},
		ObservedTypes: observedOf("acme_config"),
	}
}
