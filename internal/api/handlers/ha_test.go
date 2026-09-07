package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// newHARulesServer serves GET /cluster/ha/rules with a fixed body and status.
func newHARulesServer(t *testing.T, status int, body string) *proxmox.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/cluster/ha/rules" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:     srv.URL,
		TokenID:     "user@pam!test",
		TokenSecret: "secret-token-value",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

// The whole point of the two-value signature: "not in the list" and "could not
// read the list" are different answers, and the caller has to be able to tell.
// The single-return version returned nil for both, which is how DeleteRule came
// to audit a deletion that never happened.
func TestFindHARule_DistinguishesAbsentFromUnreadable(t *testing.T) {
	const rules = `{"data":[{"rule":"keep-apart","type":"resource-affinity",` +
		`"resources":"vm:100,vm:101","affinity":"negative","comment":"spread"}]}`

	t.Run("present", func(t *testing.T) {
		got, err := findHARule(context.Background(), newHARulesServer(t, http.StatusOK, rules), "keep-apart")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got == nil {
			t.Fatal("rule = nil, want the entry")
		}
		if got.Affinity != "negative" || got.Resources != "vm:100,vm:101" {
			t.Errorf("snapshot = %+v, want the listed rule's fields", got)
		}
	})

	t.Run("confirmed absent", func(t *testing.T) {
		got, err := findHARule(context.Background(), newHARulesServer(t, http.StatusOK, rules), "gone")
		if got != nil {
			t.Errorf("rule = %+v, want nil", got)
		}
		if err != nil {
			t.Errorf("err = %v, want nil — the list was read, the rule was not in it", err)
		}
	})

	t.Run("could not tell", func(t *testing.T) {
		got, err := findHARule(context.Background(), newHARulesServer(t, http.StatusInternalServerError, `{}`), "keep-apart")
		if got != nil {
			t.Errorf("rule = %+v, want nil", got)
		}
		if err == nil {
			t.Fatal("err = nil, want an error — a failed list read must not read as an absent rule")
		}
	})
}

func TestClassifyHARuleDelete(t *testing.T) {
	snapshot := &proxmox.HARuleEntry{Rule: "keep-apart", Type: "resource-affinity"}
	readFailed := errors.New("get HA rules: connection failed")

	tests := []struct {
		name             string
		snapshot         *proxmox.HARuleEntry
		snapErr          error
		wantAction       string
		wantPriorUnknown bool
	}{
		{
			name:       "rule was there — a real deletion",
			snapshot:   snapshot,
			wantAction: "deleted",
		},
		{
			// The regression. PVE answers 200 for an absent rule, so before
			// this branch existed every stale-list delete wrote a row claiming
			// a state change that never happened.
			name:       "rule was already gone — nothing was deleted",
			wantAction: "already_deleted",
		},
		{
			name:             "list read failed — the prior state is unknowable",
			snapErr:          readFailed,
			wantAction:       "deleted",
			wantPriorUnknown: true,
		},
		{
			// findHARule never returns a rule alongside an error, so this
			// is unreachable today. It pins the switch ordering anyway: if a
			// future lookup can report both, direct evidence of the rule must
			// outrank a stale read error rather than downgrade the row.
			name:       "snapshot present despite an error — the snapshot wins",
			snapshot:   snapshot,
			snapErr:    readFailed,
			wantAction: "deleted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action, priorUnknown := classifyHARuleDelete(tt.snapshot, tt.snapErr)
			if action != tt.wantAction {
				t.Errorf("action = %q, want %q", action, tt.wantAction)
			}
			if priorUnknown != tt.wantPriorUnknown {
				t.Errorf("priorStateUnknown = %v, want %v", priorUnknown, tt.wantPriorUnknown)
			}
		})
	}
}

// Both nil-snapshot cases have to be distinguishable in the audit log, or the
// fix has only moved the ambiguity from findHARule into the row.
func TestClassifyHARuleDelete_NilSnapshotCasesAreNotInterchangeable(t *testing.T) {
	absentAction, absentUnknown := classifyHARuleDelete(nil, nil)
	unknownAction, unknownUnknown := classifyHARuleDelete(nil, errors.New("get HA rules: connection failed"))

	if absentAction == unknownAction && absentUnknown == unknownUnknown {
		t.Fatalf("confirmed-absent and could-not-tell both produce (%q, %v) — an audit reader cannot tell them apart",
			absentAction, absentUnknown)
	}
	if absentAction == "deleted" {
		t.Errorf("confirmed-absent audits as %q; a no-op must not claim a deletion", absentAction)
	}
}
