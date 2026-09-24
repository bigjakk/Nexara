package api

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// docs/api-reference.md had NOTHING guarding it. A repo-wide search for
// "api-reference" across the Go sources, the frontend, the Makefile and
// the CI workflows returns no hit: no test read it, no lint step parsed
// it, no link checker walked it. It went roughly twenty commits stale
// before a human noticed, and the only reason anyone noticed at all is
// that the staleness had become large enough to be obvious.
//
// The file is deliberately NOT generated — its own header says the live
// source of truth is the in-app catalog at /settings/api-docs, which
// enumerates the running Fiber router at request time. What it carries
// instead is the material the catalog cannot infer (the auth handshake,
// the error envelope, the WebSocket protocol) plus a hand-curated
// overview of the endpoint groups. That editorial judgment is the point
// of the file and nothing here tries to take it over.
//
// Two things in it are NOT editorial, though, and both are checkable:
//
//   - A row naming a route is a factual claim about the router. If the
//     route was renamed or deleted, the row is simply wrong.
//   - A permission named in the prose is a factual claim about the
//     permission catalogue, which only migrations seed. This is exactly
//     the shape of the bug that made the node firewall routes answer 403
//     to every caller for six months: a well-formed "view:firewall" that
//     no migration had ever seeded. The declaration side of that is
//     closed by registry_permission_catalogue_test.go. The PROSE side —
//     documentation confidently telling an operator to grant a permission
//     that cannot exist — was not.
//
// The reverse direction of the first check is deliberately NOT enforced:
// see TestGuard_APIReferenceRowsNameRealRoutes.

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

// parseDocEndpointRows extracts the endpoint rows from one markdown
// document.
//
// Split out from the file read so it can be exercised on synthetic input.
// A parser that can only ever run against the repo's own reference cannot
// be shown to REJECT anything — it agrees with the file it was written
// against by construction, which is how a guard ends up passing while
// incapable of failing.
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

// routeShape reduces a path to what can honestly be compared between the
// doc and the router: the segment count, which segments are parameters,
// and the literal text of the rest.
//
// Parameter NAMES are deliberately erased. The doc uses its own shorthand
// — ":id" where the route says ":cluster_id", ":node" for ":node_name",
// ":sid" for ":storage_id" — applied consistently across 50-odd tables,
// and that shorthand is an editorial choice about readability, not drift.
// Demanding the route's spelling would fail 200 rows the first time this
// ran and teach the next reader to delete the guard.
//
// It would also be demanding a consistency the CODE does not have. The
// ":node" -> ":node_name" rename is not uniform in the registry either:
// registry_acme.go and registry_apt_repositories.go still declare ":node"
// while registry_nodes.go declares ":node_name". A check keyed on the
// name would have to pick one of those to be correct, and would be wrong
// about the other.
//
// What is NOT erased is the parameter/literal distinction. A doc row
// writing a literal where the route takes a parameter (or the reverse)
// describes a different contract and stays a finding.
//
// The three normalisations before the split each stand for a real
// equivalence rather than a convenience:
//
//   - The "/api/v1" prefix, because doc rows are written relative to the
//     base URL the file documents once at the top, while the router
//     carries it on every path.
//   - A trailing "?query=" fragment, because a row like
//     ".../preflight?action=" is naming ONE route and illustrating its
//     query in the same cell. Fiber routes on the path alone.
//   - A trailing slash, because StrictRouting is unset (buildFiberConfig
//     in server.go), so "/veeam-servers/" and "/veeam-servers" are one
//     route to Fiber and have to be one here. That is the ROUTER's
//     equivalence, and for writes the API no longer offers it:
//     refuseTrailingSlashWrites answers 400, before routing, to any
//     method but GET, HEAD and OPTIONS whose path ends in "/". So a row
//     that spells a write with the slash names a request the API refuses
//     even though its shape matches a route, and
//     docRowsSpellingARefusedSlash reports it separately.
//
// Wildcards keep their own marker rather than collapsing into the
// parameter one: "*" matches any number of trailing segments and ":x"
// matches exactly one, so a row that swapped them would be describing a
// different route.
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
			// Fiber's CaseSensitive is false (see segmentCaptures in
			// registry_shadow_guard_test.go), so a literal differing only
			// in case is the same route.
			segs[i] = strings.ToLower(seg)
		}
	}
	return strings.Join(segs, "/")
}

// liveRouteShapes returns the "METHOD shape" key of every route the stub
// server mounts — the registry's 535 declarations and the legacy
// registrations in router.go alike, because GetRoutes reads the table
// they both land in.
func liveRouteShapes(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, r := range newRouteStubServer(t).app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		out[r.Method+" "+routeShape(normalizeRoutePath(r.Path))] = true
	}
	return out
}

// docRowsNamingNoRoute is the comparison itself, separated from the file
// read and the t.Errorf so a test can run it over the REAL reference plus
// one injected row.
//
// That is a stronger bite-proof than a hand-written fixture: it proves the
// guard rejects a phantom row while looking at the same 540 real rows and
// the same 552 real routes it looks at in production, rather than at a
// three-line document where any parser would agree.
func docRowsNamingNoRoute(rows []docEndpointRow, live map[string]bool) []docEndpointRow {
	var out []docEndpointRow
	for _, row := range rows {
		if !live[row.Method+" "+routeShape(row.Path)] {
			out = append(out, row)
		}
	}
	return out
}

// docRowsSpellingARefusedSlash returns the rows that document a write with
// a trailing slash. routeShape matches such a row to its route, because the
// router serves both spellings; refuseTrailingSlashWrites refuses the
// slashed one before the router sees it, using the same method set and the
// same length test on the path, query left off, as isReadMethod and the
// gate do.
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

// TestGuard_APIReferenceRowsNameRealRoutes is the production guard: every
// endpoint row in docs/api-reference.md must name a route that exists.
//
// The REVERSE direction — every route having a row — is intentionally not
// enforced, for the same reason TestEndpointMetaMatchesRegisteredRoutes
// leaves its own reverse direction alone: the file is a curated overview,
// not a dump of the route table, and requiring full coverage would force
// it to become one. The catalog at /settings/api-docs is the dump, it is
// generated from the live router, and it cannot drift. Eleven routes have
// no row today (the per-node report and sensor readings, favorites, the
// caller's own sessions, /healthz, /version, /changelog, the certificate
// probe) and that is an editorial decision this test has no business
// overriding.
func TestGuard_APIReferenceRowsNameRealRoutes(t *testing.T) {
	body, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}

	rows := parseDocEndpointRows(string(body))
	// Anti-vacuity. A regex that silently stopped matching — a reformat
	// that put the tables behind an HTML <details>, a switch to a
	// different table syntax — would report every row as valid because
	// there would be no rows. The floor is deliberately far below the
	// ~540 rows the file carries: it is asking "did the parser still find
	// the endpoint tables", not "is the doc still this long".
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

// docPermissionTokenRe finds "action:resource" tokens in the prose.
//
// Permissions are written both in backticks ("needs `view:pbs` on that
// cluster") and bare ("the write needs global manage:veeam", the
// /veeam-servers/:id/platforms/:platform_id row), so the backticks cannot
// be part of the pattern.
var docPermissionTokenRe = regexp.MustCompile(`\b([a-z]+):([a-z_]+)\b`)

// docPermissionTokens returns the permission tokens one markdown document
// names, mapped to the line each was found on.
//
// A token only counts when its action is one the migrations actually
// seed. That filter is what keeps the colon-separated things that are NOT
// permissions out of the result, and the doc is full of them: the
// WebSocket channels "system:events", "system:audit" and
// "cluster:<cluster_id>:metrics", and the "host[:port]" in the iSCSI scan
// row. None of their left-hand sides is an action, so none of them is
// mistaken for a permission.
//
// The cost of that filter is the one case it cannot see: a MISSPELLED
// ACTION, "read:cluster" say, is not recognised as a permission mention
// at all rather than being reported as an unseeded one. Closing that
// would mean treating every "word:word" in the prose as a candidate and
// then excluding the channel and port-range namespaces by name — an
// open-ended exclusion list, which is the shape that rots. The action
// vocabulary is a closed set of seven that only a migration can widen, so
// this filter maintains itself. Note that a misspelled action on a route
// DECLARATION is still caught, by
// TestGuard_DeclaredActionsAreInTheCatalogue; it is only the prose that
// has this blind spot.
//
// A wildcard mention ("the dedicated `console:*` permission", "`view:*`")
// is not a token: "*" is not [a-z_]+, so the pattern does not match it at
// all. Those are prose shorthand for a family, not a claim that a row
// named "console:*" exists.
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

// seededActions is the action half of the permission catalogue, derived
// from the same migration parse the declaration guard uses. Deriving it
// rather than hard-coding the seven keeps the two in step: a migration
// that seeds a new action widens what this file recognises as a
// permission mention, with no edit here.
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

// TestGuard_APIReferencePermissionsExistInTheCatalogue is the production
// guard for the prose: every permission the reference tells an operator
// about must be one a migration seeds.
//
// It shares seededPermissions with
// registry_permission_catalogue_test.go rather than re-deriving the
// catalogue. A second parser would be a second thing to keep correct, and
// the failure mode of the pair disagreeing is that one of them silently
// stops catching anything.
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
	// Anti-vacuity, and the reason it is worth having: the whole check
	// hinges on a regex finding tokens in prose. If a rewording, an
	// encoding change or a botched edit to the pattern made it match
	// nothing, every permission in the file would be "valid".
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

// TestGuard_APIReferenceErrorSlugsMatchStatusText holds the error-envelope
// paragraph to the slugs errorHandler can actually send. The paragraph lists
// the slugs DERIVED FROM A STATUS CODE, and statusText is where those come
// from, so a slug on one side only — a status mapped in errors.go and never
// documented, or a documented slug nothing sends any more — fails here. The
// slugs a handler writes itself — tfa_required, token_exists, the
// *_confirm_required family and a few more — are out of its reach.
//
// It cannot tell whether every status the API SENDS has a slug of its own.
// statusText maps any status it does not list to internal_server_error, which
// is how the body-limit middleware's 413 went out under that name; only the
// code that returns a status knows it returns it.
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

// --- bite-proofs against the real file ----------------------------------

// TestGuard_APIReferenceErrorSlugsMatchStatusText_RejectsDriftEitherWay runs
// the production comparison over the REAL paragraph and the real statusText
// with one slug taken off each side in turn, and requires exactly that slug
// back as the one finding.
func TestGuard_APIReferenceErrorSlugsMatchStatusText_RejectsDriftEitherWay(t *testing.T) {
	body, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}
	documented := documentedErrorSlugs(t, string(body))
	sent := statusTextSlugs()

	const slug = "unsupported_media_type"
	if !documented[slug] || !sent[slug] {
		t.Fatalf("precondition: %q must be both documented and sent, or removing it proves nothing", slug)
	}

	undocumented := maps.Clone(documented)
	delete(undocumented, slug)
	if got := errorSlugDrift(undocumented, sent); len(got) != 1 || !strings.Contains(got[0], strconv.Quote(slug)) {
		t.Errorf("with %q missing from the paragraph, findings = %v, want exactly one naming it", slug, got)
	}

	unsent := maps.Clone(sent)
	delete(unsent, slug)
	if got := errorSlugDrift(documented, unsent); len(got) != 1 || !strings.Contains(got[0], strconv.Quote(slug)) {
		t.Errorf("with statusText no longer sending %q, findings = %v, want exactly one naming it", slug, got)
	}
}

// TestGuard_APIReferenceRowsNameRealRoutes_RejectsAPhantomRow runs the
// production comparison over the REAL reference with one extra row spliced
// in, and requires that row — and only that row — to come back as a
// finding.
//
// Doing it against the real document rather than a fixture is what makes
// it worth having. The reference is currently clean, so the production
// test passes; a comparison that had quietly become incapable of rejecting
// anything would pass identically, and a three-line fixture would not tell
// those apart. This does: the same 540 real rows, the same 552 real
// routes, plus one row naming a route that does not exist.
func TestGuard_APIReferenceRowsNameRealRoutes_RejectsAPhantomRow(t *testing.T) {
	body, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}

	// A deleted route and a renamed one: the two ways the file goes stale.
	//
	// The renamed one changes a LITERAL segment ("gc" -> "garbage-collect")
	// rather than a parameter name, because a parameter rename is exactly
	// what routeShape is built to tolerate — the doc already writes ":id"
	// where that route declares ":pbs_id". Picking a parameter rename here
	// would be testing that the guard reports the doc's own shorthand,
	// which would be the bug, not the check.
	const mutation = "\n| GET | `/clusters/:id/ghost-endpoint` | A row for a route that was deleted |\n" +
		"| POST | `/pbs-servers/:id/datastores/:store/garbage-collect` | A row whose route was renamed |\n"

	rows := parseDocEndpointRows(string(body) + mutation)
	live := liveRouteShapes(t)

	got := docRowsNamingNoRoute(rows, live)
	if len(got) != 2 {
		t.Fatalf("the real reference plus two phantom rows produced %d findings (%+v), want "+
			"exactly the two phantoms — either the comparison no longer rejects anything, or "+
			"the reference itself has drifted and TestGuard_APIReferenceRowsNameRealRoutes "+
			"is already failing", len(got), got)
	}
	for _, want := range []string{"/clusters/:id/ghost-endpoint", "/pbs-servers/:id/datastores/:store/garbage-collect"} {
		found := false
		for _, row := range got {
			if row.Path == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the phantom row %q was not reported; findings = %+v", want, got)
		}
	}
}

// TestGuard_APIReferenceRowsNameRealRoutes_RejectsASlashedWrite is the
// same splice for the trailing-slash check: the real reference plus a write
// and a read, each spelled with a trailing slash, on a route that exists.
//
// Only the write may come back. The gate lets a GET through, so the slashed
// read still names a request the API serves, and a check that reported it
// as well would be telling the doc to stop spelling something the API
// accepts. The shape comparison is asked too, and must report neither:
// the slash is the only thing wrong with the write, so without the second
// check nothing would catch it.
func TestGuard_APIReferenceRowsNameRealRoutes_RejectsASlashedWrite(t *testing.T) {
	body, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}
	const mutation = "\n| POST | `/veeam-servers/` | A write spelled with a trailing slash |\n" +
		"| GET | `/veeam-servers/` | A read spelled with a trailing slash |\n"
	rows := parseDocEndpointRows(string(body) + mutation)

	if got := docRowsNamingNoRoute(rows, liveRouteShapes(t)); len(got) != 0 {
		t.Fatalf("precondition: the shape comparison reported %+v; both spliced rows name a real route "+
			"by shape, so the reference has drifted or the splice is wrong", got)
	}
	got := docRowsSpellingARefusedSlash(rows)
	if len(got) != 1 || got[0].Method != "POST" || got[0].Path != "/veeam-servers/" {
		t.Errorf("findings = %+v, want exactly the slashed POST — either the check no longer rejects a "+
			"slashed write, it rejects a slashed read the API serves, or the reference itself spells a "+
			"write with a slash and TestGuard_APIReferenceRowsNameRealRoutes is already failing", got)
	}
}

// TestGuard_APIReferencePermissionsExistInTheCatalogue_RejectsAnUnseededOne
// is the same injection for the prose check, using the exact permission
// that caused the original bug: view:firewall reads like every other
// permission in the file and no migration has ever seeded it.
func TestGuard_APIReferencePermissionsExistInTheCatalogue_RejectsAnUnseededOne(t *testing.T) {
	body, err := os.ReadFile(apiReferencePath())
	if err != nil {
		t.Fatalf("reading %s: %v", apiReferencePath(), err)
	}

	const mutation = "\nThe node firewall routes need `view:firewall`, and editing rules needs " +
		"`manage:firewall`.\n"

	seeded := seededPermissions(t)
	tokens := docPermissionTokens(string(body)+mutation, seededActions(t))

	got := docPermissionsNotSeeded(tokens, seeded)
	want := []string{"manage:firewall", "view:firewall"}
	if len(got) != len(want) {
		t.Fatalf("the real reference plus two unseeded permissions produced %v, want exactly %v — "+
			"either the check no longer rejects anything, or the reference itself has drifted and "+
			"TestGuard_APIReferencePermissionsExistInTheCatalogue is already failing", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("finding %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// --- parser bite-proofs -------------------------------------------------
//
// Everything below runs on synthetic markdown. The point is to show the
// parsers REJECT and ACCEPT the right things independently of the repo's
// own file, which they agree with by construction.

// TestParseDocEndpointRows_ReadsTheShapesTheFileUses pins both row
// spellings and, more importantly, the non-rows: the table header, the
// separator, and the rate-limit table's third-cell path mention.
func TestParseDocEndpointRows_ReadsTheShapesTheFileUses(t *testing.T) {
	t.Parallel()

	const md = "## Clusters\n" +
		"\n" +
		"| Method | Path | Description |\n" +
		"|--------|------|-------------|\n" +
		"| GET | `/clusters` | List all clusters |\n" +
		"| DELETE | `/clusters/:id` | Remove cluster (`?revoke=1` also revokes) |\n" +
		"\n" +
		"| Endpoint | Purpose |\n" +
		"|----------|---------|\n" +
		"| `POST /api/v1/auth/ws-token` | Hub token |\n" +
		"\n" +
		"| Scope | Budget | Applies to |\n" +
		"|-------|--------|------------|\n" +
		"| Cluster create | 10/min | `POST /clusters` — each call spends a login attempt |\n"

	got := parseDocEndpointRows(md)
	want := []docEndpointRow{
		{Method: "GET", Path: "/clusters", Line: 5},
		{Method: "DELETE", Path: "/clusters/:id", Line: 6},
		{Method: "POST", Path: "/api/v1/auth/ws-token", Line: 10},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestRouteShape_ToleratesTheDocShorthandAndStillDiscriminates is the
// heart of the first guard: the comparison has to accept every rename the
// doc's shorthand represents and reject everything else. A shape function
// that accepted too much would pass a row naming a route that does not
// exist, which is the only thing this guard is for.
func TestRouteShape_ToleratesTheDocShorthandAndStillDiscriminates(t *testing.T) {
	t.Parallel()

	same := []struct {
		name    string
		doc     string
		route   string
		because string
	}{
		{"the :id / :cluster_id shorthand", "/clusters/:id", "/api/v1/clusters/:cluster_id",
			"the doc abbreviates the cluster id on every cluster-scoped row"},
		{"the :node / :node_name shorthand", "/clusters/:id/nodes/:node/status",
			"/api/v1/clusters/:cluster_id/nodes/:node_name/status",
			"registry_nodes.go renamed it; registry_acme.go did not"},
		{"the :sid / :storage_id shorthand", "/clusters/:id/storage/:sid/content",
			"/api/v1/clusters/:cluster_id/storage/:storage_id/content", ""},
		{"a trailing slash", "/veeam-servers/", "/api/v1/veeam-servers",
			"StrictRouting is unset, so Fiber serves both spellings from one route"},
		{"an illustrative query string", "/clusters/:id/ceph/osds/:osd_id/preflight?action=",
			"/api/v1/clusters/:cluster_id/ceph/osds/:osd_id/preflight",
			"Fiber routes on the path; the query is documentation"},
		{"a case difference in a literal", "/VM-Import", "/api/v1/vm-import",
			"Fiber's CaseSensitive is false"},
		{"a wildcard tail", "/clusters/:id/storage/:sid/content/*",
			"/api/v1/clusters/:cluster_id/storage/:storage_id/content/*", ""},
	}
	for _, tt := range same {
		t.Run("same/"+tt.name, func(t *testing.T) {
			t.Parallel()
			if routeShape(tt.doc) != routeShape(tt.route) {
				t.Errorf("routeShape(%q)=%q != routeShape(%q)=%q; the doc shorthand must be "+
					"tolerated (%s)", tt.doc, routeShape(tt.doc), tt.route, routeShape(tt.route), tt.because)
			}
		})
	}

	different := []struct {
		name  string
		doc   string
		route string
	}{
		{"a different literal segment", "/clusters/:id/vms", "/api/v1/clusters/:cluster_id/containers"},
		{"a missing segment", "/clusters/:id/vms", "/api/v1/clusters/:cluster_id/vms/:vm_id"},
		{"an extra segment", "/clusters/:id/vms/summary", "/api/v1/clusters/:cluster_id/vms"},
		{"a literal where the route takes a parameter", "/alerts/summary", "/api/v1/alerts/:id"},
		{"a parameter where the route takes a literal", "/alerts/:id", "/api/v1/alerts/summary"},
		{"a wildcard where the route takes one parameter", "/x/*", "/api/v1/x/:id"},
		{"a renamed resource", "/pbs-servers", "/api/v1/veeam-servers"},
	}
	for _, tt := range different {
		t.Run("different/"+tt.name, func(t *testing.T) {
			t.Parallel()
			if routeShape(tt.doc) == routeShape(tt.route) {
				t.Errorf("routeShape(%q) == routeShape(%q) == %q, but these describe different "+
					"routes; the comparison is too loose to catch drift", tt.doc, tt.route, routeShape(tt.doc))
			}
		})
	}
}

// TestDocPermissionTokens_SeparatesPermissionsFromEverythingElseWithAColon
// is the second parser's bite-proof. The accepted cases are transcribed
// from the reference; the rejected ones are the colon-separated tokens
// that share the file with them and would each be reported as a
// nonexistent permission if the action filter were dropped.
func TestDocPermissionTokens_SeparatesPermissionsFromEverythingElseWithAColon(t *testing.T) {
	t.Parallel()

	actions := map[string]bool{
		"view": true, "manage": true, "delete": true, "execute": true,
		"acknowledge": true, "generate": true, "console": true,
	}

	tests := []struct {
		name    string
		md      string
		want    []string
		notWant []string
	}{
		{
			name: "a backticked permission in prose",
			md:   "Reads need `view:veeam`, writes `manage:veeam`.",
			want: []string{"view:veeam", "manage:veeam"},
		},
		{
			name: "a BARE permission in a table cell",
			md:   "| PUT | `/x` | so the write needs global manage:veeam |",
			want: []string{"manage:veeam"},
		},
		{
			name:    "a websocket channel is not a permission",
			md:      "| `system:audit` | Non-cluster audit entries | Global `view:audit` |",
			want:    []string{"view:audit"},
			notWant: []string{"system:audit"},
		},
		{
			name:    "a templated channel is not a permission",
			md:      "`cluster:<cluster_id>:metrics` | Live metric samples | `view:cluster` |",
			want:    []string{"view:cluster"},
			notWant: []string{"cluster:metrics", "cluster_id:metrics"},
		},
		{
			name:    "a host:port range is not a permission",
			md:      "Discover iSCSI targets on a portal — `?portal=<host[:port]>`",
			notWant: []string{"host:port"},
		},
		{
			name:    "a wildcard family is not a token",
			md:      "requires the dedicated **`console:*`** permission, not `view:*`: `console:node` for `node_shell`",
			want:    []string{"console:node"},
			notWant: []string{"console:*", "view:*", "console:node_shell"},
		},
		{
			name:    "a URL scheme is not a permission",
			md:      "In production: `https://nexara.example.com/api/v1` and `wss://nexara.example.com/ws`",
			notWant: []string{"https:", "wss:"},
		},
		{
			name:    "a JSON key is not a permission",
			md:      `Response: { "access_token": "...", "expires_at": 1767225600 }`,
			notWant: []string{"access_token:", "expires_at:"},
		},
		{
			name: "an unseeded resource on a real action IS a token",
			md:   "The node firewall routes require `view:firewall`.",
			want: []string{"view:firewall"},
		},
		{
			name: "a header name is not a permission",
			md:   "Sec-WebSocket-Protocol: nexara.token, nexara.token.<jwt>",
			// "Protocol: nexara" would need an action named "protocol".
			notWant: []string{"protocol:nexara"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := docPermissionTokens(tt.md, actions)
			for _, w := range tt.want {
				if _, ok := got[w]; !ok {
					t.Errorf("%s not parsed; got %v", w, sortedTokenNames(got))
				}
			}
			for _, w := range tt.notWant {
				if _, ok := got[w]; ok {
					t.Errorf("%s was parsed as a permission but must not be; got %v", w, sortedTokenNames(got))
				}
			}
		})
	}
}

// TestDocPermissionTokens_ActionFilterComesFromTheMigrations proves the
// filter is driven by the seeded catalogue rather than a hard-coded list:
// widen the action set and a token the narrower set ignored becomes
// visible. This is what makes a future migration's new action
// automatically in scope for the prose check.
func TestDocPermissionTokens_ActionFilterComesFromTheMigrations(t *testing.T) {
	t.Parallel()

	const md = "The report is produced with `render:report`."

	if got := docPermissionTokens(md, map[string]bool{"view": true}); len(got) != 0 {
		t.Errorf("with actions={view}, got %v; want nothing — \"render\" is not an action", sortedTokenNames(got))
	}
	got := docPermissionTokens(md, map[string]bool{"view": true, "render": true})
	if _, ok := got["render:report"]; !ok {
		t.Errorf("with actions={view,render}, got %v; want render:report — the action set drives "+
			"what counts as a permission mention", sortedTokenNames(got))
	}
}

// TestAPIReferenceGuardsReadTheRealFile is the last vacuity check the two
// production guards cannot make about themselves: that the path they read
// resolves at all. A renamed or moved docs/api-reference.md would make
// both of them t.Fatal, which is the correct outcome — but it is worth
// stating the dependency explicitly rather than discovering it as a
// confusing failure inside a guard about something else.
func TestAPIReferenceGuardsReadTheRealFile(t *testing.T) {
	t.Parallel()

	info, err := os.Stat(apiReferencePath())
	if err != nil {
		t.Fatalf("docs/api-reference.md is not where the guards look (%s): %v", apiReferencePath(), err)
	}
	if info.Size() == 0 {
		t.Fatal("docs/api-reference.md is empty")
	}
}

func sortedTokenNames(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k, line := range m {
		out = append(out, fmt.Sprintf("%s(line %d)", k, line))
	}
	sort.Strings(out)
	return out
}
