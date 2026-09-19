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
// THE BIG ONE: a site that stops carrying the rule at all. declaredRuleSites
// finds a pattern site by matching the declared regex against the
// catalogue's, so REPLACING apischema.Rule("x") with a tighter literal does
// not register as a narrowing — it removes the parameter from the walk, and
// every guard in this file then has nothing to say about it. Swapping
// pveObjectNameParam's Rule("pve-object-id") for `^[a-z][a-z0-9]*$` with a
// MaxLength of 8 narrows 20 routes across ACME, firewall and SDN, and all
// six rule guards stay green; only unrelated domain tests notice, and only
// by accident. TestNoInlinePatternRestatesACataloguedRule catches the
// VERBATIM re-inline of a catalogued regex, which is the copy-paste case,
// and is blind to a tightened one for the same reason: it compares for
// equality. Closing this needs a source-level check that a parameter which
// USED to spell apischema.Rule still does — a ratchet over declaration
// sites, not over rules.
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
// was replaced instead of re-inlined. See the note on ruleNarrowingSites.
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
