package api

import (
	"go/ast"
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

// The catalogue (internal/api/apischema/catalogue.go) is only worth having if declarations go
// through it, and nothing stops the next author pasting `^[A-Za-z0-9][A-Za-z0-9._-]*$` into a
// new route: that is how these rules came to exist in three slightly different copies (the
// metric server id, the PVE backup job id and the firewall alias name each had one; the VM
// and container resize routes each had the delta pattern; emptyOrNodeName hand-transcribed
// the node-name format). So this file reads the declaration files as SOURCE: at runtime a
// literal and apischema.Rule("...") are the same string, and the difference is invisible.

// patternLiteral is one inline regex written at a declaration site.
type patternLiteral struct {
	file  string
	line  int
	regex string
}

// registryPatternLiterals returns every Pattern fully spelled out at the declaration, a string
// literal or a concatenation of them: `^[A-Za-z0-9]` + `[A-Za-z0-9.:_-]*$` is a second copy of a
// catalogued rule that reads as two harmless fragments. A Pattern reached through a named
// constant, apischema.Rule, or a concatenation with an identifier (`^` + devicePathBody +
// `$`) is not collected: those already have one definition.
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
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parsedSource(name)
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
				line:  parsedSourceFset.Position(kv.Value.Pos()).Line,
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
	files, err := filepath.Glob("registry_*.go")
	if err != nil {
		t.Fatalf("globbing the registry files: %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parsedSource(name)
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
		// A base whose sentinel variant is declared IS applied: orEmpty
		// builds the variant's regex out of the base's, so every site
		// carrying "<base>-or-empty" enforces the base's language plus the
		// empty string. pve-poolid is the case — POST /pools moved to
		// pve-poolid-new, and the base is now reached only through
		// pve-poolid-or-empty.
		if !used[d.Name] && !used[d.Name+"-or-empty"] {
			t.Errorf("catalogue pattern %q is declared nowhere: it documents a rule this API does not "+
				"apply. Either a declaration should be using it, or the entry should go.", d.Name)
		}
	}
}

// TestCataloguedRulesStillAcceptWhatTheirSitesSend runs each catalogued pattern's witnesses
// against EVERY declaration that carries it, as the router compiled it, not the bare regex and
// not one sampled site. A rule can arrive at a site with a MaxLength shorter than its own
// witnesses, or a Format that rewrites the value first, so the regex matches while the ROUTE
// turns the value away: the "do not change what an endpoint accepts" failure, invisible to a
// regex test. Sampling cannot see it either: pve-object-id has consumers at three MaxLengths
// (64, 100, 128), ceph-pool-name's create body is a different declaration from its path, and
// the five -or-empty rules had no sampled site at all.
func TestCataloguedRulesStillAcceptWhatTheirSitesSend(t *testing.T) {
	t.Parallel()

	byRule := make(map[string][]ruleSite)
	for _, site := range declaredRuleSites(t) {
		byRule[site.rule] = append(byRule[site.rule], site)
	}

	for _, d := range apischema.Catalogue() {
		sites := byRule[d.Name]
		rejects := d.Rejects
		// A pattern BASE with no site of its own whose sentinel variant has sites (pve-poolid, since POST
		// /pools moved to pve-poolid-new) is checked at the variant's sites: each enforces the base's
		// language plus "" (orEmpty builds it), so the base's accepts must pass there and its rejects,
		// bar "", must fail. Run here so it does not depend on derive() copying the witnesses
		// (TestOrEmptyRulesInheritTheirBaseWitnesses pins that).
		if variant := d.Name + "-or-empty"; len(sites) == 0 && d.Kind == apischema.KindPattern &&
			len(byRule[variant]) > 0 {
			sites = byRule[variant]
			rejects = slices.DeleteFunc(slices.Clone(d.Rejects), func(v string) bool { return v == "" })
		}
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()

			if len(sites) == 0 {
				// A FORMAT may have no site: it is registered whether or not a declaration reaches for it, and
				// five (email, ip, mac-addr, fingerprint-sha256, bwlimit) do not. A PATTERN with no site
				// documents a rule this API does not apply: TestEveryCataloguedPatternIsUsed reports the
				// source-level version, and reaching here means the rule is spelled apischema.Rule somewhere
				// that never becomes a declared parameter. (A base reached through its variant took the
				// variant's sites above.)
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
				for _, v := range rejects {
					if siteAccepts(site.prop, v) {
						t.Errorf("%s: %q is catalogued as rejected by %q, but this declaration accepts it",
							site, v, d.Name)
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

// narrowedByPublishedFacet reports whether the parameter's OWN published facets, everything
// /api/v1/api-docs renders BESIDE the rule, refuse this value on their own. It removes the rule
// (the Pattern at a Format site, the Format at a Pattern site) and keeps the rest. If that
// still refuses, a caller reading the payload can see it coming. If it ACCEPTS, the refusal
// came from the rule's application: a format NORMALIZES before bounds are checked, so a
// MinLength of 4 beside the disk-size format refuses "500G" (four characters typed, three
// after normalization), and the payload states a rule the route will not honour.
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

// ruleNarrowingSites is the closed set of declared parameters that refuse a value their own
// catalogued rule permits. Each is a DECLARED, PUBLISHED narrowing (the test below re-derives
// the set and checks the payload shows it); the list exists for the direction no derivation
// gives, a narrowing that DISAPPEARS. Both entries stop the payload contradicting itself:
// proxmox.ValidateSnapshotName caps snap_name at 40 where pve-configid permits 128, and deleting
// the two MaxLength lines leaves every other test green. Blind spots: a site that stops carrying
// the rule at all leaves declaredRuleSites' walk (registry_rule_reference_ratchet_test.go closes
// that by reading the source); a narrowing only the handler enforces leaves no trace, and scraping
// Descriptions was rejected because it would demand bounds that should not exist (SDN zone's
// 8-character cap is Proxmox's, deliberately unenforced).
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

// declaredRuleSites walks the built registry for every parameter, and array element schema, that
// carries a catalogued rule as a Format or a Pattern. It matches on the RULE TEXT, since
// apischema.Rule("x") and a pasted copy are the same string at runtime: a site regressed to a
// VERBATIM literal is still held to the rule (TestNoInlinePatternRestatesACataloguedRule reports
// the regression). Its blind spot is a TIGHTENED literal, which matches no entry and leaves the
// walk, and that test compares for equality too; ruleReferenceSites in
// registry_rule_reference_ratchet_test.go covers it from the source.
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

	s := sharedRouteStub(t)
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

// baseRuleSiteCounts ratchets how many declared parameters carry each CREATE rule that has an
// "-existing" twin: the ONLY thing that notices a create site losing its rule (formats are not
// ratcheted, a bare format swap narrows nothing, and the guard's own emptiness check is on the
// TWIN's sites). A swap costs less than it looks: pointing registry_ha.go's group Format at
// node-name stops the payload stating the pairing but strands nothing, since both HA ids are
// pve-configid upstream; the stranding version is the LENGTH one (PVE's $CONFIGID_RE states no
// maximum), which the witnesses below cover. A count survives a path or parameter rename, which
// a list of sites does not, and is not expected to move in ordinary work.
var baseRuleSiteCounts = map[string]int{
	"pve-configid": 7,
}

// TestGuard_ExistingRuleSitesAcceptWhatTheCreateRuleMints closes the half of the create/addressing
// pairing apischema cannot reach: the routes that ADDRESS what a create body minted carry the
// loosened "-existing" twin, whose length bound lives in a MaxLength at each declaration site, so
// raising the create ceiling without theirs mints an object this API cannot remove. It works in
// values (createRuleWitnesses grows a string), because reading the ceiling back out of the regex
// would keep a second copy. That finds a LOWER BOUND: a site narrower than the probed range fails
// soundly, one narrower than the true ceiling but wider than the probe passes. Out of scope: one
// rule on both a create body and an addressing parameter (ceph-pool-name, held by
// TestCephPoolNameParamAndBodyAgree); a TIGHTENED PATTERN, which leaves declaredRuleSites' walk and
// only registry_rule_reference_ratchet_test.go reports.
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

// createRuleWitnesses returns values the create rule d demonstrably accepts, for the addressing
// sites to be held to: a string grown one repeated rune at a time from d's shortest Accepts entry
// (first rune leads, last fills), which reaches a length ceiling nobody wrote down without
// knowing the alphabet; and every Accepts witness the rule accepts, covering shapes repetition
// cannot build. Growth is bounded by a ceiling that fatals: a rule with no maximum cannot bound
// its addressing sites, and passing quietly would be the wrong answer.
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
