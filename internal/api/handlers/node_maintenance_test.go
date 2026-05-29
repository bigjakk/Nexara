package handlers

import "testing"

func TestNodeMaintenanceCommand(t *testing.T) {
	if got := nodeMaintenanceCommand(true, "pve1"); got != "ha-manager crm-command node-maintenance enable 'pve1'" {
		t.Errorf("enable cmd = %q", got)
	}
	if got := nodeMaintenanceCommand(false, "pve1"); got != "ha-manager crm-command node-maintenance disable 'pve1'" {
		t.Errorf("disable cmd = %q", got)
	}
}

func TestNodeMaintenanceNameRe(t *testing.T) {
	valid := []string{"pve1", "pve-01", "node.example", "HV01", "a_b"}
	for _, n := range valid {
		if !nodeMaintenanceNameRe.MatchString(n) {
			t.Errorf("expected %q to be accepted", n)
		}
	}
	// Injection / metacharacter attempts must be rejected.
	invalid := []string{
		"", "pve 1", "pve;rm -rf /", "pve$(whoami)", "pve`id`",
		"pve'1", "pve|x", "pve/..", "pve&", "pve\n", "pve>out",
	}
	for _, n := range invalid {
		if nodeMaintenanceNameRe.MatchString(n) {
			t.Errorf("expected %q to be rejected", n)
		}
	}
}
