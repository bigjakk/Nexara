package handlers

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

func TestMapProxmoxError_InvalidInputIsClientError(t *testing.T) {
	// A rejected identifier never reaches Proxmox, so it must not be reported as
	// a Proxmox failure — the operator needs to know their input was the problem.
	err := fmt.Errorf("%w: volume id %q has a bad path segment %q",
		proxmox.ErrInvalidInput, "local:iso/../x", "..")

	var fe *fiber.Error
	if !errors.As(mapProxmoxError(err), &fe) {
		t.Fatalf("mapProxmoxError returned %T, want *fiber.Error", mapProxmoxError(err))
	}
	if fe.Code != fiber.StatusBadRequest {
		t.Errorf("status = %d, want %d", fe.Code, fiber.StatusBadRequest)
	}
	if !strings.Contains(fe.Message, "bad path segment") {
		t.Errorf("message = %q, want it to explain the rejection", fe.Message)
	}
}

func TestMapProxmoxError_SentinelsKeepTheirStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"not found", proxmox.ErrNotFound, fiber.StatusNotFound},
		{"forbidden", proxmox.ErrForbidden, fiber.StatusForbidden},
		{"connection failed", proxmox.ErrConnectionFailed, fiber.StatusBadGateway},
		{"unknown", errors.New("boom"), fiber.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(mapProxmoxError(tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.want {
				t.Errorf("status = %d, want %d", fe.Code, tt.want)
			}
		})
	}
}

// Proxmox answers with a JSON envelope, and mapProxmoxError's 502 message goes
// straight onto the operator's screen (describeError in ClusterCephTab.tsx), so
// what it picks out of that envelope is user-visible text.
func TestMapProxmoxError_APIErrorEnvelope(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		wantCode int
		wantMsg  string
	}{
		{
			// The case that motivated this: a cluster with no Ceph. The
			// operator was shown the whole document, braces and all.
			name:     "envelope message is unwrapped and trimmed",
			message:  `{"message":"binary not installed: /usr/bin/ceph-mon\n","data":null}`,
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "binary not installed: /usr/bin/ceph-mon",
		},
		{
			// Same flattened text, but with no rejection map alongside it, so
			// there is nothing to say the operator's input was at fault. The
			// map is what promotes this to a 400 — see the Fields case below.
			name:     "flattened text alone is not a validation failure",
			message:  "vmid: property is not defined in schema",
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "vmid: property is not defined in schema",
		},
		{
			name:     "plain text is passed through untouched",
			message:  "500 Internal Server Error",
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "500 Internal Server Error",
		},
		{
			// Nothing to unwrap, so the envelope remains the most informative
			// thing available — better than an empty error.
			name:     "envelope with no message falls back to the raw body",
			message:  `{"data":null}`,
			wantCode: fiber.StatusBadGateway,
			wantMsg:  `{"data":null}`,
		},
		{
			name:     "whitespace-only message falls back to the raw body",
			message:  `{"message":"  \n ","data":null}`,
			wantCode: fiber.StatusBadGateway,
			wantMsg:  `{"message":"  \n ","data":null}`,
		},
		{
			name:     "empty message falls back to the raw body",
			message:  "",
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "",
		},
		{
			// A bare JSON scalar does not unmarshal into the envelope struct.
			name:     "non-object JSON is passed through untouched",
			message:  "[1,2]",
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "[1,2]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &proxmox.APIError{StatusCode: 500, Message: tt.message}

			var fe *fiber.Error
			if !errors.As(mapProxmoxError(err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", fe.Code, tt.wantCode)
			}
			if fe.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMsg)
			}
		})
	}
}

// A rejected parameter is the operator's own typo, so it must not be dressed up
// as an upstream gateway failure. The APIError below is exactly what
// proxmox.checkStatus builds from a real PVE parameter-verification body — see
// TestCheckStatus_ParameterVerificationKeepsTheFieldMap, which pins that half.
func TestMapProxmoxError_ParameterRejectionIsAClientError(t *testing.T) {
	err := &proxmox.APIError{
		StatusCode: 400,
		Message:    "vmid: property is not defined in schema",
		Fields:     map[string]string{"vmid": "property is not defined in schema"},
	}

	var fe *fiber.Error
	if !errors.As(mapProxmoxError(err), &fe) {
		t.Fatal("want *fiber.Error")
	}
	if fe.Code != fiber.StatusBadRequest {
		t.Errorf("status = %d, want %d", fe.Code, fiber.StatusBadRequest)
	}
	// Message, not a fresh join of Fields: the stable order lives in Message,
	// and re-deriving it here would reintroduce the per-request shuffle.
	if fe.Message != "vmid: property is not defined in schema" {
		t.Errorf("message = %q, want the flattened field list", fe.Message)
	}
}

// Only a rejection map on a 400 is the operator's mistake. Everything else
// stays a gateway error — including a 5xx that happens to carry a map, which a
// reverse proxy in front of Proxmox can produce and which must keep being
// retried rather than being blamed on the caller.
func TestMapProxmoxError_OnlyA400RejectionIsAClientError(t *testing.T) {
	tests := []struct {
		name     string
		err      *proxmox.APIError
		wantCode int
		wantMsg  string
	}{
		{
			name:     "400 with no rejection map",
			err:      &proxmox.APIError{StatusCode: 400, Message: `{"message":"nope\n","data":null}`},
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "nope",
		},
		{
			name: "503 carrying a rejection map is still the gateway's failure",
			err: &proxmox.APIError{
				StatusCode: 503,
				Message:    "detail: no healthy upstream",
				Fields:     map[string]string{"detail": "no healthy upstream"},
			},
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "detail: no healthy upstream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(mapProxmoxError(tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", fe.Code, tt.wantCode)
			}
			if fe.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMsg)
			}
		})
	}
}

// Branch-ordering guard: a sentinel that wraps an APIError must keep the
// sentinel's status. The shape is synthetic — api_client.go wraps with
// "%w: %s", so an APIError never actually lands inside ErrConnectionFailed —
// but the ordering it pins is what stops a future edit reversing the two.
func TestMapProxmoxError_WrappedAPIErrorKeepsSentinelPrecedence(t *testing.T) {
	inner := &proxmox.APIError{StatusCode: 500, Message: `{"message":"nope\n"}`}
	wrapped := fmt.Errorf("%w: %w", proxmox.ErrConnectionFailed, inner)

	var fe *fiber.Error
	if !errors.As(mapProxmoxError(wrapped), &fe) {
		t.Fatal("want *fiber.Error")
	}
	if fe.Code != fiber.StatusBadGateway {
		t.Errorf("status = %d, want %d", fe.Code, fiber.StatusBadGateway)
	}
	if fe.Message != "Failed to connect to Proxmox" {
		t.Errorf("message = %q, want the connection-failed message", fe.Message)
	}
}

// The conflict sentence is supplied by each caller; these tests use the pool
// wording throughout.
const poolConflict = "A pool with that ID already exists"

// PVE reports a duplicate as a plain 500 with a die() string, so it carries no
// rejection map and mapProxmoxError alone can only call it a gateway failure.
// It is the operator's chosen name that is at fault.

func TestMapDuplicateNameError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
	}{
		{
			name:     "duplicate pool is a conflict",
			err:      &proxmox.APIError{StatusCode: 500, Message: "pool 'web-tier' already exists"},
			wantCode: fiber.StatusConflict,
			wantMsg:  "A pool with that ID already exists",
		},
		{
			// client_admin.go wraps every create with the pool id, so the bare
			// error above is not the shape production hands this function.
			name: "the conflict is still recognised through the client's wrap",
			err: fmt.Errorf("create resource pool %s: %w", "web-tier",
				&proxmox.APIError{StatusCode: 500, Message: "pool 'web-tier' already exists"}),
			wantCode: fiber.StatusConflict,
			wantMsg:  "A pool with that ID already exists",
		},
		{
			name:     "a rejected parameter still reaches the shared mapping",
			err:      &proxmox.APIError{StatusCode: 400, Message: "poolid: invalid format", Fields: map[string]string{"poolid": "invalid format"}},
			wantCode: fiber.StatusBadRequest,
		},
		{
			name:     "an unreachable cluster is still a gateway failure",
			err:      proxmox.ErrConnectionFailed,
			wantCode: fiber.StatusBadGateway,
		},
		{
			name:     "a missing pool is still a 404",
			err:      proxmox.ErrNotFound,
			wantCode: fiber.StatusNotFound,
		},
		{
			// PVE's HA rules say "already defined", which IsAlreadyExistsError
			// does not match — which is why CreateHARule is deliberately not
			// wired to this helper. Pinned so that loosening the predicate
			// cannot quietly turn HA-rule failures into a wrong 409.
			name:     "already defined is not already exists",
			err:      &proxmox.APIError{StatusCode: 500, Message: "HA rule 'r1' already defined"},
			wantCode: fiber.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(mapDuplicateNameError(poolConflict, tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", fe.Code, tt.wantCode)
			}
			if tt.wantMsg != "" && fe.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMsg)
			}
		})
	}

	if mapDuplicateNameError(poolConflict, nil) != nil {
		t.Error("mapDuplicateNameError(poolConflict, nil) should stay nil")
	}
}

// PVE's rule endpoints die with a plain "no rule at position N" — a bare 500
// with no rejection map — so mapProxmoxError alone can only call it a gateway
// failure. Two operators with the same rule list open produce this routinely.
func TestMapFirewallRuleError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
	}{
		{
			name:     "a stale position is not found, not a gateway failure",
			err:      &proxmox.APIError{StatusCode: 500, Message: "no rule at position 5"},
			wantCode: fiber.StatusNotFound,
			wantMsg:  "No firewall rule at that position — the list may be out of date",
		},
		{
			// The shape production really produces: parseProxmoxError returns
			// the raw body when there is no errors map, and client_firewall.go
			// then wraps it — so neither a bare sentence nor an unwrapped
			// error is what this function actually receives.
			name: "recognised through the envelope and the client's wrap",
			err: fmt.Errorf("delete cluster firewall rule %d: %w", 5,
				&proxmox.APIError{StatusCode: 500, Message: `{"data":null,"message":"no rule at position 5\n"}`}),
			wantCode: fiber.StatusNotFound,
		},
		{
			// A different plain-500 die must stay a gateway error. Note this
			// fixture does not itself contain "no rule", so it is not the guard
			// against that particular loosening — the cross-set test_RejectEachOthersNegatives
			// runs the "no rule with that name" fixture against this set, which
			// is the one that fails if the match is shortened.
			name:     "an unrelated die string is left alone",
			err:      &proxmox.APIError{StatusCode: 500, Message: "no such alias"},
			wantCode: fiber.StatusBadGateway,
		},
		{
			name:     "a rejected parameter still reaches the shared mapping",
			err:      &proxmox.APIError{StatusCode: 400, Message: "dport: invalid format", Fields: map[string]string{"dport": "invalid format"}},
			wantCode: fiber.StatusBadRequest,
		},
		{
			name:     "an unreachable cluster is still a gateway failure",
			err:      proxmox.ErrConnectionFailed,
			wantCode: fiber.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(mapFirewallRuleError(tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", fe.Code, tt.wantCode)
			}
			if tt.wantMsg != "" && fe.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMsg)
			}
		})
	}

	if mapFirewallRuleError(nil) != nil {
		t.Error("mapFirewallRuleError(nil) should stay nil")
	}
}

// These handlers have no onError, so the message is toasted verbatim. The
// connection branch of mapProxmoxError is static, so without this wrapper a
// failed apply produces a toast that never mentions the apply.
func TestMapNamedOpError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
	}{
		{
			name:     "an unreachable cluster still names the operation",
			err:      proxmox.ErrConnectionFailed,
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "apply network configuration: Failed to connect to Proxmox",
		},
		{
			name:     "a rejected parameter keeps both its status and its reason",
			err:      &proxmox.APIError{StatusCode: 400, Message: "iface: interface already exists", Fields: map[string]string{"iface": "interface already exists"}},
			wantCode: fiber.StatusBadRequest,
			wantMsg:  "apply network configuration: iface: interface already exists",
		},
		{
			name:     "a missing node keeps its 404",
			err:      proxmox.ErrNotFound,
			wantCode: fiber.StatusNotFound,
			wantMsg:  "apply network configuration: Resource not found on Proxmox",
		},
		{
			// The forbidden branch builds its message from err.Error(), so it
			// already carries the client's wrap. Note the client says "apply
			// network config on %s" while the handler passes "apply network
			// configuration": the two wordings live in different files and do
			// not agree, which is exactly why this decision is made on the
			// sentinel and not by looking for the operation in the text.
			name: "a message built from the wrap chain is not prefixed",
			err: fmt.Errorf("apply network config on %s: %w", "pve-01",
				proxmox.ErrForbidden),
			wantCode: fiber.StatusForbidden,
			wantMsg:  "Proxmox API: apply network config on pve-01: forbidden",
		},
		{
			// Same shape for the unknown-error tail.
			name:     "an unrecognised error keeps its own chain",
			err:      fmt.Errorf("shutdown node %s: %w", "pve-01", errors.New("boom")),
			wantCode: fiber.StatusInternalServerError,
			wantMsg:  "Proxmox operation failed: shutdown node pve-01: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(mapNamedOpError("apply network configuration", tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", fe.Code, tt.wantCode)
			}
			if fe.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMsg)
			}
		})
	}

	if mapNamedOpError("apply network configuration", nil) != nil {
		t.Error("mapNamedOpError(op, nil) should stay nil")
	}
}

// The die strings below were read out of PVE source, not inferred: HA rules
// from pve-ha-manager src/PVE/API2/HA/Rules.pm, metric servers from pve-manager
// PVE/API2/Cluster/MetricServer.pm, and the delete-path croak from pve-common
// src/PVE/SectionConfig.pm. Each arrives as a bare 500 with no rejection map,
// so mapProxmoxError alone can only call it a gateway failure.
func TestMapMissingObjectError(t *testing.T) {
	const haMissing = "No HA rule by that name — the list may be out of date"
	const msMissing = "No metric server with that ID — the list may be out of date"

	tests := []struct {
		name     string
		mapper   func(error) error
		err      error
		wantCode int
		wantMsg  string
	}{
		{
			// update_rule: `my $rule = $rules->{ids}->{$ruleid} || die ...`
			name:     "a stale HA rule name is not found, not a gateway failure",
			mapper:   mapHARuleError,
			err:      &proxmox.APIError{StatusCode: 500, Message: "HA rule 'r1' does not exist"},
			wantCode: fiber.StatusNotFound,
			wantMsg:  haMissing,
		},
		{
			// The shape production really produces: the envelope
			// parseProxmoxError falls back to, inside the client's wrap. PVE
			// prefixes it too — lock_ha_domain turns the die into "update HA
			// rules failed: HA rule 'r1' does not exist" — which is why the
			// match is a substring rather than a whole-message compare.
			name:   "recognised through the envelope and PVE's own lock prefix",
			mapper: mapHARuleError,
			err: fmt.Errorf("update ha rule %s: %w", "r1",
				&proxmox.APIError{StatusCode: 500, Message: `{"data":null,"message":"update HA rules failed: HA rule 'r1' does not exist\n"}`}),
			wantCode: fiber.StatusNotFound,
			wantMsg:  haMissing,
		},
		{
			// read_rule, via $get_api_ha_rule. GET /cluster/ha/rules/{rule}
			// exists, but the client has no method for it — ListRules lists and
			// DeleteRule filters — so this phrase is carried ahead of a caller.
			// Pinned so it cannot rot unnoticed the way "already defined" would
			// have on mapDuplicateNameError.
			name:     "the single-rule read wording is covered too",
			mapper:   mapHARuleError,
			err:      &proxmox.APIError{StatusCode: 500, Message: "no such ha rule 'r1'"},
			wantCode: fiber.StatusNotFound,
			wantMsg:  haMissing,
		},
		{
			// Rules.pm dies this when HA groups have not been migrated yet. It
			// is a real cluster-side state, not a stale list, and 404 would tell
			// the operator to go looking for a rule that is sitting right there.
			name:     "an unrelated HA die string is left alone",
			mapper:   mapHARuleError,
			err:      &proxmox.APIError{StatusCode: 500, Message: "cannot update ha rule: ha groups have not been migrated yet"},
			wantCode: fiber.StatusBadGateway,
		},
		{
			name:     "metric server read",
			mapper:   mapMetricServerError,
			err:      &proxmox.APIError{StatusCode: 500, Message: "status server entry 'influx1' does not exist"},
			wantCode: fiber.StatusNotFound,
			wantMsg:  msMissing,
		},
		{
			name:     "metric server update",
			mapper:   mapMetricServerError,
			err:      &proxmox.APIError{StatusCode: 500, Message: "no such server 'influx1'"},
			wantCode: fiber.StatusNotFound,
			wantMsg:  msMissing,
		},
		{
			// PVE's delete sub never checks; the absent entry's undef type
			// reaches SectionConfig::lookup, which croaks — so the croak, with
			// the file and line croak appends, is what a stale delete produces.
			name:     "metric server delete croaks instead of checking",
			mapper:   mapMetricServerError,
			err:      &proxmox.APIError{StatusCode: 500, Message: "cannot lookup undefined type! at /usr/share/perl5/PVE/API2/Cluster/MetricServer.pm line 295."},
			wantCode: fiber.StatusNotFound,
			wantMsg:  msMissing,
		},
		{
			// The false positive this predicate is shaped to avoid. PVE's
			// update sub dies BOTH "no such server '$id'" and "no such option
			// '$k'"; the second is the operator's own delete= parameter, and
			// answering 404 would send them hunting for a server that exists.
			// This is the case that fails if the match is loosened to "no such".
			name:     "no such option is a parameter mistake, not a missing server",
			mapper:   mapMetricServerError,
			err:      &proxmox.APIError{StatusCode: 500, Message: "no such option 'bogus'"},
			wantCode: fiber.StatusBadGateway,
		},
		{
			name:     "a rejected parameter still reaches the shared mapping",
			mapper:   mapMetricServerError,
			err:      &proxmox.APIError{StatusCode: 400, Message: "port: invalid format", Fields: map[string]string{"port": "invalid format"}},
			wantCode: fiber.StatusBadRequest,
		},
		{
			name:     "an unreachable cluster is still a gateway failure",
			mapper:   mapMetricServerError,
			err:      proxmox.ErrConnectionFailed,
			wantCode: fiber.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(tt.mapper(tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", fe.Code, tt.wantCode)
			}
			if tt.wantMsg != "" && fe.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMsg)
			}
		})
	}

	for name, mapper := range map[string]func(error) error{
		"mapHARuleError":       mapHARuleError,
		"mapMetricServerError": mapMetricServerError,
	} {
		if mapper(nil) != nil {
			t.Errorf("%s(nil) should stay nil", name)
		}
	}

	// The helper itself, not through a wrapper: an empty set must never match,
	// so a caller that forgets its phrases degrades to the old 502 rather than
	// answering 404 to everything.
	empty := mapMissingObjectError("unused", nil,
		&proxmox.APIError{StatusCode: 500, Message: "no such server 'influx1'"})
	var fe *fiber.Error
	if !errors.As(empty, &fe) || fe.Code != fiber.StatusBadGateway {
		t.Errorf("empty phrase set: got %v, want a 502", empty)
	}
	if mapMissingObjectError("unused", metricServerMissingPhrases, nil) != nil {
		t.Error("mapMissingObjectError(..., nil) should stay nil")
	}
}

// Every phrase set must reject every other set's negatives.
//
// Without this, each set is only ever tested against the negatives its own
// author thought of, and a later loosening passes: "no such option '<k>'" and
// "no such alias" were pinned only against the metric-server set, so shortening
// haRuleMissingPhrases to "no such" broke nothing. That is not hypothetical —
// PVE's update_rule reaches SectionConfig's delete_from_config, which dies
// "no such option '<k>'" on the very same PUT.
//
// Each fixture is a real or realistic PVE die that means something other than
// "the object you named is gone", so a 404 would be wrong for all of them
// whichever endpoint produced it.
func TestMissingObjectPhraseSets_RejectEachOthersNegatives(t *testing.T) {
	sets := map[string][]string{
		"haRuleMissingPhrases":       haRuleMissingPhrases,
		"metricServerMissingPhrases": metricServerMissingPhrases,
		"firewallRuleMissingPhrases": firewallRuleMissingPhrases,
		// Answers 409 rather than 404, but it reaches the same scan through
		// mapProxmoxDieError and so has the same two ways to be wrong.
		"staleDigestPhrases": staleDigestPhrases,
	}
	// ownedBy names the set a fixture is a legitimate positive for, so the
	// matrix can carry one set's die as every other set's negative without the
	// owner failing against itself. Empty means no set should ever match it.
	negatives := []struct{ msg, ownedBy string }{
		// PVE::Tools::assert_if_modified. A 404 or a widened phrase elsewhere
		// would turn "someone else edited this" into "it is gone".
		{"detected modified configuration - file changed by other user? try again.", "staleDigestPhrases"},
		// SectionConfig delete_from_config — the operator's own delete= param.
		{"no such option 'bogus'", ""},
		// A neighbouring firewall object; guards a shortening to "no such".
		{"no such alias", ""},
		// Guards a shortening of "no rule at position" to "no rule".
		{"no rule with that name", ""},
		// Rules.pm, a real cluster state rather than a stale list.
		{"cannot update ha rule: ha groups have not been migrated yet", ""},
		// Rules.pm's two param validators. The rule is right there; only the
		// node or the resource is wrong, so a 404 naming the rule would be
		// actively misleading. The first guards "does not exist" being widened
		// to "exist"; the second is pinned because it is the sibling check on
		// the same PUT and the likeliest place for PVE to reword into range.
		{"cannot use non-existent node(s) pve-01.", ""},
		{"cannot use unmanaged resource(s) vm:999.", ""},
		// SectionConfig lookup's *other* branch: a defined but unknown type is
		// a real config problem, not a missing id.
		{"unknown section type 'influxdb'", ""},
	}

	for name, phrases := range sets {
		for _, neg := range negatives {
			if neg.ownedBy == name {
				continue // its own positive, covered by that mapper's own test
			}
			t.Run(name+"/"+neg.msg, func(t *testing.T) {
				err := &proxmox.APIError{StatusCode: 500, Message: neg.msg}

				var fe *fiber.Error
				if !errors.As(mapMissingObjectError("should not be used", phrases, err), &fe) {
					t.Fatal("want *fiber.Error")
				}
				if fe.Code != fiber.StatusBadGateway {
					t.Errorf("status = %d, want %d — %s matched a die that is not a missing object",
						fe.Code, fiber.StatusBadGateway, name)
				}
			})
		}
	}
}

// Only Proxmox's own message is scanned, never the client's wrap around it.
//
// The wrap interpolates the id off the request path — client_admin.go's
// "get metric server %s", client_ha.go's "update ha rule %s" — so scanning the
// whole chain would let the caller's own input decide the status code. PVE
// types these ids as pve-configid and would reject this one, so the fixture is
// synthetic; the point is that the match should not depend on that.
func TestMapMissingObjectError_IgnoresTheClientsWrap(t *testing.T) {
	err := fmt.Errorf("get metric server %s: %w", "no such server",
		&proxmox.APIError{StatusCode: 500, Message: "unable to read status.cfg"})

	var fe *fiber.Error
	if !errors.As(mapMetricServerError(err), &fe) {
		t.Fatal("want *fiber.Error")
	}
	if fe.Code != fiber.StatusBadGateway {
		t.Errorf("status = %d, want %d — the phrase came from the caller's id, not from Proxmox",
			fe.Code, fiber.StatusBadGateway)
	}
}

// A phrase only ever meets a lowercased message, so mapMissingObjectError's
// strings.ToLower is what makes PVE's own capitalisation match. Nothing else
// pins it: every other fixture is already lowercase where it matters, so
// deleting that call would leave the suite green.
func TestMapMissingObjectError_MatchIsCaseInsensitive(t *testing.T) {
	err := &proxmox.APIError{StatusCode: 500, Message: "Status Server Entry 'influx1' Does Not Exist"}

	var fe *fiber.Error
	if !errors.As(mapMetricServerError(err), &fe) {
		t.Fatal("want *fiber.Error")
	}
	if fe.Code != fiber.StatusNotFound {
		t.Errorf("status = %d, want %d", fe.Code, fiber.StatusNotFound)
	}
}

// The phrase scan runs only on the 502-from-APIError branch, so a status
// mapProxmoxError already got right cannot be overwritten by a phrase that
// happens to appear in the text.
//
// This is not theoretical for the forbidden case: ErrForbidden's message is the
// raw response body, and PVE has 403s worded "pool 'x' does not exist"
// (pve-access-control RPCEnvironment.pm). Answering 404 "the list may be out of
// date" would send the operator looking for a missing object when what they
// actually lack is a privilege.
func TestMapMissingObjectError_DoesNotOutrankABetterStatus(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
	}{
		{
			name:     "a forbidden body carrying the phrase keeps its 403",
			err:      fmt.Errorf("%w: pool 'web-tier' does not exist", proxmox.ErrForbidden),
			wantCode: fiber.StatusForbidden,
			wantMsg:  "Proxmox API: forbidden: pool 'web-tier' does not exist",
		},
		{
			// The 400 also carries Fields, which the 404 would discard along
			// with the status — the operator would lose the field list too.
			name: "a rejected parameter carrying the phrase keeps its 400",
			err: &proxmox.APIError{
				StatusCode: 400,
				Message:    "id: status server entry 'x' does not exist",
				Fields:     map[string]string{"id": "status server entry 'x' does not exist"},
			},
			wantCode: fiber.StatusBadRequest,
			wantMsg:  "id: status server entry 'x' does not exist",
		},
		{
			name:     "input the client refused to send keeps its 400",
			err:      fmt.Errorf("%w: server id %q does not exist", proxmox.ErrInvalidInput, "a/b"),
			wantCode: fiber.StatusBadRequest,
		},
		{
			// Both are 502, so this one is about the message, not the status:
			// an unreachable cluster must not be relabelled as a stale list.
			name:     "an unreachable cluster keeps its own message",
			err:      fmt.Errorf("%w: no such server", proxmox.ErrConnectionFailed),
			wantCode: fiber.StatusBadGateway,
			wantMsg:  "Failed to connect to Proxmox",
		},
		{
			name:     "an unrecognised error keeps its 500",
			err:      fmt.Errorf("marshal params: %w", errors.New("no such server")),
			wantCode: fiber.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fe *fiber.Error
			if !errors.As(mapMetricServerError(tt.err), &fe) {
				t.Fatal("want *fiber.Error")
			}
			if fe.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", fe.Code, tt.wantCode)
			}
			if tt.wantMsg != "" && fe.Message != tt.wantMsg {
				t.Errorf("message = %q, want %q", fe.Message, tt.wantMsg)
			}
		})
	}
}

// The phrases are matched case-insensitively against a lowercased message, so a
// phrase written with a capital could never match anything. PVE's HA wording
// ("HA rule '<id>' does not exist") is exactly the kind that invites one.
func TestMissingObjectPhrasesAreLowercase(t *testing.T) {
	sets := map[string][]string{
		"haRuleMissingPhrases":       haRuleMissingPhrases,
		"metricServerMissingPhrases": metricServerMissingPhrases,
		"firewallRuleMissingPhrases": firewallRuleMissingPhrases,
		// Answers 409 rather than 404, but it reaches the same scan through
		// mapProxmoxDieError and so has the same two ways to be wrong.
		"staleDigestPhrases": staleDigestPhrases,
	}
	for name, phrases := range sets {
		if len(phrases) == 0 {
			t.Errorf("%s is empty — every caller of it is dead code", name)
		}
		for _, phrase := range phrases {
			if phrase != strings.ToLower(phrase) {
				t.Errorf("%s: %q has uppercase and can never match", name, phrase)
			}
		}
	}
}
