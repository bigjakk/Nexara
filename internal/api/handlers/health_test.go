package handlers

import (
	"encoding/json"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestCephIssues(t *testing.T) {
	t.Parallel()

	mustJSON := func(items []proxmox.CephHealthCheckItem) []byte {
		b, err := json.Marshal(items)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}

	t.Run("healthy status yields no issues", func(t *testing.T) {
		t.Parallel()
		if got := cephIssues("HEALTH_OK", nil); got != nil {
			t.Errorf("HEALTH_OK = %+v, want nil", got)
		}
		if got := cephIssues("", nil); got != nil {
			t.Errorf("empty = %+v, want nil", got)
		}
	})

	t.Run("one issue per check, severity mapped", func(t *testing.T) {
		t.Parallel()
		checks := mustJSON([]proxmox.CephHealthCheckItem{
			{Type: "MON_DISK_LOW", Severity: "HEALTH_WARN", Message: "mon pve1 is low on available space"},
			{Type: "OSD_DOWN", Severity: "HEALTH_ERR", Message: "1 osds down"},
		})
		got := cephIssues("HEALTH_WARN", checks)
		if len(got) != 2 {
			t.Fatalf("got %d issues, want 2", len(got))
		}
		for _, iss := range got {
			if iss.Type != "ceph" || iss.Scope != "cluster" {
				t.Errorf("unexpected issue shape: %+v", iss)
			}
		}
		if got[0].Severity != healthSevWarn || got[0].Detail != "mon pve1 is low on available space" {
			t.Errorf("check[0] = %+v", got[0])
		}
		if got[1].Severity != healthSevErr {
			t.Errorf("check[1] severity = %q, want err", got[1].Severity)
		}
	})

	t.Run("status without checks still surfaces the status", func(t *testing.T) {
		t.Parallel()
		got := cephIssues("HEALTH_ERR", nil)
		if len(got) != 1 || got[0].Severity != healthSevErr {
			t.Fatalf("got %+v, want one err issue", got)
		}
	})
}

func TestSortIssues(t *testing.T) {
	t.Parallel()
	items := []healthIssueResponse{
		{Severity: healthSevWarn, Summary: "Storage near full"},
		{Severity: healthSevErr, Summary: "Node offline", Target: "pve2"},
		{Severity: healthSevErr, Summary: "Node offline", Target: "pve1"},
		{Severity: healthSevWarn, Summary: "Failed tasks"},
	}
	sortIssues(items)

	// Errors first.
	if items[0].Severity != healthSevErr || items[1].Severity != healthSevErr {
		t.Fatalf("errors should sort first, got %+v", items)
	}
	// Within errors, alphabetical by target (pve1 before pve2).
	if items[0].Target != "pve1" || items[1].Target != "pve2" {
		t.Errorf("error order by target wrong: %q, %q", items[0].Target, items[1].Target)
	}
	// Within warnings, alphabetical by summary ("Failed tasks" before "Storage near full").
	if items[2].Summary != "Failed tasks" || items[3].Summary != "Storage near full" {
		t.Errorf("warn order by summary wrong: %q, %q", items[2].Summary, items[3].Summary)
	}
}
