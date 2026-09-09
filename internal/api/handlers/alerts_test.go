package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

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
		ClusterID:       pgtype.UUID{Bytes: uuid.New(), Valid: true},
		CooldownSeconds: 600,
		EscalationChain: json.RawMessage(`[]`),
		MessageTemplate: "",
	}
}

// storedScope is the scope resolveAlertRuleScope hands the merge for a request
// that leaves every scope field alone.
func storedScope(r db.AlertRule) alertRuleScope {
	return alertRuleScope{
		ScopeType: r.ScopeType,
		ClusterID: r.ClusterID,
		NodeID:    r.NodeID,
		VMVmid:    r.VmVmid,
	}
}

// TestMergeAlertRuleUpdate pins the partial-update merge contract the UI relies
// on. Bodies decode through encoding/json like the real handler so the
// pointer-vs-zero distinction (nil = absent, non-nil zero = explicit set) is
// exercised end to end. Regression background: the enable/disable toggle used
// to send a placeholder threshold of 0, which turned rules like
// "cpu_usage > 90" into "cpu_usage > 0".
func TestMergeAlertRuleUpdate(t *testing.T) {
	tests := []struct {
		name     string
		existing func(r db.AlertRule) db.AlertRule
		// scope is what the handler would have resolved for this body; nil
		// means the request touched no scope field, so the stored one stands.
		scope   func(r db.AlertRule) alertRuleScope
		body    string
		wantErr bool
		check   func(t *testing.T, existing db.AlertRule, params db.UpdateAlertRuleParams)
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
			// "" is the sentinel the engine reads as "use the built-in
			// message", so clearing has to be expressible on the wire.
			name: "empty message_template clears the custom template",
			existing: func(r db.AlertRule) db.AlertRule {
				r.MessageTemplate = "{{.ResourceName}} is hot"
				return r
			},
			body: `{"message_template":""}`,
			check: func(t *testing.T, _ db.AlertRule, params db.UpdateAlertRuleParams) {
				if params.MessageTemplate != "" {
					t.Fatalf("message_template = %q, want cleared", params.MessageTemplate)
				}
			},
		},
		{
			name: "omitted message_template keeps the stored one",
			existing: func(r db.AlertRule) db.AlertRule {
				r.MessageTemplate = "{{.ResourceName}} is hot"
				return r
			},
			body: `{"enabled":true}`,
			check: func(t *testing.T, existing db.AlertRule, params db.UpdateAlertRuleParams) {
				if params.MessageTemplate != existing.MessageTemplate {
					t.Fatalf("message_template = %q, want %q", params.MessageTemplate, existing.MessageTemplate)
				}
			},
		},
		{
			name: "empty description clears it",
			body: `{"description":""}`,
			check: func(t *testing.T, _ db.AlertRule, params db.UpdateAlertRuleParams) {
				if params.Description != "" {
					t.Fatalf("description = %q, want cleared", params.Description)
				}
			},
		},
		{
			// A vm→cluster rescope used to keep vm_vmid, which the engine then
			// stamped onto every alert the rule raised.
			name: "rescoping to cluster drops the vm binding",
			existing: func(r db.AlertRule) db.AlertRule {
				r.ScopeType = "vm"
				r.VmVmid = pgtype.Int4{Int32: 101, Valid: true}
				return r
			},
			scope: func(r db.AlertRule) alertRuleScope {
				s := storedScope(r)
				s.ScopeType = "cluster"
				return s
			},
			body: `{"scope_type":"cluster"}`,
			check: func(t *testing.T, _ db.AlertRule, params db.UpdateAlertRuleParams) {
				if params.VmVmid.Valid {
					t.Fatalf("vm_vmid = %+v, want cleared on a cluster-scoped rule", params.VmVmid)
				}
			},
		},
		{
			name: "rescoping to vm without a vmid is rejected",
			scope: func(r db.AlertRule) alertRuleScope {
				s := storedScope(r)
				s.ScopeType = "vm"
				return s
			},
			body:    `{"scope_type":"vm"}`,
			wantErr: true,
		},
		{
			name: "rescoping to node without a node_id is rejected",
			scope: func(r db.AlertRule) alertRuleScope {
				s := storedScope(r)
				s.ScopeType = "node"
				return s
			},
			body:    `{"scope_type":"node"}`,
			wantErr: true,
		},
		{
			// Rules predating the scope checks may be incoherent; the toggle
			// must not start failing on them.
			name: "untouched scope is not re-checked on a legacy incoherent rule",
			existing: func(r db.AlertRule) db.AlertRule {
				r.ScopeType = "vm" // stored without a vm_vmid
				return r
			},
			body: `{"enabled":false}`,
			check: func(t *testing.T, _ db.AlertRule, params db.UpdateAlertRuleParams) {
				if params.ScopeType != "vm" {
					t.Fatalf("scope_type = %q, want the stored value untouched", params.ScopeType)
				}
			},
		},
		{
			name:    "invalid metric rejected",
			body:    `{"metric":"not_a_metric"}`,
			wantErr: true,
		},
		{
			name:    "invalid severity rejected",
			body:    `{"severity":"catastrophic"}`,
			wantErr: true,
		},
		{
			name:    "out-of-range duration rejected",
			body:    `{"duration_seconds":86401}`,
			wantErr: true,
		},
		{
			// The metric/scope pair is validated on the MERGED values: moving
			// an existing node-scoped rule to snapshot_age_days must be
			// rejected even though the request never mentions scope_type.
			name: "snapshot_age_days with node scope rejected",
			existing: func(r db.AlertRule) db.AlertRule {
				r.ScopeType = "node"
				r.NodeID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
				return r
			},
			body:    `{"metric":"snapshot_age_days"}`,
			wantErr: true,
		},
		{
			// Same rule for the Veeam guest metrics, which read the same kind
			// of per-guest inventory.
			name: "veeam_rpo_hours with node scope rejected",
			existing: func(r db.AlertRule) db.AlertRule {
				r.ScopeType = "node"
				r.NodeID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
				return r
			},
			body:    `{"metric":"veeam_rpo_hours"}`,
			wantErr: true,
		},
		{
			// A repository is shared across every cluster its server
			// protects, so a cluster-scoped rule would be a claim about
			// infrastructure that cluster does not own.
			name:    "veeam_repo_used_percent with cluster scope rejected",
			body:    `{"metric":"veeam_repo_used_percent"}`,
			wantErr: true,
		},
		{
			// And the converse: a per-node metric has nothing to read at
			// global scope, so it would never fire either.
			name: "cpu_usage with global scope rejected",
			scope: func(r db.AlertRule) alertRuleScope {
				s := storedScope(r)
				s.ScopeType = "global"
				return s
			},
			body:    `{"scope_type":"global"}`,
			wantErr: true,
		},
		{
			// A malware verdict is 0-3. The form's percentage default of 90
			// produced a rule that was accepted, stored, evaluated every tick,
			// and could never be true.
			name:    "veeam_malware_status threshold out of range rejected",
			body:    `{"metric":"veeam_malware_status","threshold":90}`,
			wantErr: true,
		},
		{
			name:    "veeam_malware_status threshold in range accepted",
			body:    `{"metric":"veeam_malware_status","threshold":2}`,
			wantErr: false,
		},
		{
			name: "veeam_repo_used_percent with global scope accepted",
			scope: func(r db.AlertRule) alertRuleScope {
				s := storedScope(r)
				s.ScopeType = "global"
				return s
			},
			body:    `{"metric":"veeam_repo_used_percent","scope_type":"global"}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := baseAlertRule()
			if tt.existing != nil {
				existing = tt.existing(existing)
			}
			scope := storedScope(existing)
			if tt.scope != nil {
				scope = tt.scope(existing)
			}
			var req createAlertRuleRequest
			if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
				t.Fatalf("unmarshal %q: %v", tt.body, err)
			}

			params, err := mergeAlertRuleUpdate(existing, req, scope)
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

func TestNormalizeAlertRuleScope(t *testing.T) {
	cluster := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	node := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	vmid := pgtype.Int4{Int32: 101, Valid: true}

	tests := []struct {
		name     string
		in       alertRuleScope
		wantNode bool
		wantVM   bool
	}{
		{
			name: "cluster scope keeps neither binding",
			in:   alertRuleScope{ScopeType: "cluster", ClusterID: cluster, NodeID: node, VMVmid: vmid},
		},
		{
			name:     "node scope keeps the node only",
			in:       alertRuleScope{ScopeType: "node", ClusterID: cluster, NodeID: node, VMVmid: vmid},
			wantNode: true,
		},
		{
			name:   "vm scope keeps the vmid only",
			in:     alertRuleScope{ScopeType: "vm", ClusterID: cluster, NodeID: node, VMVmid: vmid},
			wantVM: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeAlertRuleScope(tt.in)
			if got.NodeID.Valid != tt.wantNode {
				t.Errorf("node_id valid = %v, want %v", got.NodeID.Valid, tt.wantNode)
			}
			if got.VMVmid.Valid != tt.wantVM {
				t.Errorf("vm_vmid valid = %v, want %v", got.VMVmid.Valid, tt.wantVM)
			}
			if got.ClusterID != tt.in.ClusterID {
				t.Errorf("cluster_id = %+v, want it untouched", got.ClusterID)
			}
		})
	}
}

// Every scope type needs the binding the engine evaluates on; without it the
// rule is accepted and then fails on every tick without ever firing.
func TestValidateAlertRuleScope(t *testing.T) {
	cluster := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	node := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	vmid := pgtype.Int4{Int32: 101, Valid: true}

	tests := []struct {
		name    string
		scope   alertRuleScope
		wantErr bool
	}{
		{name: "cluster with cluster_id", scope: alertRuleScope{ScopeType: "cluster", ClusterID: cluster}},
		{name: "cluster without cluster_id", scope: alertRuleScope{ScopeType: "cluster"}, wantErr: true},
		{name: "node with node_id", scope: alertRuleScope{ScopeType: "node", ClusterID: cluster, NodeID: node}},
		{name: "node without node_id", scope: alertRuleScope{ScopeType: "node", ClusterID: cluster}, wantErr: true},
		{name: "vm with cluster and vmid", scope: alertRuleScope{ScopeType: "vm", ClusterID: cluster, VMVmid: vmid}},
		{name: "vm without vmid", scope: alertRuleScope{ScopeType: "vm", ClusterID: cluster}, wantErr: true},
		{name: "vm without cluster_id", scope: alertRuleScope{ScopeType: "vm", VMVmid: vmid}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAlertRuleScope(tt.scope)
			if tt.wantErr && err == nil {
				t.Fatal("got nil error, want rejection")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("got %v, want accepted", err)
			}
		})
	}
}

// newAlertRuleTestApp wires CreateRule with NIL queries: every case below must
// reach its verdict before any database access, so a regression that moves the
// node lookup ahead of the permission gate panics on the nil Queries and fails
// loudly instead of silently reintroducing a node-existence oracle.
func newAlertRuleTestApp(t *testing.T) *fiber.App {
	t.Helper()
	handler := NewAlertHandler(nil, testEncryptionKey, nil, nil)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Use(func(c fiber.Ctx) error {
		if role := c.Get("X-Test-Role"); role != "" {
			c.Locals("role", role)
			c.Locals("user_id", uuid.New())
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)
	app.Post("/alert-rules", handler.CreateRule)
	return app
}

// A rule whose scope has no binding is one the engine can never evaluate: it
// errors once per tick, forever, and the rule silently never fires. Creating
// one used to return 201.
func TestCreateRule_GatesAndScopeBindings(t *testing.T) {
	app := newAlertRuleTestApp(t)
	clusterID := uuid.New().String()
	nodeID := uuid.New().String()
	const base = `"name":"x","metric":"cpu_usage","operator":">","threshold":90`

	tests := []struct {
		name   string
		role   string
		body   string
		status int
	}{
		{
			name:   "a node-scoped create is refused before the node lookup",
			role:   "viewer",
			body:   `{` + base + `,"scope_type":"node","cluster_id":"` + clusterID + `","node_id":"` + nodeID + `"}`,
			status: http.StatusForbidden,
		},
		{
			name:   "no cluster permission is refused",
			role:   "viewer",
			body:   `{` + base + `,"cluster_id":"` + clusterID + `"}`,
			status: http.StatusForbidden,
		},
		{
			name:   "a rule naming no cluster needs global manage:alert",
			role:   "viewer",
			body:   `{` + base + `}`,
			status: http.StatusForbidden,
		},
		{
			name:   "node_id without a cluster_id would authorize after the lookup",
			role:   "admin",
			body:   `{` + base + `,"scope_type":"node","node_id":"` + nodeID + `"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "cluster scope needs a cluster",
			role:   "admin",
			body:   `{` + base + `}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "node scope needs a node",
			role:   "admin",
			body:   `{` + base + `,"scope_type":"node","cluster_id":"` + clusterID + `"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "vm scope needs a vmid",
			role:   "admin",
			body:   `{` + base + `,"scope_type":"vm","cluster_id":"` + clusterID + `"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "a vmid on a cluster-scoped rule is refused, not dropped",
			role:   "admin",
			body:   `{` + base + `,"cluster_id":"` + clusterID + `","vm_vmid":101}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "name is required",
			role:   "admin",
			body:   `{"metric":"cpu_usage","operator":">","threshold":90,"cluster_id":"` + clusterID + `"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "threshold is required",
			role:   "admin",
			body:   `{"name":"x","metric":"cpu_usage","operator":">","cluster_id":"` + clusterID + `"}`,
			status: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/alert-rules", bytes.NewReader([]byte(tt.body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Role", tt.role)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tt.status {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want %d (body %s)", resp.StatusCode, tt.status, body)
			}
		})
	}
}

func TestValidateAlertRuleBindings(t *testing.T) {
	vmid := int32(101)

	tests := []struct {
		name      string
		req       createAlertRuleRequest
		scopeType string
		wantErr   bool
	}{
		{name: "node_id with node scope", req: createAlertRuleRequest{NodeID: uuid.New().String()}, scopeType: "node"},
		{name: "node_id with cluster scope", req: createAlertRuleRequest{NodeID: uuid.New().String()}, scopeType: "cluster", wantErr: true},
		{name: "node_id with vm scope", req: createAlertRuleRequest{NodeID: uuid.New().String()}, scopeType: "vm", wantErr: true},
		{name: "vm_vmid with vm scope", req: createAlertRuleRequest{VMVmid: &vmid}, scopeType: "vm"},
		{name: "vm_vmid with cluster scope", req: createAlertRuleRequest{VMVmid: &vmid}, scopeType: "cluster", wantErr: true},
		{name: "vm_vmid with node scope", req: createAlertRuleRequest{VMVmid: &vmid}, scopeType: "node", wantErr: true},
		{name: "no binding at all", req: createAlertRuleRequest{}, scopeType: "cluster"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAlertRuleBindings(tt.req, tt.scopeType)
			if tt.wantErr && err == nil {
				t.Fatal("got nil error, want rejection")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("got %v, want accepted", err)
			}
		})
	}
}

func TestAlertRuleScopeTouched(t *testing.T) {
	vmid := int32(101)

	tests := []struct {
		name string
		req  createAlertRuleRequest
		want bool
	}{
		{name: "empty body", req: createAlertRuleRequest{}},
		{name: "enabled only", req: createAlertRuleRequest{Enabled: new(bool)}},
		{name: "scope_type", req: createAlertRuleRequest{ScopeType: "vm"}, want: true},
		{name: "cluster_id", req: createAlertRuleRequest{ClusterID: uuid.New().String()}, want: true},
		{name: "node_id", req: createAlertRuleRequest{NodeID: uuid.New().String()}, want: true},
		{name: "vm_vmid", req: createAlertRuleRequest{VMVmid: &vmid}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := alertRuleScopeTouched(tt.req); got != tt.want {
				t.Fatalf("alertRuleScopeTouched = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMaxDurationFor pins the metric-aware duration cap.
//
// The uniform 86400 cap that preceded it made pve_backup_failed unable to watch
// a weekly backup at all: the window could never reach back far enough to
// include the run it existed to check, so the rule was accepted, stored,
// evaluated every tick, and could never fire. The browser caught this — the
// API rejected a 30-day window with "must be between 0 and 86400" — which no
// unit test would have, because every test until now used the default 300.
func TestMaxDurationFor(t *testing.T) {
	tests := []struct {
		metric string
		want   int32
	}{
		// Windowed: duration_seconds is the span being counted.
		{"pve_backup_failed", maxWindowedDurationSec},
		{"pve_task_failed", maxWindowedDurationSec},
		// Level metrics: duration_seconds is how long the threshold must hold,
		// where more than a day is already an extreme rule.
		{"cpu_usage", maxDurationSec},
		{"mem_percent", maxDurationSec},
		{"veeam_job_failed", maxDurationSec},
		{"snapshot_age_days", maxDurationSec},
		// An unknown metric is rejected before the cap is consulted; the
		// conservative default is still the right answer here.
		{"bogus", maxDurationSec},
	}
	for _, tt := range tests {
		if got := maxDurationFor(tt.metric); got != tt.want {
			t.Errorf("maxDurationFor(%q) = %d, want %d", tt.metric, got, tt.want)
		}
	}

	if maxWindowedDurationSec <= 7*24*60*60 {
		t.Errorf("windowed cap %d does not exceed a week, so a weekly backup "+
			"cannot be covered by its own alert window", maxWindowedDurationSec)
	}
}
