package handlers

import (
	"context"

	"github.com/bigjakk/nexara/internal/auditkey"
)

// The audit types the cluster's Firewall tab holds. The rule set and the
// options are read by their one id, "cluster" — a rule's own id is its position,
// and a guest's and a node's rule sets are filed under theirs — and the rest
// whole.
var (
	firewallSetTypes   = []string{"firewall_rule", "firewall_options"}
	firewallWholeTypes = []string{"firewall_alias", "firewall_ipset", "firewall_ipset_entry",
		"firewall_security_group", "firewall_security_group_rule", "firewall_template"}
)

// FirewallHistory is the Firewall tab's kind: the cluster's rule set and
// options — which have no row of their own, so they are read here and nowhere
// else —, its aliases, IP sets and security groups with their entries and
// rules, and the templates applied to the cluster. A guest's and a node's rule
// sets are theirs: only the cluster's own is read. A template's creation,
// change and removal belong to no cluster; its History is the template's.
func FirewallHistory() HistoryKind {
	return HistoryKind{
		Name:       "firewall",
		Categories: []string{"rules", "options", "aliases", "ipsets", "groups", "templates", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("rules", "firewall_rule"),
			typeRule("options", "firewall_options"),
			typeRule("aliases", "firewall_alias"),
			typeRule("ipsets", "firewall_ipset", "firewall_ipset_entry"),
			typeRule("groups", "firewall_security_group", "firewall_security_group_rule"),
			typeRule("templates", "firewall_template"),
		},
		Select: func(context.Context, HistoryRequest) (HistorySelection, error) {
			sel := HistorySelection{}
			for _, t := range firewallSetTypes {
				sel.Objects = append(sel.Objects, HistoryObject{ResourceType: t, Value: "cluster"})
			}
			for _, t := range firewallWholeTypes {
				sel.Objects = append(sel.Objects, HistoryObject{ResourceType: t, WholeType: true})
			}
			return sel, nil
		},
		ObjectTypes: append(append([]string{}, firewallSetTypes...), firewallWholeTypes...),
		// A template's rows are Nexara's own, not a Proxmox object.
		ObservedTypes: observedOf(append(append([]string{}, firewallSetTypes...), firewallWholeTypes[:5]...)...),
	}
}

// FirewallAliasHistory is one alias's kind. An alias is filed under its lower-
// case name, which Proxmox stores it under, and a rename is a row of the old
// name that names the new one.
func FirewallAliasHistory() HistoryKind {
	k := objectHistory("firewall-alias", "name", []string{"firewall_alias"}, "firewall_alias")
	k.Select = func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
		name := auditkey.FirewallName(r.Params.String("name"))
		return HistorySelection{Objects: []HistoryObject{
			{ResourceType: "firewall_alias", Value: name},
			{ResourceType: "firewall_alias", DetailKey: "rename", Value: name},
		}}, nil
	}
	return k
}

// FirewallIPSetHistory is one IP set's kind: its own rows and its entries',
// which name it.
func FirewallIPSetHistory() HistoryKind {
	return HistoryKind{
		Name:       "firewall-ipset",
		Categories: []string{"lifecycle", "entries", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("entries", "firewall_ipset_entry"),
			ruleCreatedDeleted,
		},
		Select: func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
			name := auditkey.FirewallName(r.Params.String("name"))
			return HistorySelection{Objects: []HistoryObject{
				{ResourceType: "firewall_ipset", Value: name},
				{ResourceType: "firewall_ipset_entry", DetailKey: "set", Value: name},
			}}, nil
		},
		ObjectTypes:   []string{"firewall_ipset", "firewall_ipset_entry"},
		ObservedTypes: observedOf("firewall_ipset", "firewall_ipset_entry"),
	}
}

// FirewallGroupHistory is one security group's kind: its own rows and its
// rules', which are filed under the group.
func FirewallGroupHistory() HistoryKind {
	return HistoryKind{
		Name:       "firewall-group",
		Categories: []string{"lifecycle", "rules", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("rules", "firewall_security_group_rule"),
			ruleCreatedDeleted,
		},
		Select: func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
			group := auditkey.FirewallName(r.Params.String("group"))
			return HistorySelection{Objects: []HistoryObject{
				{ResourceType: "firewall_security_group", Value: group},
				{ResourceType: "firewall_security_group_rule", Value: group},
			}}, nil
		},
		ObjectTypes:   []string{"firewall_security_group", "firewall_security_group_rule"},
		ObservedTypes: observedOf("firewall_security_group", "firewall_security_group_rule"),
	}
}

// FirewallTemplateHistory is one firewall template's kind, the one global kind
// of the cluster page: a template belongs to Nexara, not to a cluster, so its
// creation, change and removal are filed under no cluster, and every cluster it
// was applied to files its own row. Its id is unique across Nexara, which is
// what lets it be read in every cluster (globalHistoryResourceTypes).
func FirewallTemplateHistory() HistoryKind {
	return HistoryKind{
		Name:       "firewall-template",
		Categories: []string{"lifecycle", "config", "applied", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			{Category: "applied", Field: HistoryFieldAction, Equals: []string{"template_applied"}},
			ruleCreatedDeleted, ruleUpdated,
		},
		Select:      SelectObjects("id", HistoryObject{ResourceType: "firewall_template"}),
		ObjectTypes: []string{"firewall_template"},
		Global:      true,
	}
}
