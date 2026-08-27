package handlers

import (
	"testing"

	"github.com/google/uuid"
)

// Unattributable rows must be visible to global holders only. Getting this
// backwards in the permissive direction shows one tenant's backup inventory to
// another; getting it backwards in the strict direction hides everything from
// everyone until an operator maps a cluster, which is the safe failure.
func TestVeeamScope_PermitsPlatform(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	platformA := uuid.New()
	platformUnmapped := uuid.New()

	mapping := map[uuid.UUID]uuid.UUID{platformA: clusterA}

	tests := []struct {
		name     string
		access   clusterAccess
		platform uuid.UUID
		valid    bool
		want     bool
	}{
		{
			name:     "global sees a mapped platform",
			access:   clusterAccess{HasGlobal: true},
			platform: platformA, valid: true, want: true,
		},
		{
			name:     "global sees an unmapped platform",
			access:   clusterAccess{HasGlobal: true},
			platform: platformUnmapped, valid: true, want: true,
		},
		{
			// A job that has never run carries no platform and could belong
			// to any cluster.
			name:     "global sees a row with no platform at all",
			access:   clusterAccess{HasGlobal: true},
			platform: uuid.Nil, valid: false, want: true,
		},
		{
			name:     "cluster-scoped sees its own cluster's platform",
			access:   clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: true}},
			platform: platformA, valid: true, want: true,
		},
		{
			name:     "cluster-scoped does not see another cluster's platform",
			access:   clusterAccess{Allowed: map[uuid.UUID]bool{clusterB: true}},
			platform: platformA, valid: true, want: false,
		},
		{
			// The Phase 2 state: no operator has confirmed a mapping yet, so
			// nothing is attributable and a scoped caller sees none of it.
			name:     "cluster-scoped does not see an unmapped platform",
			access:   clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: true}},
			platform: platformUnmapped, valid: true, want: false,
		},
		{
			name:     "cluster-scoped does not see a row with no platform",
			access:   clusterAccess{Allowed: map[uuid.UUID]bool{clusterA: true}},
			platform: uuid.Nil, valid: false, want: false,
		},
		{
			name:     "no access at all sees nothing",
			access:   clusterAccess{},
			platform: platformA, valid: true, want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope := veeamScope{access: tc.access, platformCluster: mapping}
			if got := scope.permitsPlatform(tc.platform, tc.valid); got != tc.want {
				t.Errorf("permitsPlatform = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseVeeamRange(t *testing.T) {
	hours := func(d string) float64 {
		return parseVeeamRange(d).Hours()
	}
	if hours("24h") != 24 {
		t.Errorf("24h = %v", hours("24h"))
	}
	if hours("30d") != 720 {
		t.Errorf("30d = %v", hours("30d"))
	}
	// Anything unrecognised falls back to the default rather than erroring:
	// a chart with a sensible window beats a 400.
	for _, bad := range []string{"", "banana", "7", "-1d"} {
		if hours(bad) != 168 {
			t.Errorf("parseVeeamRange(%q) = %v hours, want the 7d default", bad, hours(bad))
		}
	}
}
