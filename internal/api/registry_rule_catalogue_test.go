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
	for _, site := range declaredPatternSites(t) {
		byRule[site.rule] = append(byRule[site.rule], site)
	}

	for _, d := range apischema.Catalogue() {
		if d.Kind != apischema.KindPattern {
			continue
		}
		sites := byRule[d.Name]
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()

			if len(sites) == 0 {
				// TestEveryCataloguedPatternIsUsed reports the source-level
				// version of this; reaching here means the rule is spelled
				// apischema.Rule somewhere that never becomes a declared
				// parameter, which is the same finding one layer down.
				t.Fatalf("no declared parameter carries this rule, so its witnesses check nothing")
			}

			for _, site := range sites {
				// Requires and Alias are cross-field rules about OTHER
				// parameters; this asks only what the declaration admits for
				// this value, so they are cleared rather than satisfied.
				prop := site.prop
				prop.Requires = nil
				prop.Alias = ""
				props := apischema.Properties{"v": prop}

				for _, v := range d.Accepts {
					if _, err := props.Validate(map[string]any{"v": v}); err != nil {
						t.Errorf("%s: %q is catalogued as accepted, but this declaration rejects it: %v. "+
							"A bound or a format on the declaration is narrowing the rule without saying so.",
							site, v, err)
					}
				}
				for _, v := range d.Rejects {
					if _, err := props.Validate(map[string]any{"v": v}); err == nil {
						t.Errorf("%s: %q is catalogued as rejected, but this declaration accepts it",
							site, v)
					}
				}
			}
		})
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

// declaredPatternSites walks the built registry and returns every parameter
// — and every array element schema — whose Pattern is a catalogued rule.
//
// It matches on the RULE TEXT rather than on how the declaration spelled it,
// because at runtime apischema.Rule("x") and a pasted copy of the same regex
// are the same string. That is deliberate: a site that regressed to a literal
// is still held to the rule here, and the source-level guards above are what
// report the regression itself.
func declaredPatternSites(t *testing.T) []ruleSite {
	t.Helper()

	rules := make(map[string]string) // rule text -> rule name
	for _, d := range apischema.Catalogue() {
		if d.Kind == apischema.KindPattern {
			rules[d.Rule] = d.Name
		}
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
			if name, ok := rules[prop.Pattern]; ok {
				out = append(out, ruleSite{rule: name, method: e.Method, path: e.Path, param: param, prop: prop})
			}
			if prop.Items == nil {
				continue
			}
			// An element schema carries the same facets minus the ones
			// compileItems forbids, so it validates standalone.
			if name, ok := rules[prop.Items.Pattern]; ok {
				item := *prop.Items
				out = append(out, ruleSite{
					rule: name, method: e.Method, path: e.Path, param: param + "[]", prop: item,
				})
			}
		}
	}
	return out
}
