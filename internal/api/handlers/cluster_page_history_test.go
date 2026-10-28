package handlers

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// likeRegexp is a SQL LIKE pattern, backslash the escape, as a regexp.
func likeRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '\\':
			i++
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return regexp.MustCompile(b.String() + "$")
}

// categoryOf is what ListHistory does with a kind's rules: the first rule whose
// field matches its pattern decides, else the default.
func categoryOf(k HistoryKind, entry, action, resourceType, worker string) string {
	categories, fields, patterns := k.sqlRules()
	value := map[string]string{"entry": entry, "action": action, "resource_type": resourceType, "worker": worker}
	for i := range patterns {
		if likeRegexp(patterns[i]).MatchString(value[fields[i]]) {
			return categories[i]
		}
	}
	return k.Default
}

// The rules put the rows the cluster page's Histories read where their filters
// say: a tab's by the kind of object, an object's by what happened to it. One
// row for each rule that is not the plain verbs, and for the plain verbs on one
// kind of each shape.
func TestClusterPageHistoryCategories(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		kind                             HistoryKind
		entry, action, typ, worker, want string
	}{
		// A tab: by type, the arm and disarm by theirs.
		{HAHistory(), "audit", "created", "ha_group", "", "groups"},
		{HAHistory(), "audit", "arm_ha", "ha", "", "arming"},
		{HAHistory(), "audit", "created", "something_new", "", "other"},
		{AccessHistory(), "audit", "regenerated", "pve_token", "", "tokens"},
		{CephHistory(), "audit", "mon_create", "ceph_mgr", "", "monitors"},
		{FirewallHistory(), "audit", "rule_created", "firewall_security_group_rule", "", "groups"},
		{NetworksHistory(), "audit", "network_applied", "network", "", "applied"},
		{NetworksHistory(), "audit", "interface_updated", "network", "", "interfaces"},
		{NetworksHistory(), "audit", "sdn_applied", "sdn", "", "applied"},
		{DRSHistory(), "audit", "evaluate_blocked_native_crs", "drs", "", "evaluations"},
		{DRSHistory(), "audit", "config_update", "drs", "", "config"},
		{DRSHistory(), "audit", "created", "ha_rule", "", "ha_rules"},
		{OptionsHistory(), "audit", "description_updated", "cluster_options", "", "notes"},
		{OptionsHistory(), "audit", "updated", "cluster_options", "", "options"},
		// An object: by what happened to it, plain verbs and typed ones alike.
		{HAGroupHistory(), "audit", "created", "ha_group", "", "lifecycle"},
		{HAGroupHistory(), "audit", "already_deleted", "ha_group", "", "lifecycle"},
		{HAGroupHistory(), "audit", "updated", "ha_group", "", "config"},
		{HAGroupHistory(), "observed", "changed", "ha_group", "", "config"},
		{SDNZoneHistory(), "audit", "sdn_zone_created", "sdn_zone", "", "lifecycle"},
		{SDNZoneHistory(), "audit", "sdn_zone_updated", "sdn_zone", "", "config"},
		{SDNZoneHistory(), "audit", "sdn_zone_deleted", "sdn_zone", "", "lifecycle"},
		// ...and what the kind reads beside the object's own rows.
		{HAResourceHistory(), "audit", "hamigrate", "vm", "hamigrate", "tasks"},
		{HAResourceHistory(), "audit", "updated", "ha_resource", "", "config"},
		{PoolHistory(), "audit", "set_pool", "vm", "", "members"},
		{PoolHistory(), "audit", "deleted", "pool", "", "lifecycle"},
		{AccessUserHistory(), "audit", "granted", "pve_acl", "", "permissions"},
		{AccessUserHistory(), "audit", "created", "pve_token", "", "tokens"},
		{AccessUserHistory(), "audit", "created", "pve_user", "", "lifecycle"},
		{AccessRoleHistory(), "audit", "revoked", "pve_acl", "", "permissions"},
		{ReplicationJobHistory(), "audit", "triggered", "replication", "", "sync"},
		{CephOSDHistory(), "audit", "osd_destroy", "ceph_osd", "", "lifecycle"},
		{CephOSDHistory(), "audit", "osd_restart", "ceph_osd", "", "state"},
		{CephOSDHistory(), "observed", "changed", "ceph_osd", "", "state"},
		{CephPoolHistory(), "audit", "update", "ceph_pool", "", "config"},
		{CephMonitorHistory(), "audit", "mon_destroy", "ceph_mon", "", "lifecycle"},
		{NetworkInterfaceHistory(), "audit", "network_reverted", "network", "", "applied"},
		{NetworkInterfaceHistory(), "audit", "interface_created", "network", "", "lifecycle"},
		{SDNVNetHistory(), "audit", "sdn_subnet_deleted", "sdn_subnet", "", "subnets"},
		{FirewallIPSetHistory(), "audit", "created", "firewall_ipset_entry", "", "entries"},
		{FirewallIPSetHistory(), "audit", "deleted", "firewall_ipset", "", "lifecycle"},
		{FirewallGroupHistory(), "audit", "rule_updated", "firewall_security_group_rule", "", "rules"},
		{FirewallTemplateHistory(), "audit", "template_applied", "firewall_template", "", "applied"},
		{FirewallTemplateHistory(), "audit", "template_created", "firewall_template", "", "lifecycle"},
		{FirewallTemplateHistory(), "audit", "template_updated", "firewall_template", "", "config"},
		{NodeCertificateHistory(), "audit", "renewed", "certificate", "", "certificate"},
		{NodeCertificateHistory(), "audit", "updated", "acme_config", "", "config"},
		{DRSRuleHistory(), "audit", "rule_deleted", "drs_rule", "", "lifecycle"},
	} {
		if got := categoryOf(tt.kind, tt.entry, tt.action, tt.typ, tt.worker); got != tt.want {
			t.Errorf("%s: %s %s %s %s is %q, want %q", tt.kind.Name, tt.entry, tt.typ, tt.action, tt.worker, got, tt.want)
		}
	}
}

// The DRS tab shows the changes seen in Proxmox to the HA rules it lists, but
// the rules' own view decides who reads them: view:drs opens the tab, not the
// rules (view:ha).
func TestDRSHistoryObservedRulesNeedViewHA(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		granted []string
		want    []string
	}{
		{[]string{"drs"}, []string{}},
		{[]string{"drs", "ha"}, []string{"ha_rule"}},
	} {
		got, err := permittedObservedTypes(DRSHistory(), func(_, resource string) (bool, error) {
			return slices.Contains(tt.granted, resource), nil
		})
		if err != nil || !slices.Equal(got, tt.want) {
			t.Errorf("granted %v: permitted observed types = %v (%v), want %v", tt.granted, got, err, tt.want)
		}
	}
}
