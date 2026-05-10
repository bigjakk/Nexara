package notifications

import (
	"strings"
	"testing"
)

func TestPagerDutyDedupKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload AlertPayload
		want    string
	}{
		{
			name: "rule + cluster + resource composes the key",
			payload: AlertPayload{
				RuleID:       "rule-A",
				ClusterID:    "cluster-1",
				ResourceName: "vm-100",
			},
			want: "nexara-rule-A-cluster-1-vm-100",
		},
		{
			name: "global rule (empty cluster) uses 'global' placeholder",
			payload: AlertPayload{
				RuleID:       "rule-A",
				ClusterID:    "",
				ResourceName: "node-1",
			},
			want: "nexara-rule-A-global-node-1",
		},
		{
			name: "uuid-shaped fields are passed through verbatim",
			payload: AlertPayload{
				RuleID:       "11111111-1111-1111-1111-111111111111",
				ClusterID:    "22222222-2222-2222-2222-222222222222",
				ResourceName: "pve3",
			},
			want: "nexara-11111111-1111-1111-1111-111111111111-22222222-2222-2222-2222-222222222222-pve3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pagerdutyDedupKey(tc.payload)
			if got != tc.want {
				t.Errorf("pagerdutyDedupKey: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPagerDutyDedupKey_DistinctResources locks down the bug fix: two alerts
// from the same rule on different resources must produce different keys.
// If this test fails, the previous "nexara-<rule_id>" regression is back and
// the second resource's incident will be suppressed by PagerDuty.
func TestPagerDutyDedupKey_DistinctResources(t *testing.T) {
	t.Parallel()

	a := AlertPayload{
		RuleID:       "shared-rule",
		ClusterID:    "shared-cluster",
		ResourceName: "vm-100",
	}
	b := AlertPayload{
		RuleID:       "shared-rule",
		ClusterID:    "shared-cluster",
		ResourceName: "vm-200",
	}

	keyA := pagerdutyDedupKey(a)
	keyB := pagerdutyDedupKey(b)

	if keyA == keyB {
		t.Fatalf("dedup keys must differ for distinct resources from the same rule, both got %q", keyA)
	}
	if !strings.Contains(keyA, "vm-100") || !strings.Contains(keyB, "vm-200") {
		t.Errorf("expected resource names embedded in keys; got %q and %q", keyA, keyB)
	}
}

// TestPagerDutyDedupKey_StableAcrossStates locks down the resolve-matching
// contract: a trigger event and its later resolve event must share the same
// key, otherwise PagerDuty creates a new incident for the resolve instead of
// closing the open one. The State field is intentionally NOT part of the key.
func TestPagerDutyDedupKey_StableAcrossStates(t *testing.T) {
	t.Parallel()

	trigger := AlertPayload{
		RuleID:       "rule-A",
		ClusterID:    "cluster-1",
		ResourceName: "node-1",
		State:        "firing",
		CurrentValue: 95.0,
	}
	resolve := AlertPayload{
		RuleID:       "rule-A",
		ClusterID:    "cluster-1",
		ResourceName: "node-1",
		State:        "resolved",
		CurrentValue: 42.0,
	}

	if pagerdutyDedupKey(trigger) != pagerdutyDedupKey(resolve) {
		t.Errorf("dedup key must be stable across firing → resolved transitions; got %q vs %q",
			pagerdutyDedupKey(trigger), pagerdutyDedupKey(resolve))
	}
}

// TestPagerDutyDedupKey_DistinctClusters keeps cluster scope in the key so a
// global rule that fires on a same-named resource in two clusters (e.g.
// "node-1" exists on both) produces two separate PagerDuty incidents.
func TestPagerDutyDedupKey_DistinctClusters(t *testing.T) {
	t.Parallel()

	a := AlertPayload{
		RuleID:       "rule-A",
		ClusterID:    "cluster-east",
		ResourceName: "node-1",
	}
	b := AlertPayload{
		RuleID:       "rule-A",
		ClusterID:    "cluster-west",
		ResourceName: "node-1",
	}

	if pagerdutyDedupKey(a) == pagerdutyDedupKey(b) {
		t.Errorf("dedup key must differ across clusters for same resource name; both got %q", pagerdutyDedupKey(a))
	}
}
