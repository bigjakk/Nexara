package api

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

// router.go mounts the registry before every legacy block, which is right for a migrated
// LITERAL but opens two holes for a migrated :PARAM: it shadows a legacy literal that
// matches it (GET /vms/:id over GET /vms/summary), so the legacy handler and its
// hand-placed permission check become dead code; and an exact duplicate (migrated, its
// router.go entry not deleted) makes the legacy registration dead outright.
// registryLegacyRouteConflicts reports both.

// legacyRouteKeys returns the normalized "METHOD path" key of every route registration
// NOT accounted for by the registry; budgetedLegacyKeys says why a set filter would hide
// a leftover legacy duplicate.
func legacyRouteKeys(t *testing.T, s *Server) []string {
	t.Helper()

	var allKeys []string
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		allKeys = append(allKeys, r.Method+" "+normalizeRoutePath(r.Path))
	}
	return budgetedLegacyKeys(allKeys, registryRouteKeySet(s.registry.Endpoints()))
}

// budgetedLegacyKeys is the pure comparison legacyRouteKeys drives: every key of allKeys
// (one per route-table row, so a key can repeat) that survives skipping AT MOST one
// occurrence per key in registryKeys. A set filter would drop every occurrence of a key
// once it was in the registry, hiding the legacy row left behind after a migration.
func budgetedLegacyKeys(allKeys []string, registryKeys map[string]bool) []string {
	budget := make(map[string]bool, len(registryKeys))
	for key := range registryKeys {
		budget[key] = true
	}

	var out []string
	for _, key := range allKeys {
		if budget[key] {
			delete(budget, key)
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// pathSegments splits a route path into its slash-separated segments,
// dropping the leading empty element every absolute path produces.
func pathSegments(path string) []string {
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}

// isOptionalSegment reports whether seg is Fiber v3's optional-parameter syntax
// (":name?"). Register refuses it, so no registered route carries one; this guard models
// it anyway, because that refusal is another file's decision and one edit from being
// relaxed. pathIsCapturedBy leans on the refusal for "*" and "+" instead (see its comment).
func isOptionalSegment(seg string) bool {
	return strings.HasPrefix(seg, ":") && strings.HasSuffix(seg, "?")
}

// segmentCaptures reports whether a registry pattern segment matches anything a legacy
// segment in the same position matches: a :param segment matches any single segment, a
// literal only its own text, case-insensitively as Fiber's CaseSensitive: false does.
func segmentCaptures(registrySeg, legacySeg string) bool {
	if strings.HasPrefix(registrySeg, ":") {
		return true
	}
	return strings.EqualFold(registrySeg, legacySeg)
}

// pathIsCapturedBy reports whether pattern, registered ahead of routePath, would match
// every request routePath matches, shadowing it (same width) or duplicating it.
// Register refuses "*"/"+" in a registry path but never runs on a legacy one (router.go
// has DELETE .../content/*): a wildcard on routePath is read as a literal, which is exact
// for its one width and under-models only its own reach. A trailing run of :name?
// segments lets one pattern match a RANGE of widths, from the first non-optional segment
// to its own length; a non-trailing "?" has no such effect.
func pathIsCapturedBy(pattern, routePath string) bool {
	pSegs := pathSegments(pattern)
	rSegs := pathSegments(routePath)

	trailingOptional := 0
	for i := len(pSegs) - 1; i >= 0 && isOptionalSegment(pSegs[i]); i-- {
		trailingOptional++
	}
	minWidth := len(pSegs) - trailingOptional
	if len(rSegs) < minWidth || len(rSegs) > len(pSegs) {
		return false
	}
	for i := range rSegs {
		if !segmentCaptures(pSegs[i], rSegs[i]) {
			return false
		}
	}
	return true
}

// registryLegacyRouteConflicts reports, for each endpoint in eps, every legacy route it
// shadows or exactly duplicates. legacyKeys is "METHOD path" for every route NOT declared
// in eps, passed in so a synthetic pair can prove the check fires without a Fiber app.
func registryLegacyRouteConflicts(eps []Endpoint, legacyKeys []string) []string {
	var findings []string
	for _, e := range eps {
		// StrictRouting is unset, so "/x/:id/" and "/x/:id" are one route; legacyKeys is normalized too.
		regPath := normalizeRoutePath(e.Path)
		for _, legacyKey := range legacyKeys {
			method, legacyPath, ok := strings.Cut(legacyKey, " ")
			if !ok || !strings.EqualFold(method, e.Method) {
				continue
			}
			if !pathIsCapturedBy(regPath, legacyPath) {
				continue
			}
			if strings.EqualFold(legacyPath, regPath) {
				findings = append(findings, fmt.Sprintf(
					"registry route %s %s exactly duplicates legacy route %s — Fiber mounts the "+
						"registry first (router.go), so the legacy handler for %s is dead code; "+
						"delete its registration in router.go now that the route is migrated",
					e.Method, regPath, legacyKey, legacyPath))
				continue
			}
			findings = append(findings, fmt.Sprintf(
				"registry route %s %s (mounted first) shadows legacy route %s — "+
					"the legacy handler for %s is now unreachable; migrate it into the "+
					"registry too, or give the registry route a literal path that does not "+
					"capture it",
				e.Method, regPath, legacyKey, legacyPath))
		}
	}
	sort.Strings(findings)
	return findings
}

// TestBudgetedLegacyKeys_LeftoverDuplicateSurvivesTheBudget: a key that appears twice in
// the route table, once from the registry and once from a legacy block never deleted after
// migration, keeps its second occurrence; one registered only by the registry vanishes.
func TestBudgetedLegacyKeys_LeftoverDuplicateSurvivesTheBudget(t *testing.T) {
	const key = "GET /api/v1/rbac/permissions"
	registryKeys := map[string]bool{key: true}
	for _, tt := range []struct {
		name string
		all  []string
		want []string
	}{
		{"a leftover legacy row", []string{key, key, "GET /api/v1/other"}, []string{"GET /api/v1/other", key}},
		{"a cleanly migrated route", []string{key, "GET /api/v1/other"}, []string{"GET /api/v1/other"}},
	} {
		if got := budgetedLegacyKeys(tt.all, registryKeys); !slices.Equal(got, tt.want) {
			t.Errorf("%s: budgetedLegacyKeys = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestGuard_RegistryDoesNotConflictWithLegacyRoutes is the production guard: every
// declared registry endpoint against every route a legacy block in router.go still registers.
func TestGuard_RegistryDoesNotConflictWithLegacyRoutes(t *testing.T) {
	s := sharedRouteStub(t)
	legacy := legacyRouteKeys(t, s)
	for _, msg := range registryLegacyRouteConflicts(s.registry.Endpoints(), legacy) {
		t.Error(msg)
	}
}

// TestRegistryLegacyRouteConflicts_CatchesAShadowedLiteral drives the detector over
// synthetic pairs, since the real registry is clean: a :param route shadowing a legacy
// literal; an exact duplicate, worded differently; the shapes that must not collide (another
// method, literal or width); an optional trailing segment, which widens the match to one
// segment short (Register refuses such a path, so the probe is handed in directly); and a
// trailing slash on the registry path, which must not hide the shadow.
func TestRegistryLegacyRouteConflicts_CatchesAShadowedLiteral(t *testing.T) {
	for _, tt := range []struct {
		name   string
		path   string // the registry route, GET
		legacy []string
		want   []string // one substring per finding, in order
	}{
		{"a shadowed literal", "/api/v1/vms/:id", []string{
			"GET /api/v1/vms/summary", "GET /api/v1/containers/summary", "POST /api/v1/vms/summary", "GET /api/v1/vms/:id/config"},
			[]string{"shadows legacy route GET /api/v1/vms/summary"}},
		{"an exact duplicate", "/api/v1/vms/summary", []string{"GET /api/v1/vms/summary"},
			[]string{"exactly duplicates legacy route GET /api/v1/vms/summary"}},
		{"an optional trailing segment", "/api/v1/vms/:id?", []string{
			"GET /api/v1/vms", "GET /api/v1/vms/summary", "GET /api/v1/vms/a/b"},
			[]string{"legacy route GET /api/v1/vms —", "legacy route GET /api/v1/vms/summary —"}},
		{"a trailing slash on the registry path", "/api/v1/vms/:id/", []string{"GET /api/v1/vms/summary"},
			[]string{"shadows legacy route GET /api/v1/vms/summary"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			probe := Endpoint{Method: "GET", Path: tt.path}
			got := registryLegacyRouteConflicts([]Endpoint{probe}, tt.legacy)
			if len(got) != len(tt.want) {
				t.Fatalf("findings = %v, want %d", got, len(tt.want))
			}
			for i, want := range tt.want {
				if !strings.Contains(got[i], want) {
					t.Errorf("finding %d = %q, want it to carry %q", i, got[i], want)
				}
			}
		})
	}
}
