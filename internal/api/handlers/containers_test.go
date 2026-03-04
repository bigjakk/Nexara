package handlers

import "testing"

func TestValidCTActions(t *testing.T) {
	valid := []string{"start", "stop", "shutdown", "reboot", "suspend", "resume"}
	for _, action := range valid {
		if !validCTActions[action] {
			t.Errorf("expected %q to be valid CT action", action)
		}
	}

	invalid := []string{"delete", "migrate", "reset", "snapshot", ""}
	for _, action := range invalid {
		if validCTActions[action] {
			t.Errorf("expected %q to be invalid CT action", action)
		}
	}
}
