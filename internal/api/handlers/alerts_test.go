package handlers

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func baseAlertRule() db.AlertRule {
	return db.AlertRule{
		ID:              uuid.New(),
		Name:            "High CPU",
		Description:     "fires when cpu is hot",
		Enabled:         true,
		Severity:        "warning",
		Metric:          "cpu_usage",
		Operator:        ">",
		Threshold:       90,
		DurationSeconds: 300,
		ScopeType:       "cluster",
		CooldownSeconds: 600,
		EscalationChain: json.RawMessage(`[]`),
		MessageTemplate: "",
	}
}

// TestMergeAlertRuleUpdate pins the partial-update merge contract the UI
// relies on. Bodies decode through encoding/json like the real handler so the
// pointer-vs-zero distinction (nil = absent, non-nil 0 = explicit zero) is
// exercised end to end. Regression background: the frontend enable/disable
// toggle used to send a placeholder threshold of 0, which turned rules like
// "cpu_usage > 90" into "cpu_usage > 0".
func TestMergeAlertRuleUpdate(t *testing.T) {
	tests := []struct {
		name     string
		existing func(r db.AlertRule) db.AlertRule
		body     string
		wantErr  bool
		check    func(t *testing.T, existing db.AlertRule, params db.UpdateAlertRuleParams)
	}{
		{
			name: "enabled-only body keeps everything else",
			body: `{"enabled":false}`,
			check: func(t *testing.T, existing db.AlertRule, params db.UpdateAlertRuleParams) {
				// Explicit want-literal built from the existing row — an
				// independent oracle, deliberately not derived from a second
				// mergeAlertRuleUpdate call.
				want := db.UpdateAlertRuleParams{
					ID:              existing.ID,
					Name:            existing.Name,
					Description:     existing.Description,
					Enabled:         false,
					Severity:        existing.Severity,
					Metric:          existing.Metric,
					Operator:        existing.Operator,
					Threshold:       existing.Threshold,
					DurationSeconds: existing.DurationSeconds,
					ScopeType:       existing.ScopeType,
					ClusterID:       existing.ClusterID,
					NodeID:          existing.NodeID,
					VmVmid:          existing.VmVmid,
					CooldownSeconds: existing.CooldownSeconds,
					EscalationChain: existing.EscalationChain,
					MessageTemplate: existing.MessageTemplate,
				}
				if !reflect.DeepEqual(params, want) {
					t.Fatalf("params = %+v, want %+v", params, want)
				}
			},
		},
		{
			name: "explicit zero threshold is applied",
			body: `{"threshold":0}`,
			check: func(t *testing.T, _ db.AlertRule, params db.UpdateAlertRuleParams) {
				if params.Threshold != 0 {
					t.Fatalf("threshold = %v, want 0 (a present zero is an explicit set)", params.Threshold)
				}
				if !params.Enabled {
					t.Fatal("enabled must keep its stored value")
				}
			},
		},
		{
			name:    "invalid metric rejected",
			body:    `{"metric":"not_a_metric"}`,
			wantErr: true,
		},
		{
			// The metric/scope pair is validated on the MERGED values: moving
			// an existing node-scoped rule to snapshot_age_days must be
			// rejected even though the request never mentions scope_type.
			name: "snapshot_age_days with node scope rejected",
			existing: func(r db.AlertRule) db.AlertRule {
				r.ScopeType = "node"
				return r
			},
			body:    `{"metric":"snapshot_age_days"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := baseAlertRule()
			if tt.existing != nil {
				existing = tt.existing(existing)
			}
			var req createAlertRuleRequest
			if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
				t.Fatalf("unmarshal %q: %v", tt.body, err)
			}

			params, err := mergeAlertRuleUpdate(existing, req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("mergeAlertRuleUpdate(%s) = nil error, want rejection", tt.body)
				}
				return
			}
			if err != nil {
				t.Fatalf("mergeAlertRuleUpdate(%s): %v", tt.body, err)
			}
			if tt.check != nil {
				tt.check(t, existing, params)
			}
		})
	}
}
