package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// The catalogue (internal/api/apischema/catalogue.go) is only worth having
// if declarations actually go through it. Nothing stops the next author
// pasting `^[A-Za-z0-9][A-Za-z0-9._-]*$` into a new route — it looks
// self-explanatory, it is two characters shorter, and it is how every one
// of these rules came to exist in three slightly different copies in the
// first place. The metric server id, the PVE backup job id and the firewall
// alias name each had their own copy of that regex; the VM and container
// resize routes each had their own copy of the delta pattern; and
// emptyOrNodeName was a hand transcription of the node-name format that
// nothing tied back to it.
//
// So this file reads the declaration files as SOURCE, not as a compiled
// registry: at runtime a literal and apischema.Rule("...") are the same
// string and the difference this guards is invisible.

// patternLiteral is one inline regex written at a declaration site.
type patternLiteral struct {
	file  string
	line  int
	regex string
}

// registryPatternLiterals returns every Pattern whose value is fully spelled
// out at the declaration — a string literal, or a concatenation of string
// literals.
//
// The concatenation case is collected on purpose. `^[A-Za-z0-9]` +
// `[A-Za-z0-9.:_-]*$` is a second hand-written copy of a catalogued rule
// that reads as two harmless fragments, and folding it here is what stops it
// being the way round both guards below.
//
// A Pattern that reaches through a named constant or apischema.Rule is NOT
// collected, and neither is a concatenation with an identifier among its
// parts — `^` + devicePathBody + `$` in registry_nodes.go is the shape that
// matters. Those already have exactly one definition, which is the whole ask.
func registryPatternLiterals(t *testing.T) []patternLiteral {
	t.Helper()

	// The declaration files only. No repo walk: stale agent worktrees live
	// under .claude/worktrees/ and a walk finds their copies of these same
	// files, which is how a repo-walking test comes to assert things about
	// source nobody is editing.
	files, err := filepath.Glob("registry_*.go")
	if err != nil {
		t.Fatalf("globbing the registry files: %v", err)
	}
	if len(files) < 10 {
		t.Fatalf("found %d registry files, want the declaration set; the glob is wrong and this test "+
			"would pass by looking at nothing", len(files))
	}

	var out []patternLiteral
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Pattern" {
				return true
			}
			regex, ok := foldStringLiterals(kv.Value)
			if !ok {
				return true
			}
			out = append(out, patternLiteral{
				file:  name,
				line:  fset.Position(kv.Value.Pos()).Line,
				regex: regex,
			})
			return true
		})
	}
	return out
}

// foldStringLiterals evaluates an expression that is written out in full at
// the declaration: a string literal, or any "+" tree whose every leaf is
// one. It reports false as soon as a leaf is anything else — an identifier,
// a call — because such an expression has a definition elsewhere and is not
// a copy of the rule.
func foldStringLiterals(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return v, true
	case *ast.ParenExpr:
		return foldStringLiterals(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := foldStringLiterals(e.X)
		if !ok {
			return "", false
		}
		right, ok := foldStringLiterals(e.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	default:
		return "", false
	}
}

// TestNoInlinePatternIsWrittenTwice is the guard that keeps the catalogue
// load-bearing. A regex two declarations both want is a rule, and a rule
// belongs in the catalogue where its provenance and its witnesses live —
// not in two places that are free to drift apart by one character.
func TestNoInlinePatternIsWrittenTwice(t *testing.T) {
	t.Parallel()

	sites := make(map[string][]patternLiteral)
	for _, lit := range registryPatternLiterals(t) {
		sites[lit.regex] = append(sites[lit.regex], lit)
	}

	for _, regex := range slices.Sorted(maps.Keys(sites)) {
		found := sites[regex]
		if len(found) < 2 {
			continue
		}
		where := make([]string, 0, len(found))
		for _, lit := range found {
			where = append(where, lit.file+":"+strconv.Itoa(lit.line))
		}
		t.Errorf("the regex %s is written out at %d declaration sites (%s). Two copies of one rule drift: "+
			"add it to apischema/catalogue.go with what it permits and where it came from, and spell both "+
			"sites Pattern: apischema.Rule(\"<name>\")",
			regex, len(found), strings.Join(where, ", "))
	}
}

// TestNoInlinePatternRestatesACataloguedRule closes the gap the test above
// leaves open. Re-inlining ONE site of a rule three declarations share
// leaves only one literal, so counting literals cannot see it — and it is
// the likelier mistake, because the site that gets pasted over is whichever
// one someone was editing, not the pair.
//
// A literal that is character-for-character a catalogued rule is that rule,
// and the copy silently stops tracking it the moment the catalogue entry is
// corrected.
func TestNoInlinePatternRestatesACataloguedRule(t *testing.T) {
	t.Parallel()

	catalogued := make(map[string]string) // regex -> rule name
	for _, d := range apischema.Catalogue() {
		if d.RuleIsRegex {
			catalogued[d.Rule] = d.Name
		}
	}

	for _, lit := range registryPatternLiterals(t) {
		name, ok := catalogued[lit.regex]
		if !ok {
			continue
		}
		t.Errorf("%s:%d writes out the regex %s, which is the catalogued rule %q. The copy no longer "+
			"tracks the entry that carries its provenance and its witnesses: spell it "+
			"apischema.Rule(%q) — or, if the rule is a format, Format: %q.",
			lit.file, lit.line, lit.regex, name, name, name)
	}
}

// TestEveryCataloguedPatternIsUsed is the other direction. A catalogue
// entry nobody declares is a rule the tests keep green and the API does not
// enforce — documentation of something that is not there, which is the
// failure mode this whole file exists to prevent, pointed the other way.
func TestEveryCataloguedPatternIsUsed(t *testing.T) {
	t.Parallel()

	used := make(map[string]bool)
	fset := token.NewFileSet()
	files, err := filepath.Glob("registry_*.go")
	if err != nil {
		t.Fatalf("globbing the registry files: %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Rule" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "apischema" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			name, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("unquoting %s: %v", lit.Value, err)
			}
			used[name] = true
			return true
		})
	}

	for _, d := range apischema.Catalogue() {
		if d.Kind != apischema.KindPattern {
			continue
		}
		if !used[d.Name] {
			t.Errorf("catalogue pattern %q is declared nowhere: it documents a rule this API does not "+
				"apply. Either a declaration should be using it, or the entry should go.", d.Name)
		}
	}
}

// TestCataloguedRulesStillAcceptWhatTheirSitesSend runs each catalogued
// pattern's witnesses against EVERY declaration that carries it, as the
// router compiled it — not against the regex on its own, and not against one
// sampled site.
//
// The distinction is the point. Consolidation moved these rules; if one
// arrived at a site carrying a MaxLength shorter than the rule's own
// witnesses, or a Format that runs before the pattern and rewrites the
// value, the regex would still match while the ROUTE turned the value away.
// That is exactly the "do not change what any endpoint accepts as a side
// effect" failure, and it is invisible to a test that checks the regex.
//
// It walks every site rather than sampling one because sampling could not
// see the thing it was there to protect. pve-object-id alone has consumers
// at three different MaxLengths (64, 100, 128), the create-body half of
// ceph-pool-name is a different declaration from the path half, and the five
// -or-empty rules had no sampled site at all — so the rules with the most
// consumers were the ones checked least. Walking is also cheap: the registry
// is already built in-process for the other guards here.
func TestCataloguedRulesStillAcceptWhatTheirSitesSend(t *testing.T) {
	t.Parallel()

	byRule := make(map[string][]ruleSite)
	for _, site := range declaredRuleSites(t) {
		byRule[site.rule] = append(byRule[site.rule], site)
	}

	for _, d := range apischema.Catalogue() {
		sites := byRule[d.Name]
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()

			if len(sites) == 0 {
				// A FORMAT may legitimately have no site: it is registered
				// in the format registry whether or not a declaration
				// reaches for it, and five (email, ip, mac-addr,
				// fingerprint-sha256, bwlimit) currently do not.
				// A PATTERN with no site documents a rule this API does not
				// apply — TestEveryCataloguedPatternIsUsed reports the
				// source-level version of that, and reaching here means the
				// rule is spelled apischema.Rule somewhere that never
				// becomes a declared parameter, the same finding one layer
				// down.
				if d.Kind == apischema.KindPattern {
					t.Fatalf("no declared parameter carries this rule, so its witnesses check nothing")
				}
				t.Skip("no declaration carries this format")
			}

			for _, site := range sites {
				for _, v := range d.Accepts {
					if siteAccepts(site.prop, v) {
						continue
					}
					// The declaration refuses a value the rule permits.
					// That is ALLOWED — a route may be stricter than the
					// general rule — but only if a reader of the payload
					// can SEE why. narrowedByPublishedFacet asks exactly
					// that: does the parameter's own published bound or
					// enum refuse this value on its own?
					if narrowedByPublishedFacet(site.prop, v) {
						continue
					}
					t.Errorf("%s: %q is catalogued as accepted by %q, and this declaration rejects it "+
						"for a reason the docs payload does not publish. A route may narrow a rule, but "+
						"the narrowing has to be a DECLARED facet — a MaxLength, a MinLength, an Enum — "+
						"so that /api/v1/api-docs shows it beside the rule. A narrowing that lives in a "+
						"Format that rewrites the value first, or anywhere else the schema cannot render, "+
						"leaves a caller reading permits text that the route will not honour.",
						site, v, d.Name)
				}
				for _, v := range d.Rejects {
					if siteAccepts(site.prop, v) {
						t.Errorf("%s: %q is catalogued as rejected, but this declaration accepts it",
							site, v)
					}
				}
			}
		})
	}
}

// siteAccepts reports whether one declared parameter admits a value.
//
// Requires and Alias are cross-field rules about OTHER parameters; this
// asks only what the declaration admits for THIS value, so they are
// cleared rather than satisfied.
func siteAccepts(prop apischema.Property, v string) bool {
	prop.Requires = nil
	prop.Alias = ""
	_, err := apischema.Properties{"v": prop}.Validate(map[string]any{"v": v})
	return err == nil
}

// narrowedByPublishedFacet reports whether the parameter's OWN published
// facets — everything /api/v1/api-docs renders BESIDE the rule — refuse
// this value on their own.
//
// It removes the rule and keeps the rest. Which field is the rule depends
// on how the site reaches it, and both are published, so only one is
// dropped: a Format site keeps its Pattern (a second, independently
// rendered constraint), and a Pattern site keeps its Format (there are
// none, but the asymmetry should not be baked in). If what is left still
// refuses the value, a caller reading the payload can see the refusal
// coming.
//
// If it ACCEPTS, the refusal came from the rule's own application at this
// site rather than from anything rendered — which is not a contradiction
// in terms, because a format NORMALIZES before the bounds are checked. A
// MinLength of 4 beside the disk-size format refuses "500G": four
// characters as the caller types it, three after normalization to "500".
// The published bound is measured against a value the caller never sees,
// so the payload states a rule the route will not honour.
func narrowedByPublishedFacet(prop apischema.Property, v string) bool {
	published := prop
	published.Requires = nil
	published.Alias = ""
	if prop.Format != "" {
		published.Format = ""
	} else {
		published.Pattern = ""
	}
	return !siteAccepts(published, v)
}

// ruleNarrowingSites is the closed set of declared parameters that refuse
// a value their own catalogued rule permits.
//
// Every entry here is a DECLARED, PUBLISHED narrowing — the test below
// re-derives the set and checks the narrowing is visible in the payload,
// so an entry cannot be added for a narrowing a caller cannot see. The
// list exists for the other direction, which no derived check can provide:
// it fails when a narrowing DISAPPEARS.
//
// That is not hypothetical. Both entries below were added to stop the
// payload contradicting itself: snap_name's handler (validateSnapshotName)
// caps the name at 40 where the pve-configid rule permits 128, and until
// the MaxLength was declared the payload published "2 to 128 characters"
// for a route that answers 400 at 41. Delete the two MaxLength lines and
// every other test in this repo stays green — the schema simply gets wider
// — which is why the floor has to be a list rather than a derivation.
//
// # What this cannot see
//
// THE BIG ONE, and it is no longer this file's to carry: a site that stops
// carrying the rule at all. declaredRuleSites finds a pattern site by
// matching the declared regex against the catalogue's, so REPLACING
// apischema.Rule("x") with a tighter literal does not register as a
// narrowing — it removes the parameter from the walk, and every guard in
// this file then has nothing to say about it. Swapping pveObjectNameParam's
// Rule("pve-object-id") for `^[a-z][a-z0-9]*$` with a MaxLength of 8 narrows
// 20 routes across ACME, firewall and SDN, and all six rule guards here stay
// green; only unrelated domain tests notice, and only by accident.
// TestNoInlinePatternRestatesACataloguedRule catches the VERBATIM re-inline
// of a catalogued regex, which is the copy-paste case, and is blind to a
// tightened one for the same reason: it compares for equality.
//
// registry_rule_reference_ratchet_test.go closes it, with the shape this
// note asked for — a ratchet over declaration SITES that still spell
// apischema.Rule, read from the source, rather than over rules read from the
// build. Nothing here changed: these guards still cannot see it, and are not
// meant to.
//
// The lesser one: a narrowing that lives only in the handler leaves no
// trace a declaration walk can read. Before the MaxLength was declared,
// the registry genuinely admitted a 128-character snap_name and only the
// handler refused it. Scraping the Description for a stated limit was
// considered and rejected — POST /sdn/zones' `zone` says "Proxmox caps
// this at 8 characters for most zone types", which is an UPSTREAM
// narrowing this API deliberately does not enforce, so a prose guard would
// demand a bound that should not exist and would be silenced rather than
// satisfied. The reachable discipline is: when you find a handler-side
// narrowing, declare it, and this list holds it there.
var ruleNarrowingSites = []string{
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots [snap_name] narrows pve-configid",
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots [snap_name] narrows pve-configid",
}

// TestGuard_RuleNarrowingSitesAreDeclared is the ratchet under
// ruleNarrowingSites. It fails in both directions: a narrowing that
// vanishes, and one that appears without anyone deciding it should.
func TestGuard_RuleNarrowingSitesAreDeclared(t *testing.T) {
	t.Parallel()

	byName := make(map[string]apischema.RuleDoc)
	for _, d := range apischema.Catalogue() {
		byName[d.Name] = d
	}

	var got []string
	for _, site := range declaredRuleSites(t) {
		d := byName[site.rule]
		for _, v := range d.Accepts {
			if siteAccepts(site.prop, v) {
				continue
			}
			got = append(got, site.String()+" narrows "+site.rule)
			break
		}
	}
	slices.Sort(got)

	if !slices.Equal(got, ruleNarrowingSites) {
		t.Errorf("parameters refusing a value their catalogued rule permits =\n  %s\nwant\n  %s\n\n"+
			"A narrowing that DISAPPEARED means a declared bound was deleted and the route's real "+
			"limit went back to living somewhere the payload cannot show. A narrowing that APPEARED "+
			"means a route just became stricter than its documented rule: confirm the bound is "+
			"declared (not enforced only in the handler) and add it here.",
			strings.Join(got, "\n  "), strings.Join(ruleNarrowingSites, "\n  "))
	}
}

// ruleSite is one declared parameter that carries a catalogued rule.
type ruleSite struct {
	rule   string
	method string
	path   string
	param  string
	prop   apischema.Property
}

func (s ruleSite) String() string {
	return s.method + " " + s.path + " [" + s.param + "]"
}

// declaredRuleSites walks the built registry and returns every parameter
// — and every array element schema — that carries a catalogued rule,
// whether as a Format or as a Pattern.
//
// It matches on the RULE TEXT rather than on how the declaration spelled it,
// because at runtime apischema.Rule("x") and a pasted copy of the same regex
// are the same string. That is deliberate for the copy-paste case: a site
// that regressed to a VERBATIM literal is still held to the rule here, and
// TestNoInlinePatternRestatesACataloguedRule reports the regression itself.
//
// It is also this function's blind spot, and the blind spot is bigger than
// the case it handles. A literal that is TIGHTENED rather than copied
// matches no catalogue entry, so the site silently leaves this walk — and
// TestNoInlinePatternRestatesACataloguedRule compares for equality, so it
// does not see a tightened copy either. Neither guard reports a rule that
// was replaced instead of re-inlined.
//
// That is covered from the other side, and has to be: no reading of the
// BUILT registry can distinguish apischema.Rule("x") from a pasted copy,
// because they compile to the same string. See ruleReferenceSites in
// registry_rule_reference_ratchet_test.go, which reads the declarations as
// source and ratchets the ones that name their rule.
func declaredRuleSites(t *testing.T) []ruleSite {
	t.Helper()

	// Patterns are reached BY VALUE, so they need a regex index; a format
	// is reached BY NAME and needs none. Formats are deliberately left out
	// of this index: a format validates AND NORMALIZES, so a bare Pattern
	// that merely matches a format's regex is not that format, and
	// attributing it would hold the site to witnesses it never runs.
	rules := make(map[string]string) // rule text -> rule name
	for _, d := range apischema.Catalogue() {
		if d.Kind == apischema.KindPattern {
			rules[d.Rule] = d.Name
		}
	}
	formats := make(map[string]bool, len(rules))
	for _, d := range apischema.Catalogue() {
		if d.Kind == apischema.KindFormat {
			formats[d.Name] = true
		}
	}
	// ruleOf answers with the catalogued rule a property carries, however
	// it reaches it.
	ruleOf := func(p apischema.Property) (string, bool) {
		if formats[p.Format] {
			return p.Format, true
		}
		name, ok := rules[p.Pattern]
		return name, ok
	}

	s := newRouteStubServer(t)
	endpoints := s.registry.Endpoints()
	if len(endpoints) < 400 {
		t.Fatalf("the registry reports %d endpoints, want the declared set; this test would pass by "+
			"looking at almost nothing", len(endpoints))
	}

	var out []ruleSite
	for _, e := range endpoints {
		for _, param := range slices.Sorted(maps.Keys(e.Parameters)) {
			prop := e.Parameters[param]
			if name, ok := ruleOf(prop); ok {
				out = append(out, ruleSite{rule: name, method: e.Method, path: e.Path, param: param, prop: prop})
			}
			if prop.Items == nil {
				continue
			}
			// An element schema carries the same facets minus the ones
			// compileItems forbids, so it validates standalone.
			if name, ok := ruleOf(*prop.Items); ok {
				item := *prop.Items
				out = append(out, ruleSite{
					rule: name, method: e.Method, path: e.Path, param: param + "[]", prop: item,
				})
			}
		}
	}
	return out
}

// baseRuleSiteCounts ratchets how many declared parameters carry each CREATE
// rule that has a "-existing" twin.
//
// This count is the ONLY thing in the package that notices a create site
// losing its rule, whether one site of several or all of them. Nothing else
// covers it: formats are not ratcheted (registry_rule_reference_ratchet_test.go
// says so outright), a bare format substitution narrows nothing so
// TestGuard_RuleNarrowingSitesAreDeclared stays quiet, and the guard's own
// emptiness check below is on the TWIN's addressing sites, not on these.
//
// What a swap costs is smaller than it first looks, and the honest version is
// worth writing down. Point registry_ha.go's group Format at node-name and the
// declaration — and the /api/v1/api-docs payload built from it — stop stating
// the rule this pairing is about. It does NOT strand an object: both HA ids are
// `format: pve-configid` upstream too (internal/proxmox/client_ha.go), so PVE
// refuses "pve.01" itself and the caller gets a confusing 400 rather than
// something created and unaddressable. The version that really does strand an
// object is the LENGTH one, because PVE's $CONFIGID_RE states no maximum, and
// the witnesses in the guard below are what cover that.
//
// A count is a weaker identity than the list of sites, because two changes
// within one rule can cancel — and a list would be no churnier here, since all
// of these are inline per-route declarations rather than a shared parameter.
// The count's real advantage is narrower than "less churn": it survives a path
// or parameter rename, which a list of sites does not. It is not expected to
// move at all in ordinary work.
var baseRuleSiteCounts = map[string]int{
	"pve-configid": 5,
}

// TestGuard_ExistingRuleSitesAcceptWhatTheCreateRuleMints closes the half of
// the create/addressing pairing that apischema cannot reach.
//
// A "-existing" rule is the loosened twin of a create rule: the create body
// carries the strict one, and the routes that ADDRESS what it created carry
// the twin. TestCreateConfigIDIsASubsetOfTheAddressingRule
// (apischema/catalogue_test.go) holds the PATTERN half of that pairing, and
// its own note records what it cannot see — the twin carries no length bound
// in the rule, so the bound lives in a MaxLength at each DECLARATION SITE, in
// this package. Raising the create ceiling without raising theirs re-opens the
// gap and nothing over there fails.
//
// The failure is the one ceph-pool-name's catalogue entry records, arrived at
// from the length side rather than the character side: a create body admits a
// name, Proxmox stores the object under it, and the route that would read or
// delete it refuses that same value in its own path parameter. The object is
// one this API created and cannot remove.
//
// # This is a ONE-SIDED check, and the direction matters
//
// It works in values rather than in numbers, because the create ceiling is
// spelled as a repetition count inside a regex and reading a bound back out of
// that leaves this file holding a second copy of it. The witnesses come from
// createRuleWitnesses, which grows a string one repeated rune at a time.
//
// That finds a LOWER BOUND on the create rule's ceiling, not the ceiling. A
// rule whose longest legal value needs a shape repetition cannot build — a
// segmented rule like "a/b/c", or one ending in a required character class —
// keeps its real maximum out of reach. So:
//
//   - A site narrower than the probed range FAILS here, and that is sound: the
//     create rule demonstrably accepts a value the site demonstrably refuses.
//   - A site narrower than the rule's TRUE ceiling but wider than the probed
//     range passes. This guard does not see it, and no amount of corroboration
//     inside this file would change that.
//
// An earlier revision tried to close the second case by requiring the probed
// range to equal the range of the rule's Accepts witnesses. That was worse
// than the gap: the two can sit below the true ceiling together, so it bought
// no soundness, and of the catalogue's base rules only pve-configid satisfies
// it — every other rule keeps its cap in a format function or in a per-site
// MaxLength, so the check would have fatalled on the next twin anyone added
// and been deleted as an obstacle. What remains of it is cheap and honest: the
// rule's own Accepts witnesses are added to the candidate set, so a boundary
// the catalogue HAS written down is covered even when repetition cannot reach
// it.
//
// # Known out of scope
//
// The pairing is found by the "-existing" naming convention, so a rule carried
// on BOTH a create body and an addressing path parameter under one name is not
// seen at all. ceph-pool-name is exactly that shape today: registry_ceph.go
// declares MaxLength 128 at both its sites — cephPoolNameParam addresses,
// createCephPoolParams [name] mints — and raising only the CREATE one re-opens
// this same bug with nothing failing. (Check that direction twice before
// editing it: pve-configid-existing's own Divergence note opens by recording
// that an earlier version of the same sentence had it backwards.)
//
// # What a site can do that this does and does not catch
//
// A MinLength or an Enum added at a site refuses a value the create rule mints
// and fails here. A TIGHTENED PATTERN does not, and the reason is structural
// rather than an oversight worth fixing here: Property has a single Pattern
// field, so a tighter pattern REPLACES the catalogued regex rather than adding
// to it. declaredRuleSites then matches the site against no catalogue entry
// and it leaves the walk altogether — the blind spot this file documents at
// length above. registry_rule_reference_ratchet_test.go is what reports that,
// by reading the declarations as source. Naming this guard as the one that
// catches it would send the next reader to the wrong file.
func TestGuard_ExistingRuleSitesAcceptWhatTheCreateRuleMints(t *testing.T) {
	t.Parallel()

	byName := make(map[string]apischema.RuleDoc)
	for _, d := range apischema.Catalogue() {
		byName[d.Name] = d
	}

	sitesByRule := make(map[string][]ruleSite)
	for _, s := range declaredRuleSites(t) {
		sitesByRule[s.rule] = append(sitesByRule[s.rule], s)
	}

	twins := 0
	for _, d := range apischema.Catalogue() {
		if strings.Contains(d.Name, "-existing") && !strings.HasSuffix(d.Name, "-existing") {
			t.Errorf("%q carries -existing somewhere other than the end of its name. The pairing "+
				"below matches by SUFFIX, so this rule is not seen at all and its addressing sites "+
				"are held to nothing. Rename it, or teach the pairing the new shape.", d.Name)
			continue
		}
		baseName, isVariant := strings.CutSuffix(d.Name, "-existing")
		if !isVariant {
			continue
		}
		// Counted here rather than after the base lookup: a twin whose
		// base is missing is a broken pair, not an absent one, and the
		// backstop at the end must not then report that no twin exists.
		twins++

		base, ok := byName[baseName]
		if !ok {
			t.Errorf("%s is catalogued but its create rule %q is not: a loosened twin with nothing "+
				"to be a twin OF cannot be held to anything", d.Name, baseName)
			continue
		}

		t.Run(d.Name, func(t *testing.T) {
			sites := sitesByRule[d.Name]
			if len(sites) == 0 {
				t.Fatalf("no declared parameter carries %s, so this pairing is unguarded. Either the "+
					"rule lost its last site — in which case the create rule's ceiling is now "+
					"answerable to nothing — or the sites regressed to an inline literal, which "+
					"ruleReferenceSites reports.", d.Name)
			}

			// The pairing is the API's invariant only if the CREATE routes
			// really carry the create rule. See baseRuleSiteCounts.
			want, pinned := baseRuleSiteCounts[baseName]
			if !pinned {
				t.Fatalf("%s has a -existing twin but no entry in baseRuleSiteCounts, so nothing "+
					"holds its create sites in place. Add one with the count this run reports: %d.",
					baseName, len(sitesByRule[baseName]))
			}
			if got := len(sitesByRule[baseName]); got != want {
				t.Fatalf("%[1]d declared parameters carry %[2]s, want %[3]d.\n"+
					"FEWER: either a create site had its Format swapped — which nothing else in "+
					"this package reports, and which leaves the object minted under one rule and "+
					"addressed under another — or a create route was legitimately deleted. For a "+
					"deletion, lower the pin; for a swap, restore the Format.\n"+
					"MORE: a new create route. Confirm it really should mint %[2]s values, then "+
					"raise the pin.\n"+
					"The catalogue's %[2]s entry states this count in prose as well, and nothing "+
					"points at it from here — update its Divergence note too.",
					got, baseName, want)
			}

			witnesses := createRuleWitnesses(t, base)

			// Keyed by the declared facets the routes share, so one bad
			// ceiling on a shared parameter reports once with its blast
			// radius rather than once per route. The SITE loop is the outer
			// one: a site refusing several witnesses is one failing site
			// with several failing witnesses, not several failing sites.
			type failure struct {
				refused []string
				routes  []string
			}
			failures := make(map[string]*failure)
			for _, s := range sites {
				var refused []string
				for _, w := range witnesses {
					if !siteAccepts(s.prop, w) {
						refused = append(refused, w)
					}
				}
				if len(refused) == 0 {
					continue
				}
				key := s.param + " (" + declaredNarrowingFacets(s.prop) + ")"
				f := failures[key]
				if f == nil {
					f = &failure{}
					failures[key] = f
				}
				f.routes = append(f.routes, s.String())
				for _, w := range refused {
					if !slices.Contains(f.refused, w) {
						f.refused = append(f.refused, w)
					}
				}
			}

			for _, key := range slices.Sorted(maps.Keys(failures)) {
				f := failures[key]
				t.Errorf("%s refuses %s, which %s accepts, across %d route(s) including %s.\n\n"+
					"An object created under such a name could be neither read nor deleted through "+
					"this API. Widen the site to cover what %s mints, or narrow %s to match the "+
					"site — but do not leave them apart.",
					key, describeWitnesses(f.refused), baseName, len(f.routes), f.routes[0],
					baseName, baseName)
			}
		})
	}

	if twins == 0 {
		t.Fatal("no catalogued rule ends in -existing, so this guard checked nothing. The naming " +
			"convention it keys on has changed; re-point it before deleting it.")
	}
}

// declaredNarrowingFacets renders the facets a site can narrow a rule with, so
// a failure message says what refused the value. Rendering only MaxLength made
// a MinLength narrowing print a MaxLength that was in fact correct, and left an
// Enum narrowing printing no cause at all.
func declaredNarrowingFacets(p apischema.Property) string {
	bound := func(v *int) string {
		if v == nil {
			return "unset"
		}
		return strconv.Itoa(*v)
	}
	s := "MinLength=" + bound(p.MinLength) + " MaxLength=" + bound(p.MaxLength)
	if len(p.Enum) > 0 {
		s += " Enum=" + strings.Join(p.Enum, "|")
	}
	return s
}

// describeWitnesses renders refused values for a failure message, shortening
// the long ones to their head and a rune count so a 128-character witness does
// not bury the sentence that explains it.
func describeWitnesses(ws []string) string {
	if len(ws) == 0 {
		// Unreachable while the caller skips sites that refused nothing,
		// and spelled out rather than returned as an empty string so a
		// later refactor cannot produce a failure message that names no
		// value at all.
		return "NOTHING (a failure was recorded with no refused witness — this is a bug in the guard)"
	}
	parts := make([]string, 0, len(ws))
	for _, w := range ws {
		const head = 24
		if n := utf8.RuneCountInString(w); n > head {
			r := []rune(w)
			parts = append(parts, strconv.Quote(string(r[:head]))+"… ("+strconv.Itoa(n)+" characters)")
			continue
		}
		parts = append(parts, strconv.Quote(w))
	}
	return strings.Join(parts, ", ")
}

// createRuleWitnesses returns values the create rule d demonstrably accepts,
// for the addressing sites to be held to.
//
// Two sources, deliberately different in kind:
//
//   - A grown string, one repeated rune at a time, from d's own shortest
//     Accepts entry: its first rune leads, its last rune fills. This reaches
//     the length boundary without this function knowing the rule's alphabet,
//     and it is the only source that finds a ceiling nobody wrote down — the
//     case this guard exists for, where someone raises the create bound and
//     leaves the addressing sites behind.
//   - Every Accepts witness the rule actually accepts. These cost nothing and
//     cover shapes repetition cannot build, which is the partial answer to the
//     lower-bound limitation the test's own comment sets out.
//
// The growth is bounded by a ceiling that fatals rather than silently
// returning the largest value tried: a rule with no maximum cannot bound its
// addressing sites, and passing quietly would be the wrong answer to it.
func createRuleWitnesses(t *testing.T, d apischema.RuleDoc) []string {
	t.Helper()

	var prop apischema.Property
	switch d.Kind {
	case apischema.KindFormat:
		prop = apischema.Property{Type: apischema.String, Format: d.Name}
	case apischema.KindPattern:
		prop = apischema.Property{Type: apischema.String, Pattern: d.Rule}
	default:
		t.Fatalf("%s is neither a format nor a pattern, so there is no way to apply it to a probe "+
			"value the way a declaration would", d.Name)
	}

	seed := ""
	for _, v := range d.Accepts {
		if v == "" {
			continue
		}
		if seed == "" || utf8.RuneCountInString(v) < utf8.RuneCountInString(seed) {
			seed = v
		}
	}
	if seed == "" {
		t.Fatalf("%s has no non-empty Accepts witness to build a length probe from", d.Name)
	}

	// Comfortably past any ceiling in the catalogue, so that reaching it
	// means the rule is effectively unbounded rather than merely large.
	const ceiling = 1024
	runes := []rune(seed)
	lead, fill := string(runes[0]), string(runes[len(runes)-1])

	var shortest, longest string
	for n := 1; n <= ceiling; n++ {
		v := lead + strings.Repeat(fill, n-1)
		if !siteAccepts(prop, v) {
			continue
		}
		if shortest == "" {
			shortest = v
		}
		longest = v
	}
	switch {
	case shortest == "":
		t.Fatalf("%s accepted no value built from its own shortest witness %q; the probe cannot "+
			"reach this rule's alphabet, so it has produced no evidence about this rule at all",
			d.Name, seed)
	case utf8.RuneCountInString(longest) == ceiling:
		t.Fatalf("%s still accepts a %d-character value, so it has no ceiling this probe can find. "+
			"An unbounded create rule cannot be held against a bounded addressing site: give the "+
			"rule a bound, or raise the probe ceiling if the real one is simply higher.",
			d.Name, ceiling)
	}

	out := []string{shortest, longest}
	for _, v := range d.Accepts {
		if siteAccepts(prop, v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
