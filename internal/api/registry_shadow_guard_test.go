package api

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// router.go mounts the registry before every legacy block, and says why:
// "a migrated literal path would otherwise be shadowed by whichever legacy
// :param route still matches it." Fiber matches routes in registration
// order and stops at the first match, so that ordering is correct for a
// migrated LITERAL — but it opens two converse holes for a migrated
// :PARAM, both owned by this file:
//
//   - Shadowing: a registry route like GET /api/v1/vms/:id, mounted first,
//     also matches GET /api/v1/vms/summary, so the legacy literal route —
//     and its hand-placed require*Perm call — becomes dead code Fiber
//     never reaches.
//   - Exact duplication: a registry route registered under the identical
//     path a legacy block still claims makes the legacy registration
//     unconditionally dead, full stop — the single most likely Phase 4
//     mistake (migrate a handler, forget to delete its router.go entry).
//     TestPackageRegistryIsStillEmpty catches this TODAY by comparing
//     "METHOD path" keys for an exact match, but that test's own top
//     check (`endpoints.Len() != 0`) makes it fail — and therefore need
//     deleting or rewriting — the moment Phase 4 registers anything at
//     all. This file does not inherit that expiry, so it owns exact
//     duplication itself rather than deferring to a test that will not
//     be there to defer to.
//
// registryLegacyRouteConflicts below reports both.

// legacyRouteKeys returns the normalized "METHOD path" key of every route
// registration NOT accounted for by the registry — see budgetedLegacyKeys
// for the "skip at most one occurrence per registry key" rule this
// applies, and why plain set-membership filtering would hide a leftover
// legacy duplicate.
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

// budgetedLegacyKeys is the pure comparison legacyRouteKeys drives against
// a live route table's keys (allKeys, one entry per route-table row, so a
// key can repeat): every key that survives after skipping AT MOST one
// occurrence per key in registryKeys. Factored out so a synthetic case can
// prove the "budget of one" behavior — set membership alone would drop
// EVERY occurrence of a key once it is anywhere in the registry, hiding a
// leftover legacy duplicate under the same path (see legacyRouteKeys' doc
// comment); this is what makes that not happen.
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

// isOptionalSegment reports whether seg is Fiber v3's optional-parameter
// syntax (":name?"). apischema's extraction layer deliberately supports
// this — see TestRegistryOmitsUnseenKeysRatherThanPassingEmptyStrings'
// "an optional path segment that was not supplied" case in
// registry_request_test.go, and readSource's SourcePath comment in
// registry_params.go — so Register does not, and should not, refuse it;
// this guard has to understand it instead.
func isOptionalSegment(seg string) bool {
	return strings.HasPrefix(seg, ":") && strings.HasSuffix(seg, "?")
}

// segmentCaptures reports whether a registry pattern segment matches
// anything a legacy segment in the same position matches. A :param
// segment (Fiber's syntax — optionally suffixed <type> or ?, or several
// params sharing one segment like :a-:b) matches any single path
// segment, so it captures a literal legacy segment AND another legacy
// :param in the same slot equally. A literal segment only captures its
// own exact text, compared case-insensitively to match Fiber's own
// CaseSensitive: false (see checkPathParams in registry.go).
func segmentCaptures(registrySeg, legacySeg string) bool {
	if strings.HasPrefix(registrySeg, ":") {
		return true
	}
	return strings.EqualFold(registrySeg, legacySeg)
}

// pathIsCapturedBy reports whether pattern, registered ahead of routePath,
// would match every request routePath matches — i.e. whether pattern
// shadows (same width) or duplicates (identical path) routePath.
//
// Register already refuses a registry path containing a greedy wildcard
// (checkPathParams' "*"/"+" check) — but ONLY on the registry side: that
// validation never runs on a legacy path, and router.go has one today
// (DELETE .../content/*). A wildcard on routePath is treated as an
// ordinary literal segment here, which is exact for the one width it can
// represent but cannot express Fiber's true "matches any number of
// trailing segments" semantics; no registry pattern can shadow a WIDER
// match than its own fixed segment count anyway, so this under-models
// only the wildcard's own reach, never the registry side's.
//
// A trailing run of :name? segments lets ONE registered pattern match a
// RANGE of widths, not just its own — Fiber consumes optional segments
// left to right, so a request with w segments (minWidth <= w <=
// len(pSegs)) matches against the pattern's first w segments. A
// non-trailing "?" has no such effect (Fiber still requires everything
// after it), so only a trailing run counts.
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

// registryLegacyRouteConflicts reports, for each endpoint in eps, every
// legacy route it either shadows or exactly duplicates — see the file's
// top comment for why this owns both rather than deferring the duplicate
// case elsewhere. legacyKeys is "METHOD path" for every route NOT
// declared in eps; passing it in — rather than deriving it from a live
// app inside this function — is what lets a test hand in a synthetic pair
// and prove the check fires without booting a Fiber app at all.
func registryLegacyRouteConflicts(eps []Endpoint, legacyKeys []string) []string {
	var findings []string
	for _, e := range eps {
		// normalizeRoutePath so a trailing slash cannot desync the
		// segment count from legacyKeys, which is already normalized
		// (legacyRouteKeys uses it too) — StrictRouting is unset
		// (buildFiberConfig, server.go), so Fiber treats "/x/:id/" and
		// "/x/:id" as one route and this comparison has to as well.
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

// TestBudgetedLegacyKeys_LeftoverDuplicateSurvivesTheBudget proves the
// bug a plain set-membership filter has: when a key appears TWICE in the
// live route table (once from the registry, once from a legacy block that
// was never deleted after migration), a naive "drop every row whose key
// is in the registry" filter drops BOTH occurrences — so
// registryLegacyRouteConflicts would never even see the leftover legacy
// row to report it as a duplicate. budgetedLegacyKeys must let the SECOND
// occurrence through.
func TestBudgetedLegacyKeys_LeftoverDuplicateSurvivesTheBudget(t *testing.T) {
	allKeys := []string{
		"GET /api/v1/rbac/permissions", // the registry's own row
		"GET /api/v1/rbac/permissions", // the leftover legacy row — same key
		"GET /api/v1/other",
	}
	registryKeys := map[string]bool{"GET /api/v1/rbac/permissions": true}

	got := budgetedLegacyKeys(allKeys, registryKeys)
	want := []string{"GET /api/v1/other", "GET /api/v1/rbac/permissions"}
	if len(got) != len(want) {
		t.Fatalf("budgetedLegacyKeys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("budgetedLegacyKeys[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestBudgetedLegacyKeys_MigratedRouteLeavesTheSetCleanly is the ordinary
// case: a key registered ONLY by the registry (properly migrated, no
// leftover legacy registration) must not appear in the result at all.
func TestBudgetedLegacyKeys_MigratedRouteLeavesTheSetCleanly(t *testing.T) {
	allKeys := []string{"GET /api/v1/rbac/permissions", "GET /api/v1/other"}
	registryKeys := map[string]bool{"GET /api/v1/rbac/permissions": true}

	got := budgetedLegacyKeys(allKeys, registryKeys)
	if len(got) != 1 || got[0] != "GET /api/v1/other" {
		t.Errorf("budgetedLegacyKeys = %v, want just [\"GET /api/v1/other\"]", got)
	}
}

// TestGuard_RegistryDoesNotConflictWithLegacyRoutes is the production
// guard: every declared registry endpoint against every route a legacy
// block in router.go still registers.
func TestGuard_RegistryDoesNotConflictWithLegacyRoutes(t *testing.T) {
	s := newRouteStubServer(t)
	legacy := legacyRouteKeys(t, s)
	for _, msg := range registryLegacyRouteConflicts(s.registry.Endpoints(), legacy) {
		t.Error(msg)
	}
}

// TestRegistryLegacyRouteConflicts_CatchesAShadowedLiteral proves the
// shadow half bites: a registry :param route mounted ahead of a
// same-shaped legacy literal is reported, and an unrelated legacy route is
// not.
func TestRegistryLegacyRouteConflicts_CatchesAShadowedLiteral(t *testing.T) {
	reg := NewRegistry()
	reg.Register(registryProbeEndpoint("/api/v1/vms/:id",
		Permissions{Public: "synthetic route for the shadow guard test"}, noopParamsHandler,
		apischema.Properties{"id": {Type: apischema.String}}))

	got := registryLegacyRouteConflicts(reg.Endpoints(), []string{
		"GET /api/v1/vms/summary",        // same method, same shape: shadowed
		"GET /api/v1/containers/summary", // different literal segment: not captured
		"POST /api/v1/vms/summary",       // different method: not captured
	})

	if len(got) != 1 {
		t.Fatalf("findings = %v, want exactly 1", got)
	}
	if !strings.Contains(got[0], "shadows") || !strings.Contains(got[0], "GET /api/v1/vms/summary") || !strings.Contains(got[0], "/api/v1/vms/:id") {
		t.Errorf("finding = %q, want a shadow finding naming both the legacy route and the registry pattern", got[0])
	}
}

// TestRegistryLegacyRouteConflicts_CatchesAnExactDuplicate proves the
// duplicate half bites: a registry route registered under the IDENTICAL
// path a legacy block still claims is reported as a duplicate, worded
// differently from a shadow finding, and is not silently deferred to a
// test that will not exist once Phase 4 starts (see the file's top
// comment).
func TestRegistryLegacyRouteConflicts_CatchesAnExactDuplicate(t *testing.T) {
	reg := NewRegistry()
	reg.Register(registryProbeEndpointNoParams("/api/v1/vms/summary",
		Permissions{Public: "synthetic route for the shadow guard test"}, noopParamsHandler))

	got := registryLegacyRouteConflicts(reg.Endpoints(), []string{"GET /api/v1/vms/summary"})
	if len(got) != 1 {
		t.Fatalf("findings = %v, want exactly 1", got)
	}
	if !strings.Contains(got[0], "exactly duplicates") {
		t.Errorf("finding = %q, want it worded as a duplicate, not a shadow", got[0])
	}
}

// TestRegistryLegacyRouteConflicts_DifferentSegmentCountNeverConflicts
// proves a registry pattern with a different (non-optional) number of
// path segments cannot capture a legacy route, even when one is a prefix
// of the other.
func TestRegistryLegacyRouteConflicts_DifferentSegmentCountNeverConflicts(t *testing.T) {
	reg := NewRegistry()
	reg.Register(registryProbeEndpoint("/api/v1/vms/:id",
		Permissions{Public: "synthetic route for the shadow guard test"}, noopParamsHandler,
		apischema.Properties{"id": {Type: apischema.String}}))

	got := registryLegacyRouteConflicts(reg.Endpoints(), []string{"GET /api/v1/vms/:id/config"})
	if len(got) != 0 {
		t.Errorf("findings = %v, want none — different segment counts cannot match in Fiber's router", got)
	}
}

// TestRegistryLegacyRouteConflicts_OptionalTrailingSegmentWidensTheMatch
// proves the review-flagged gap is closed: a registry route ending in
// :name? matches BOTH the full width and one segment short (Fiber v3's
// optional-parameter syntax, deliberately supported by the extraction
// layer — see isOptionalSegment's doc comment), so it must be checked
// against legacy routes at both widths, not just its own declared one.
func TestRegistryLegacyRouteConflicts_OptionalTrailingSegmentWidensTheMatch(t *testing.T) {
	reg := NewRegistry()
	reg.Register(registryProbeEndpoint("/api/v1/vms/:id?",
		Permissions{Public: "synthetic route for the shadow guard test"}, noopParamsHandler,
		apischema.Properties{"id": {Type: apischema.String, Optional: true}}))

	got := registryLegacyRouteConflicts(reg.Endpoints(), []string{
		"GET /api/v1/vms",         // one segment short of the declared path: still captured
		"GET /api/v1/vms/summary", // full width: still captured
		"GET /api/v1/vms/a/b",     // too wide even for the optional segment: not captured
	})

	if len(got) != 2 {
		t.Fatalf("findings = %v, want exactly 2 (the short match and the full-width match)", got)
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "GET /api/v1/vms ") && !strings.Contains(joined, "GET /api/v1/vms\n") && !strings.Contains(joined, "legacy route GET /api/v1/vms —") {
		t.Errorf("findings = %v, want one of them to name the short match GET /api/v1/vms", got)
	}
	if !strings.Contains(joined, "GET /api/v1/vms/summary") {
		t.Errorf("findings = %v, want one of them to name the full-width match GET /api/v1/vms/summary", got)
	}
}

// TestRegistryLegacyRouteConflicts_TrailingSlashDoesNotDesyncTheWidth
// proves normalizeRoutePath keeps a registry path's trailing slash from
// inflating its segment count relative to legacyKeys, which is already
// normalized — StrictRouting is unset, so Fiber treats the two spellings
// as one route and this guard has to as well (see
// registryLegacyRouteConflicts' doc comment).
func TestRegistryLegacyRouteConflicts_TrailingSlashDoesNotDesyncTheWidth(t *testing.T) {
	reg := NewRegistry()
	reg.Register(registryProbeEndpoint("/api/v1/vms/:id/",
		Permissions{Public: "synthetic route for the shadow guard test"}, noopParamsHandler,
		apischema.Properties{"id": {Type: apischema.String}}))

	got := registryLegacyRouteConflicts(reg.Endpoints(), []string{"GET /api/v1/vms/summary"})
	if len(got) != 1 {
		t.Fatalf("findings = %v, want exactly 1 — a trailing slash on the registry path must not hide the shadow", got)
	}
}
