package api

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// docs/api-reference.md is curated, not generated (the live source of truth is the in-app
// catalog), and two things in it are checkable: a row naming a route is a claim about the
// router, and a permission named in the prose is a claim about the catalogue that only
// migrations seed (the shape of the bug that made the node firewall routes answer 403 to
// everyone for six months: a well-formed "view:firewall" no migration seeded).
// registry_permission_catalogue_test.go closes the declaration side; this file closes the
// prose side. The reverse direction of the route check, a row for every route, is
// deliberately not enforced: the file is an overview, the catalog is the dump.

// apiReferencePath is the curated reference, relative to this package.
func apiReferencePath() string {
	return filepath.Join("..", "..", "docs", "api-reference.md")
}

// docEndpointRow is one endpoint row parsed out of the markdown.
type docEndpointRow struct {
	Method string
	Path   string // exactly as the doc spells it, for the failure message
	Line   int
}

// docReferenceRowRe matches the dominant row shape, "| GET | `/path` |".
// The method is [A-Z]+ so the "| Method | Path | Description |" HEADER of
// every one of those tables does not parse as a row of itself.
var docReferenceRowRe = regexp.MustCompile("^\\|\\s*([A-Z]+)\\s*\\|\\s*`([^`]+)`\\s*\\|")

// docReferenceInlineRowRe matches the second shape, where the method and
// the path share one backticked cell: "| `POST /api/v1/auth/ws-token` |".
// The WebSocket section's token table is written that way.
//
// Requiring the pair to be the row's FIRST cell is what keeps the
// rate-limit table out: its rows name paths too ("| Cluster create |
// 10/min | `POST /clusters` — …"), but in the third cell, describing a
// limiter's scope rather than claiming a route exists. Those are prose
// about a budget, not an endpoint listing, and folding them in would mean
// parsing a sentence.
var docReferenceInlineRowRe = regexp.MustCompile("^\\|\\s*`([A-Z]+)\\s+(/[^`]+)`\\s*\\|")

// parseDocEndpointRows extracts the endpoint rows from one markdown document. Split out
// from the file read so the real reference plus a planted row can be run through it.
func parseDocEndpointRows(md string) []docEndpointRow {
	var out []docEndpointRow
	for i, line := range strings.Split(md, "\n") {
		if m := docReferenceRowRe.FindStringSubmatch(line); m != nil {
			out = append(out, docEndpointRow{Method: m[1], Path: m[2], Line: i + 1})
			continue
		}
		if m := docReferenceInlineRowRe.FindStringSubmatch(line); m != nil {
			out = append(out, docEndpointRow{Method: m[1], Path: m[2], Line: i + 1})
		}
	}
	return out
}

// routeShape reduces a path to what can honestly be compared between the doc and the
// router: segment count, which segments are parameters, and the literal text of the rest.
// Parameter NAMES are erased: the doc's shorthand (":id" for ":cluster_id", ":node" for
// ":node_name") is applied across 50-odd tables, and the registry itself is not uniform
// about ":node". The parameter/literal distinction is kept, and so is "*" as its own
// marker. Also normalised: the "/api/v1" prefix (the doc writes paths relative to the base
// URL), a trailing "?query=" illustration, and a trailing slash, which StrictRouting being
// unset makes one route (for writes the API now refuses the slashed spelling:
// docRowsSpellingARefusedSlash).
func routeShape(path string) string {
	path = strings.TrimPrefix(path, "/api/v1")
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, seg := range segs {
		switch {
		case strings.HasPrefix(seg, ":"):
			segs[i] = "{param}"
		case seg == "*":
			segs[i] = "{*}"
		case seg == "+":
			segs[i] = "{+}"
		default:
			// Fiber's CaseSensitive is false (see segmentCaptures).
			segs[i] = strings.ToLower(seg)
		}
	}
	return strings.Join(segs, "/")
}

// liveRouteShapes returns the "METHOD shape" key of every route the stub server mounts,
// registry declarations and legacy registrations alike.
func liveRouteShapes(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, r := range sharedRouteStub(t).app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		out[r.Method+" "+routeShape(normalizeRoutePath(r.Path))] = true
	}
	return out
}

// docRowsNamingNoRoute is the comparison itself, separated from the file read so the
// bite test can run it over the REAL reference plus planted rows.
func docRowsNamingNoRoute(rows []docEndpointRow, live map[string]bool) []docEndpointRow {
	var out []docEndpointRow
	for _, row := range rows {
		if !live[row.Method+" "+routeShape(row.Path)] {
			out = append(out, row)
		}
	}
	return out
}

// docRowsSpellingARefusedSlash returns the rows that document a write with a trailing
// slash: routeShape matches one to its route, but refuseTrailingSlashWrites refuses it
// before routing (same method set and length test, query left off).
func docRowsSpellingARefusedSlash(rows []docEndpointRow) []docEndpointRow {
	var out []docEndpointRow
	for _, row := range rows {
		path, _, _ := strings.Cut(row.Path, "?")
		if len(path) > 1 && strings.HasSuffix(path, "/") && !isReadMethod(row.Method) {
			out = append(out, row)
		}
	}
	return out
}

// docPermissionsNotSeeded is the same separation for the prose check.
func docPermissionsNotSeeded(tokens map[string]int, seeded map[string]string) []string {
	var out []string
	for name := range tokens {
		if _, ok := seeded[name]; !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// TestGuard_APIReferenceRowsNameRealRoutes is the production guard: every endpoint row in
// docs/api-reference.md must name a route that exists.
func TestGuard_APIReferenceRowsNameRealRoutes(t *testing.T) {
	body, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}

	rows := parseDocEndpointRows(string(body))
	// Anti-vacuity: a reformat the regex cannot read reports every row valid because there
	// are no rows. The floor is far below the ~540 the file carries: it asks whether the
	// parser still finds the tables, not how long the doc is.
	if len(rows) < 100 {
		t.Fatalf("parsed only %d endpoint rows from %s; the file carries around 540, so the "+
			"parser has stopped recognising the tables and this guard would pass vacuously",
			len(rows), apiReferencePath())
	}

	live := liveRouteShapes(t)
	if len(live) == 0 {
		t.Fatal("the stub server mounted no routes; the guard would pass vacuously")
	}

	for _, row := range docRowsNamingNoRoute(rows, live) {
		t.Errorf("%s:%d documents %s %s, which matches no route the server registers — "+
			"the route was renamed or deleted and the row was not. Fix the row (or delete it); "+
			"parameter NAMES are not compared, so this is a real difference in method, segment "+
			"count, or a literal segment.",
			apiReferencePath(), row.Line, row.Method, row.Path)
	}
	for _, row := range docRowsSpellingARefusedSlash(rows) {
		t.Errorf("%s:%d documents %s %s with a trailing slash, which the API refuses with a 400 before "+
			"routing (refuseTrailingSlashWrites) — spell the path without it",
			apiReferencePath(), row.Line, row.Method, row.Path)
	}
}

// docPermissionTokenRe finds "action:resource" tokens in the prose, backticked or bare
// ("the write needs global manage:veeam").
var docPermissionTokenRe = regexp.MustCompile(`\b([a-z]+):([a-z_]+)\b`)

// docPermissionTokens returns the permission tokens one markdown document names, mapped
// to the line each was found on. A token counts only when its action is one the
// migrations seed, which keeps out every other colon-separated thing in the doc (the
// WebSocket channels "system:events" and "cluster:<cluster_id>:metrics", "host[:port]").
// The cost is the one case it cannot see: a MISSPELLED action is not recognised as a
// permission at all (a misspelled action on a route declaration is still caught by
// TestGuard_DeclaredActionsAreInTheCatalogue). A wildcard ("console:*") is not a token.
func docPermissionTokens(md string, actions map[string]bool) map[string]int {
	out := map[string]int{}
	for i, line := range strings.Split(md, "\n") {
		for _, m := range docPermissionTokenRe.FindAllStringSubmatch(line, -1) {
			if !actions[m[1]] {
				continue
			}
			if _, seen := out[m[0]]; !seen {
				out[m[0]] = i + 1
			}
		}
	}
	return out
}

// seededActions is the action half of the permission catalogue, derived from the same
// migration parse the declaration guard uses, so a migration that seeds a new action widens
// what counts as a permission mention with no edit here.
func seededActions(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for pair := range seededPermissions(t) {
		action, _, ok := strings.Cut(pair, ":")
		if ok {
			out[action] = true
		}
	}
	return out
}

// TestGuard_APIReferencePermissionsExistInTheCatalogue is the production guard for the
// prose: every permission the reference tells an operator about must be one a migration
// seeds (seededPermissions is shared with registry_permission_catalogue_test.go: a second
// parser is a second thing to keep correct).
func TestGuard_APIReferencePermissionsExistInTheCatalogue(t *testing.T) {
	body, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}

	seeded := seededPermissions(t)
	actions := seededActions(t)
	if len(actions) == 0 {
		t.Fatal("no actions parsed from migrations/; the guard would pass vacuously")
	}

	tokens := docPermissionTokens(string(body), actions)
	// Anti-vacuity: if the pattern matched nothing, every permission would be "valid".
	if len(tokens) == 0 {
		t.Fatalf("found no permission tokens in %s; the file names dozens, so the parser has "+
			"stopped recognising them and this guard would pass vacuously", apiReferencePath())
	}

	for _, name := range docPermissionsNotSeeded(tokens, seeded) {
		t.Errorf("%s:%d tells the reader about the permission %q, which no migration seeds into "+
			"the permissions table — HasPermission can only match a seeded row, so an operator "+
			"following this cannot grant it and any route requiring it answers 403 to everyone, "+
			"Admin included. Either seed it in a new migration or correct the reference.",
			apiReferencePath(), tokens[name], name)
	}
}

// TestGuard_APIReferenceErrorSlugsMatchStatusText holds the error-envelope paragraph to the
// slugs errorHandler can send: a slug on one side only, a status mapped in errors.go and
// never documented or a documented slug nothing sends, fails here. Slugs a handler writes
// itself (tfa_required, the *_confirm_required family) are out of its reach, and so is
// whether every status the API SENDS has a slug of its own: statusText maps any status it
// does not list to internal_server_error, which is how the body-limit middleware's 413
// went out under that name.
func TestGuard_APIReferenceErrorSlugsMatchStatusText(t *testing.T) {
	body, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}
	for _, finding := range errorSlugDrift(documentedErrorSlugs(t, string(body)), statusTextSlugs()) {
		t.Error(finding)
	}
}

// errorSlugRe matches one backticked slug in the error-envelope paragraph.
var errorSlugRe = regexp.MustCompile("`([a-z_]+)`")

// documentedErrorSlugs reads the slugs out of the error-envelope paragraph,
// "`error` is a stable slug derived from the status code (...)".
func documentedErrorSlugs(t *testing.T, md string) map[string]bool {
	t.Helper()
	const lead = "`error` is a stable slug derived from the status code ("
	_, rest, ok := strings.Cut(md, lead)
	if !ok {
		t.Fatalf("%s no longer contains %q, the paragraph this guard reads", apiReferencePath(), lead)
	}
	list, _, ok := strings.Cut(rest, ")")
	if !ok {
		t.Fatalf("the error-slug list in %s is never closed with \")\"", apiReferencePath())
	}
	slugs := map[string]bool{}
	for _, m := range errorSlugRe.FindAllStringSubmatch(list, -1) {
		slugs[m[1]] = true
	}
	if len(slugs) == 0 {
		t.Fatalf("parsed no slugs from the error-slug list in %s; the parser has stopped "+
			"recognising them", apiReferencePath())
	}
	return slugs
}

// statusTextSlugs is every value statusText can return, found by asking it
// about every status code there is.
func statusTextSlugs() map[string]bool {
	slugs := map[string]bool{}
	for code := 100; code <= 599; code++ {
		slugs[statusText(code)] = true
	}
	return slugs
}

// errorSlugDrift reports every slug that is on one side only.
func errorSlugDrift(documented, sent map[string]bool) []string {
	var out []string
	for slug := range sent {
		if !documented[slug] {
			out = append(out, fmt.Sprintf("statusText (errors.go) can send the error slug %q, which the "+
				"error-envelope paragraph of %s does not list, so no reader of it knows to expect it",
				slug, apiReferencePath()))
		}
	}
	for slug := range documented {
		if !sent[slug] {
			out = append(out, fmt.Sprintf("%s lists the error slug %q, which statusText (errors.go) "+
				"returns for no status code", apiReferencePath(), slug))
		}
	}
	sort.Strings(out)
	return out
}

// TestGuard_APIReferenceRowsNameRealRoutes_RejectsAPhantomRow plants one fault of each
// kind the three guards report into the REAL reference, which is clean and so cannot tell a
// working comparison from one that stopped rejecting anything: rows for a deleted route and
// a renamed one (a literal changed, not a parameter, which routeShape tolerates by design),
// one of them in the inline row shape; a wildcard where the route takes one parameter; a
// literal in another case (tolerated: Fiber's CaseSensitive is false, so no finding); a write
// and a read spelled with a trailing slash (only the write is a finding); an unseeded
// permission, backticked and bare; and a slug off each side of the error paragraph.
func TestGuard_APIReferenceRowsNameRealRoutes_RejectsAPhantomRow(t *testing.T) {
	body, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}
	const planted = "\n| GET | `/clusters/:id/ghost-endpoint` | A row for a route that was deleted |\n" +
		"| POST | `/pbs-servers/:id/datastores/:store/garbage-collect` | A row whose route was renamed |\n" +
		"| GET | `/clusters/:id/vms/*` | A wildcard where the route takes one parameter |\n" +
		"| GET | `/Veeam-Servers` | A literal spelled in another case, which Fiber serves |\n" +
		"| `POST /api/v1/auth/ghost-token` | A phantom in the inline row shape |\n" +
		"| POST | `/veeam-servers/` | A write spelled with a trailing slash |\n" +
		"| GET | `/veeam-servers/` | A read spelled with a trailing slash |\n" +
		"The node firewall routes need `view:firewall`, and editing rules needs global manage:firewall.\n"

	rows := parseDocEndpointRows(string(body) + planted)
	phantoms := docRowsNamingNoRoute(rows, liveRouteShapes(t))
	wantPhantoms := []string{"/clusters/:id/ghost-endpoint", "/pbs-servers/:id/datastores/:store/garbage-collect",
		"/clusters/:id/vms/*", "/api/v1/auth/ghost-token"}
	if len(phantoms) != len(wantPhantoms) {
		t.Fatalf("the real reference plus %d phantom rows produced %d findings (%+v), want exactly the phantoms — "+
			"either the comparison no longer rejects anything, or the reference has drifted and "+
			"TestGuard_APIReferenceRowsNameRealRoutes is already failing", len(wantPhantoms), len(phantoms), phantoms)
	}
	for i, want := range wantPhantoms {
		if phantoms[i].Path != want {
			t.Errorf("phantom %d = %q, want %q", i, phantoms[i].Path, want)
		}
	}
	if got := docRowsSpellingARefusedSlash(rows); len(got) != 1 || got[0].Method != "POST" || got[0].Path != "/veeam-servers/" {
		t.Errorf("slash findings = %+v, want exactly the slashed POST: a slashed read is a request the API serves", got)
	}

	got := docPermissionsNotSeeded(docPermissionTokens(string(body)+planted, seededActions(t)), seededPermissions(t))
	if want := []string{"manage:firewall", "view:firewall"}; !slices.Equal(got, want) {
		t.Errorf("unseeded permissions = %v, want %v", got, want)
	}

	documented, sent := documentedErrorSlugs(t, string(body)), statusTextSlugs()
	const slug = "unsupported_media_type"
	if !documented[slug] || !sent[slug] {
		t.Fatalf("precondition: %q must be both documented and sent", slug)
	}
	undocumented, unsent := maps.Clone(documented), maps.Clone(sent)
	delete(undocumented, slug)
	delete(unsent, slug)
	for name, drift := range map[string][]string{
		"missing from the paragraph": errorSlugDrift(undocumented, sent),
		"no longer sent":             errorSlugDrift(documented, unsent),
	} {
		if len(drift) != 1 || !strings.Contains(drift[0], strconv.Quote(slug)) {
			t.Errorf("with %q %s, findings = %v, want exactly one naming it", slug, name, drift)
		}
	}
}
