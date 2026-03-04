package handlers

import "testing"

func TestExtractNodeFromUPID(t *testing.T) {
	tests := []struct {
		upid string
		want string
	}{
		{"UPID:pve1:000012:00AB:65000000:qmstart:100:user@pam:", "pve1"},
		{"UPID:node-02:000012:00AB:65000000:qmstop:101:admin@pve:", "node-02"},
		{"UPID:my-node:00FF:0A0B:65123456:qmshutdown:200:user@pam:", "my-node"},
		{"invalid", ""},
		{"", ""},
		{"UPID:", ""},
	}

	for _, tt := range tests {
		got := extractNodeFromUPID(tt.upid)
		if got != tt.want {
			t.Errorf("extractNodeFromUPID(%q) = %q, want %q", tt.upid, got, tt.want)
		}
	}
}

func TestSplitUPID(t *testing.T) {
	parts := splitUPID("UPID:pve1:000012:00AB:65000000:qmstart:100:user@pam:")
	if len(parts) != 8 {
		t.Fatalf("expected 8 parts, got %d: %v", len(parts), parts)
	}
	if parts[0] != "UPID" {
		t.Errorf("parts[0] = %q, want UPID", parts[0])
	}
	if parts[1] != "pve1" {
		t.Errorf("parts[1] = %q, want pve1", parts[1])
	}
}

func TestValidVMActions(t *testing.T) {
	valid := []string{"start", "stop", "shutdown", "reboot", "reset", "suspend", "resume"}
	for _, action := range valid {
		if !validVMActions[action] {
			t.Errorf("expected %q to be valid", action)
		}
	}

	invalid := []string{"delete", "migrate", "snapshot", ""}
	for _, action := range invalid {
		if validVMActions[action] {
			t.Errorf("expected %q to be invalid", action)
		}
	}
}
