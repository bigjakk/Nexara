package api

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// This file closes the blind spot the guards in registry_rule_catalogue_test.go
// name and cannot reach: a declaration that STOPS carrying a catalogued rule.
//
// Every guard there finds a site by matching the rule's TEXT — the compiled
// Pattern against the catalogue's regex. That works because at runtime
// apischema.Rule("x") and a pasted copy of the same regex are the same string,
// and it is exactly why a TIGHTENED copy is invisible: the new regex matches no
// catalogue entry, so the parameter does not fail any check, it LEAVES the walk.
// Every rule guard then has nothing to say about it.
//
// Measured, not hypothesised. Replacing pveObjectNameParam's
// Pattern: apischema.Rule("pve-object-id") with a hand-written ^[a-z][a-z0-9]*$
// and a MaxLength of 8 narrows every route that takes it — 33 Endpoint
// declarations in registry_firewall.go and registry_sdn.go, five ACME path
// parameters through acmeObjectNameParam (a bare call to it), and the two ACME
// body names that call it directly (createACMEAccountParams now overrides the
// Pattern with the catalogued sentinel and so is reached by the MaxLength half
// only; createACMEPluginParams still takes both) — and ALL SIX guards in
// registry_rule_catalogue_test.go stay green. (Their own note counts 20 routes;
// that is the routes whose accepted set demonstrably changed, not the reach.)
// Only unrelated domain tests notice, and only because they happen to exercise
// real values on those routes; a parameter without such a test slips through in
// silence. TestNoInlinePatternRestatesACataloguedRule compares for EQUALITY, so
// it catches the verbatim re-inline and is blind to the tightened one for the
// same reason.
//
// So this guard asks a question no value-matching check can: does the SOURCE
// still name the rule? It reads the declaration files as text, resolves what
// each Pattern reaches, and ratchets the answer.

// ruleReferenceSites is the closed set of declared parameters whose pattern
// comes from the catalogue BY NAME — every place the registry source spells
// apischema.Rule, whether at the parameter or through one of the package-level
// bindings that hold one (emptyOrNodeName, emptyOrUUID, pbsSafeIDPattern and
// the rest).
//
// Each line is "<file> <container> = <rule>" for a named Property declaration,
// and "<file> <container> [<parameter>] = <rule>" when the pattern sits under a
// Properties key. An element schema adds "[]" to the key, or stands alone as
// "<file> <container> []" when the array it belongs to has no key of its own.
//
// The list exists for the direction nothing derived can provide: it fails when
// a site STOPS naming its rule. A derived check cannot, because the evidence is
// gone — a tightened literal looks exactly like a parameter that never had a
// rule in the first place.
//
// # Why the identity is the container and not the route
//
// Counting routes was the obvious shape and is the wrong one. pve-object-id
// reaches 33 route declarations through pveObjectNameParam alone; a ratchet on
// the route count moves every time anyone adds or removes an endpoint, which is
// a ratchet people learn to bump without reading, and — worse — a bare total
// hides the defect outright, because a tightened literal at one site and a new
// apischema.Rule reference at another cancel.
//
// The container is stable under exactly the churn the route count is not.
// Adding a route that takes pveObjectNameParam("…") or emptyOrNodeName does not
// move this list at all, and neither does deleting one, because the shared
// declaration is what carries the rule. It moves only when a route declares its
// OWN inline rule reference, which 8 of the 37 entries below do — the ones whose
// container is a register…Endpoints function rather than a named parameter — and
// then the failure names which of the five things happened.
var ruleReferenceSites = []string{
	"registry_acme.go createACMEAccountParams = pve-object-id-or-empty",
	"registry_alerts.go alertFilterClusterParam = uuid-or-empty",
	"registry_alerts.go maintenanceWindowParams [node_id] = uuid-or-empty",
	"registry_backup.go backupJobParams [node] = node-name-or-empty",
	"registry_backup.go pbsDatastoreParam = pbs-safe-id",
	"registry_backup.go pbsJobIDParam = pbs-safe-id",
	"registry_backup.go pveBackupJobIDParam = pve-object-id",
	"registry_backup.go registerBackupEndpoints [datastore] = pbs-safe-id-or-empty",
	"registry_backup.go registerBackupEndpoints [store] = pbs-safe-id-or-empty",
	"registry_ceph.go cephPoolNameParam = ceph-pool-name",
	"registry_ceph.go createCephPoolParams [name] = ceph-pool-name",
	"registry_containers.go createCTParams [storage] = storage-id-or-empty",
	"registry_containers.go registerContainerEndpoints [size] = disk-resize",
	"registry_ha.go haConfigIDParam = pve-configid-existing",
	"registry_ldap.go ldapConfigParams [default_role_id] = uuid-or-empty",
	"registry_metric_servers.go metricServerIDParam = pve-object-id",
	"registry_migrations.go createMigrationParams [target_node] = node-name-or-empty",
	"registry_migrations.go createMigrationParams [target_storage] = storage-id-or-empty",
	"registry_networks.go ifaceParam = pve-object-id-colon",
	"registry_networks.go pveObjectNameParam = pve-object-id",
	"registry_nodes.go registerNodeEndpoints [target_node] = node-name-or-empty",
	"registry_oidc.go oidcConfigParams [default_role_id] = uuid-or-empty",
	"registry_pbs.go pbsAttachedClusterParam = uuid-or-empty",
	"registry_pools.go poolCreateIDParam = pve-poolid",
	"registry_pools.go poolIDParam = pve-poolid-segment",
	"registry_reports.go createScheduleParams [email_channel_id] = uuid-or-empty",
	"registry_rolling_update.go createRollingUpdateParams [notify_channel_id] = uuid-or-empty",
	"registry_sdn.go sdnControllerSettings [node] = node-name-or-empty",
	"registry_sdn.go sdnSubnetIDParam = pve-object-id-colon",
	"registry_virtio_win.go registerVirtioWinEndpoints [node] = node-name-or-empty",
	"registry_virtio_win.go registerVirtioWinEndpoints [storage] = storage-id-or-empty",
	"registry_vm_import.go registerVMImportEndpoints [node] = node-name-or-empty",
	"registry_vms.go cloneParams [storage] = storage-id-or-empty",
	"registry_vms.go cloneParams [target] = node-name-or-empty",
	"registry_vms.go optPoolID = pve-poolid-or-empty",
	"registry_vms.go registerVMEndpoints [size] = disk-resize",
	"registry_vms.go snapshotNameParam = pve-configid-existing",
}

// TestGuard_RuleReferenceSitesStillNameTheirRule is the ratchet.
//
// It fails in all five directions, and the message says which one — which
// matters more here than usual, because two of them look identical in the
// source and want opposite edits:
//
//   - RETIRED — the site still declares a pattern and no longer names a rule.
//     THIS IS THE DEFECT. Nothing else in the repo reports it.
//   - STRIPPED — the parameter is still declared and has no pattern at all.
//     The same defect widened instead of narrowed, and the one case where
//     the obvious reading of "the rule is gone" is the wrong one.
//   - RETARGETED — the site names a different rule than it did.
//   - GONE — the declaration itself is not in the source. Delete the line,
//     once you have ruled out a rename.
//   - NEW — a site names a rule and is not listed. Add the line.
//
// # What this cannot see
//
// It reads the source, so it answers "does the declaration name the rule",
// not "does the route end up applying it". The other direction is
// declaredRuleSites in registry_rule_catalogue_test.go, which walks the built
// registry; the two are deliberately different readings of the same fact and
// neither subsumes the other.
//
// FORMATS ARE NOT RATCHETED. A format is reached by name as a bare string —
// Format: "uuid" — so the same swap is possible there: replace
// Format: "node-name" with a tightened Pattern and the site leaves
// declaredRuleSites just as silently. It is left out because 58 declarations
// carry a format and 48 of those are "uuid", many written inline in a route
// declaration, so ratcheting them WOULD move on ordinary route churn — the
// failure mode the container identity above exists to avoid. The exposure is
// real and is stated here rather than covered.
//
// A BARE IDENTIFIER OR A DIRECT CALL, AND NOTHING ELSE. A pattern reaching a
// rule through an identifier is resolved when that identifier is a
// package-level binding of apischema.Rule("x") in these same files. Every other
// expression — a binding of a binding, a selector from another package, a
// helper call, an arithmetic one — reads as "names no rule" and simply never
// enters the list. It is not reported as a regression; it is not covered.
//
// That is not a hypothetical shape. Three declarations are built that way
// today, and they are mundane rather than exotic — the single device path and
// the comma-separated device list in registry_nodes.go, each a "+" tree
// assembled around devicePathBody, and the recipient element in
// registry_reports.go, which reaches handlers.EmailAddressPattern through a
// selector into another package.
//
// NONE OF THE THREE IS A LOSS HERE, and the reason is worth stating so that
// nobody "fixes" them into the list: each reaches a single definition a reader
// can open, and neither devicePathBody nor EmailAddressPattern is a catalogue
// entry, so there is no rule reference for this ratchet to have lost.
// registry_rule_catalogue_test.go exempts the same shape for the same reason
// and names devicePathBody outright. What the walk owes them is nothing; what
// it owes a CATALOGUED rule assembled this way is everything, because that one
// silently opts out of the catalogue.
//
// THERE WAS A FOURTH, AND IT WAS THAT SECOND KIND. createACMEAccountParams in
// registry_acme.go took the property pveObjectNameParam built and prefixed its
// Pattern with `^$|`, an assignment whose right-hand side was a "+" of a
// literal and the property's own field: a sentinel variant of a catalogued
// rule, derived AT THE DECLARATION rather than by the catalogue's own orEmpty.
// Tightening that line to `^[a-z][a-z0-9]{0,7}$` — which turns "", every
// uppercase name and every name past 8 characters into a 400 — passed the
// whole of ./internal/api/... Nothing next door fired, because the result
// matched no catalogue entry; nothing here did, because the expression was
// never a reference.
//
// The fix was not to teach this walk to fold a "+" tree. It was to catalogue
// the variant as pve-object-id-or-empty, which turned the expression back into
// an apischema.Rule call and put the site in the list above, where the same
// mutation now reports RETIRED. A hand-derived sentinel is the one instance of
// this shape with an answer cheaper than widening the resolver, and it should
// get that answer rather than this one.
//
// Resolution is also by NAME rather than by scope. A function-local variable
// shadowing one of those bindings would be read as the binding, and a var spec
// declaring several names at once is attributed wholly to the first. Neither
// exists today, and this walk would not notice one arriving.
//
// registry_*.go IS THE WHOLE SCOPE. That is not an assumption: every
// apischema.Rule reference in package api lives in one of these files today. A
// declaration moved to another file leaves the walk, and its site is reported
// GONE — true, but the reader has to notice the matching NEW line elsewhere to
// see that nothing was lost. GONE's message says so; it cannot tell the two
// apart on its own.
//
// It says nothing about whether the rule is the RIGHT one, or whether a sibling
// MaxLength or Enum narrows it. Those are the catalogue witnesses and
// TestGuard_RuleNarrowingSitesAreDeclared.
//
// One COST rather than a blind spot, stated so it is not a surprise: two
// rule-bearing Patterns at one identity fail outright and cannot be listed,
// because the ratchet could not then tell them apart. The fix is to give one a
// named Property — the idiom every shared parameter here already uses — so the
// guard pushes toward the shape the catalogue's own scope note asks for. No
// site is in that position today.
func TestGuard_RuleReferenceSitesStillNameTheirRule(t *testing.T) {
	t.Parallel()

	spellings, declared := registryRuleSpellings(t)

	// Anti-vacuity. A walk that found nothing would agree with an empty list
	// and report success for having looked at no source at all.
	if len(spellings) == 0 {
		t.Fatalf("no Pattern or Format field was found in any registry_*.go file; the walk is broken and " +
			"this guard would pass by examining nothing")
	}
	if len(ruleReferenceSites) == 0 {
		t.Fatalf("ruleReferenceSites is empty, so this ratchet holds nothing in place; populate it from " +
			"the NEW lines below")
	}

	// A line naming a rule the catalogue has no entry for still fails below —
	// usually as RETARGETED, because the source's real rule name is what `got`
	// carries and the two simply differ. This names the root cause instead, so
	// the reader is not left diffing two rule names to work out that one of
	// them no longer exists.
	//
	// The reachable causes are a catalogue RENAME with a stale line here, and
	// a typo. The third reading — a catalogue entry deleted while a
	// declaration still spells it — cannot reach this code at all, because
	// apischema.Rule panics for an unknown name at package init.
	catalogued := make(map[string]bool)
	for _, d := range apischema.Catalogue() {
		if d.Kind == apischema.KindPattern {
			catalogued[d.Name] = true
		}
	}

	wantBySite := make(map[string][]string)
	for _, entry := range ruleReferenceSites {
		site, rule, ok := strings.Cut(entry, " = ")
		if !ok {
			t.Fatalf("ruleReferenceSites entry %q is malformed: want \"<site> = <rule>\"", entry)
		}
		if !catalogued[rule] {
			t.Errorf("ruleReferenceSites names the rule %q, which the catalogue has no pattern entry for. "+
				"Either the entry was renamed in apischema/catalogue.go, or this line is a typo. The "+
				"site it names will report below too — as RETARGETED against whatever the source really "+
				"spells, unless that declaration has separately gone or lost its pattern — so fix this "+
				"line first and read the second finding afterwards.", rule)
		}
		wantBySite[site] = append(wantBySite[site], rule)
	}

	gotBySite := make(map[string][]string)
	byField := make(map[string][]patternSpelling)
	var examined int
	for _, s := range spellings {
		byField[s.site] = append(byField[s.site], s)
		if s.field != "Pattern" {
			continue
		}
		examined++
		if s.rule != "" {
			gotBySite[s.site] = append(gotBySite[s.site], s.rule)
		}
	}
	if examined == 0 {
		t.Fatalf("the walk found %d declared facets and not one Pattern among them; the classifier is "+
			"broken and this guard would pass by examining nothing", len(spellings))
	}

	if len(gotBySite) == 0 {
		t.Fatalf("the walk classified %d Patterns and resolved not one apischema.Rule reference among "+
			"them. That is a broken classifier, not %d simultaneous regressions — check ruleCallArg "+
			"against how the declarations spell the call (an import alias for apischema, or a rename of "+
			"Rule, breaks it silently). Reported here rather than as a RETIRED line per site, because "+
			"every one of those lines would confidently name the wrong cause.",
			examined, len(ruleReferenceSites))
	}

	// Two Patterns at one identity are two sites this ratchet cannot tell
	// apart: tighten one and add the other and the pair cancels, which is the
	// cancellation the container identity was chosen to avoid. Report it and
	// leave the site out of the diff below, where it would otherwise read as a
	// rule retargeted to itself.
	//
	// The count is over every Pattern spelling at the identity, not only the
	// ones that name a rule, because the cancelling pair need not be two rules.
	// A container that carries Pattern: apischema.Rule("x") and then overrides
	// it with p.Pattern = `^tight$` would leave got equal to want and pass in
	// silence. Nothing does that today — but build-from-a-helper-then-override
	// is already the idiom at poolCreateIDParam, optPoolID and
	// pbsAttachedClusterUpdateParam, each of which happens to write only ONE
	// Pattern of its own, so the second one is an edit away rather than a
	// rewrite away. Sites with no rule at all are left alone: they are not this
	// ratchet's business.
	ambiguous := make(map[string]bool)
	for _, site := range slices.Sorted(maps.Keys(byField)) {
		if len(gotBySite[site]) == 0 {
			continue
		}
		patterns := 0
		for _, s := range byField[site] {
			if s.field == "Pattern" {
				patterns++
			}
		}
		if patterns < 2 {
			continue
		}
		ambiguous[site] = true
		t.Errorf("%s declares %d separate Patterns (%d naming a catalogued rule), so this ratchet cannot "+
			"tell them apart — tightening one while adding another, or overriding a rule with a literal "+
			"further down, would cancel and report nothing. Give one of them a named Property, the way "+
			"pveObjectNameParam and emptyOrNodeName already do, so each site has an identity of its own.",
			site, patterns, len(gotBySite[site]))
	}

	for _, site := range slices.Sorted(maps.Keys(unionKeys(wantBySite, gotBySite))) {
		if ambiguous[site] {
			continue
		}
		want := slices.Sorted(slices.Values(wantBySite[site]))
		got := slices.Sorted(slices.Values(gotBySite[site]))
		if slices.Equal(want, got) {
			continue
		}

		switch {
		case len(want) == 0:
			for _, rule := range got {
				t.Errorf("NEW: %s names the catalogued rule %q and is not in ruleReferenceSites. A site "+
					"nothing records is a site that can stop naming its rule without anyone hearing about "+
					"it — add this line:\n\t%q,", site, rule, site+" = "+rule)
			}
		case len(got) == 0:
			t.Errorf("%s", retiredMessage(site, want, byField[site], declared[site]))
		default:
			t.Errorf("RETARGETED: %s named %s and now names %s. A rule swap changes what the route accepts "+
				"and what /api/v1/api-docs publishes beside it. Confirm the new rule is the one that route "+
				"wants — the catalogue entry states what each permits — and then update the line here.",
				site, quoteAll(want), quoteAll(got))
		}
	}
}

// retiredMessage explains a site that ruleReferenceSites records and the source
// no longer names a rule at, using what the source has THERE NOW to tell the
// defect apart from an ordinary deletion.
//
// stillDeclared is what separates the two cases that look identical from the
// facets alone: a parameter whose Pattern was DELETED declares no facet, and so
// does a parameter that was deleted outright. The first is a route that stopped
// validating its input and the second is a line to drop from the ratchet, so
// they cannot share a message.
func retiredMessage(site string, want []string, at []patternSpelling, stillDeclared bool) string {
	var pattern, format *patternSpelling
	for i := range at {
		switch at[i].field {
		case "Pattern":
			pattern = &at[i]
		case "Format":
			format = &at[i]
		}
	}

	switch {
	case pattern != nil:
		// Two shapes reach here and a reader needs to be sent to different
		// lines for each: the pattern was rewritten HERE, or the binding it
		// reaches through stopped holding a rule and every use site of that
		// binding is failing alongside this one.
		what := fmt.Sprintf("now spells its pattern out: %s (line %d)", pattern.spelt, pattern.line)
		if pattern.via != "" {
			// Every verb here is sequential on purpose. An indexed one would
			// make the NEXT verb anybody appends resolve to index 2 rather
			// than to a new trailing argument, which is a silent trap in a
			// message this likely to be extended.
			what = fmt.Sprintf("still reads it from %s (line %d), which no longer holds one — look at "+
				"where %s is declared, not at this line, and expect every other site reading it to be "+
				"failing too", pattern.via, pattern.line, pattern.via)
		}
		return fmt.Sprintf("RETIRED: %s carried the catalogued rule %s and %s.\n\nThis is the failure this "+
			"guard exists for, and it is the one failure nothing else reports. Every other rule guard "+
			"finds a site by MATCHING the catalogued regex, so a pattern that no longer equals one does "+
			"not fail them — the parameter leaves their walk and they fall silent. A pattern written here "+
			"is also free to be NARROWER than the rule, which turns requests every route sharing this "+
			"declaration used to accept into 400s, while /api/v1/api-docs goes on publishing the rule's "+
			"own \"permits\" text.\n\nIf the rule is still right, spell it apischema.Rule(%q) again. If "+
			"this route genuinely needs to be stricter, the narrowing belongs in a DECLARED facet beside "+
			"the rule — a MaxLength, a MinLength, an Enum — so the payload shows it; see "+
			"ruleNarrowingSites. If the rule itself is wrong, fix the catalogue entry, which is what every "+
			"other site carrying it is reading.",
			site, quoteAll(want), what, want[0])
	case format != nil:
		return fmt.Sprintf("RETIRED: %s carried the catalogued PATTERN rule %s and now declares Format: %s "+
			"(line %d). A format NORMALIZES the value and every registered one rejects the empty string, so "+
			"this is not the same check — it is why the -or-empty rules and the resize deltas are patterns "+
			"in the first place (see the catalogue's \"Two kinds of rule\"). If the swap is deliberate, "+
			"remove the line here and say why at the declaration.",
			site, quoteAll(want), format.spelt, format.line)
	case stillDeclared:
		// The declaration is still there and the facet is simply gone, so
		// the parameter now constrains nothing. It is the same defect as a
		// tightened literal pointed the other way — a WIDENING — and it must
		// not be confused with a deletion, because the advice differs
		// completely: one line is dropped from the ratchet, the other is a
		// route that stopped validating its input.
		return fmt.Sprintf("STRIPPED: %s carried the catalogued rule %s and now declares NO Pattern and no "+
			"Format, while the parameter itself is still declared. The route did not go away; its rule "+
			"did, so it accepts anything the type and any surviving MaxLength allow. Do NOT delete this "+
			"line — that would record the loss as intended. Put apischema.Rule(%q) back, or, if the "+
			"parameter really should be unconstrained, say why at the declaration and remove the line in "+
			"the same change.",
			site, quoteAll(want), want[0])
	default:
		return fmt.Sprintf("GONE: %s is in ruleReferenceSites carrying %s, and the declaration itself is "+
			"no longer in the source. If the parameter was deleted with its route, delete this line too.\n\n"+
			"Check that first, because two other things reach here. A RENAME leaves the site under its new "+
			"name — look for a NEW line above naming the same rule, and make both edits together. A rename "+
			"AND a tightening in one change leaves NO such line, and then deleting this one records a "+
			"narrowing as intended; if there is no NEW counterpart, go and read the declaration before "+
			"touching this list.",
			site, quoteAll(want))
	}
}

// patternSpelling is one declared Pattern or Format, as the SOURCE writes it —
// which is the whole point. The compiled registry cannot tell apischema.Rule("x")
// from a pasted copy of the same regex, and that distinction is the defect.
type patternSpelling struct {
	// site identifies the declaration without reference to a line number or a
	// route, so that it survives edits above it and route churn around it.
	site string
	// field is "Pattern" or "Format".
	field string
	// rule is the catalogued pattern rule this reaches BY NAME, and "" when it
	// reaches none — an inline regex, a constant from elsewhere, a format.
	rule string
	// via is the identifier the value reads through, when it is a bare one,
	// and "" otherwise. It is what lets the failure point at the binding
	// rather than at the use site when a whole binding stops holding a rule.
	via string
	// spelt is the expression as written, for the failure message.
	spelt string
	line  int
}

// registryRuleSpellings reads every declaration file and classifies each
// Pattern and Format by HOW the source reaches its value.
//
// An identifier is resolved one hop, through the package-level bindings that
// hold a rule — emptyOrNodeName, emptyOrUUID, pbsSafeIDPattern and the rest.
// That hop is not optional: six such bindings carry a rule to 23 of the 37
// sites in ruleReferenceSites, and recording only the binding would leave every
// one of its use sites free to be replaced by a literal without the guard
// noticing — the same defect one layer down, and the more likely one, because
// the site that gets pasted over is whichever one someone was editing.
// The second return is every site identity the registry DECLARES at all,
// facet or no facet, so that a rule which was stripped can be told from a
// parameter that was deleted. Those two look identical from the facets alone
// and need opposite advice.
func registryRuleSpellings(t *testing.T) ([]patternSpelling, map[string]bool) {
	t.Helper()

	// The declaration files only. No repo walk: stale agent worktrees live
	// under .claude/worktrees/ and a walk finds their copies of these same
	// files, which is how a repo-walking test comes to assert things about
	// source nobody is editing.
	names, err := filepath.Glob("registry_*.go")
	if err != nil {
		t.Fatalf("globbing the registry files: %v", err)
	}
	fset := token.NewFileSet()
	parsed := make(map[string]*ast.File, len(names))
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		parsed[name] = f
	}
	if len(parsed) < 10 {
		t.Fatalf("found %d registry declaration files, want the declaration set; the glob is wrong and this "+
			"test would pass by looking at nothing", len(parsed))
	}

	files := slices.Sorted(maps.Keys(parsed))

	// Pass 1: the package-level identifiers that hold a catalogued rule.
	bindings := make(map[string]string)
	for _, name := range files {
		for _, decl := range parsed[name].Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.VAR && gen.Tok != token.CONST) {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range value.Names {
					if i >= len(value.Values) {
						break
					}
					if rule, ok := ruleCallArg(value.Values[i]); ok {
						bindings[id.Name] = rule
					}
				}
			}
		}
	}

	// ruleOf answers with the catalogued rule an expression names, however it
	// reaches it.
	ruleOf := func(expr ast.Expr) string {
		if rule, ok := ruleCallArg(expr); ok {
			return rule
		}
		if id, ok := expr.(*ast.Ident); ok {
			return bindings[id.Name]
		}
		return ""
	}

	var out []patternSpelling
	declared := make(map[string]bool)
	for _, name := range files {
		for _, decl := range parsed[name].Decls {
			for _, c := range declContainers(decl) {
				prefix := name + " " + c.name
				spellings, keys := containerSpellings(fset, c.node, prefix, ruleOf)
				out = append(out, spellings...)
				// The container itself is a site identity — every named
				// Property declaration is one — and so is each parameter
				// key inside it.
				declared[prefix] = true
				for _, k := range keys {
					declared[k] = true
				}
			}
		}
	}
	return out, declared
}

// container is one top-level declaration, named so that a site inside it has an
// identity that does not move when the lines around it do.
type container struct {
	name string
	node ast.Node
}

// declContainers names a top-level declaration. A grouped var block yields one
// container per spec, so that the three -or-empty bindings in registry_vms.go
// are three names and not one.
func declContainers(decl ast.Decl) []container {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return []container{{name: d.Name.Name, node: d}}
	case *ast.GenDecl:
		out := make([]container, 0, len(d.Specs))
		for _, spec := range d.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) == 0 {
				continue
			}
			out = append(out, container{name: value.Names[0].Name, node: value})
		}
		return out
	default:
		return nil
	}
}

// containerSpellings finds every Pattern and Format written inside one
// top-level declaration, as a composite-literal field or as an assignment to
// one (poolCreateIDParam and optPoolID both set p.Pattern after building the
// property from a shared helper).
//
// It also returns every site identity the container DECLARES, facet or no
// facet, which is what lets a stripped rule be told from a deleted parameter.
// Both that set and the facet sites are spelled by siteOf, so the two cannot
// disagree about what a site is called — they did once, and the cost was that
// STRIPPED could never fire for an element schema.
//
// Within that, the set is deliberately generous: ANY string-keyed entry inside
// the container counts, so an unrelated string-keyed map would make a site read
// as declared. That errs toward the louder message, which is the safe direction
// — STRIPPED asks the reader to look, GONE tells them to delete a line.
func containerSpellings(fset *token.FileSet, node ast.Node, prefix string, ruleOf func(ast.Expr) string) ([]patternSpelling, []string) {
	var out []patternSpelling
	var keys []string
	var stack []ast.Node

	// siteOf is the ONE place a site identity is spelled, for the reason the
	// doc comment gives. parents is the chain ABOVE the facet or the entry.
	siteOf := func(parents []ast.Node) string {
		switch key := propertyKey(parents); {
		case key == elementOnly:
			// An element schema whose array has no parameter key of its own:
			// reportRecipientsParam is a named Property returning an Array,
			// so the Items schema hangs off the declaration directly.
			return prefix + " []"
		case key != "":
			return prefix + " [" + key + "]"
		default:
			return prefix
		}
	}

	record := func(field string, value ast.Expr, parents []ast.Node) {
		site := siteOf(parents)
		via := ""
		if id, ok := value.(*ast.Ident); ok {
			via = id.Name
		}
		out = append(out, patternSpelling{
			site:  site,
			field: field,
			rule:  ruleOf(value),
			via:   via,
			spelt: renderExpr(fset, value),
			line:  fset.Position(value.Pos()).Line,
		})
	}

	// EVERY path below must return true. ast.Inspect emits the closing nil
	// visit only when the callback returned true, so a single `return false`
	// — the natural "don't descend into this value" optimization — leaves a
	// node pushed and never popped, and the pop then underflows.
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		parents := stack[:len(stack)-1]

		switch e := n.(type) {
		case *ast.KeyValueExpr:
			// A parameter key, or an Items field: both name a site that
			// EXISTS whether or not it carries a facet. stack rather than
			// parents, because the identity is the one a facet INSIDE this
			// entry would be given, and that reading has to see this key.
			switch key := e.Key.(type) {
			case *ast.BasicLit:
				if key.Kind == token.STRING {
					keys = append(keys, siteOf(stack))
				}
			case *ast.Ident:
				if key.Name == "Items" {
					keys = append(keys, siteOf(stack))
				}
				if key.Name == "Pattern" || key.Name == "Format" {
					record(key.Name, e.Value, parents)
				}
			}
		case *ast.AssignStmt:
			if len(e.Lhs) != 1 || len(e.Rhs) != 1 {
				return true
			}
			sel, ok := e.Lhs[0].(*ast.SelectorExpr)
			if ok && (sel.Sel.Name == "Pattern" || sel.Sel.Name == "Format") {
				record(sel.Sel.Name, e.Rhs[0], parents)
			}
		}
		return true
	})
	return out, keys
}

// elementOnly is what propertyKey answers for an element schema that has no
// parameter key above it at all — the Items of an Array declared as a named
// Property, which reportRecipientsParam in registry_reports.go is. Without it
// the site would read "[[]]": a key of "[]" wrapped in the caller's brackets.
const elementOnly = "[]"

// propertyKey is the Properties key a facet sits under — "size" in
// apischema.Properties{"size": {Pattern: …}} — and "" when the property is not
// inside one, which is how every named Property declaration reads.
//
// An element schema is marked with a trailing "[]": Items is a Go field name
// rather than a parameter, so the nearest STRING key is still the parameter and
// the suffix is what keeps the element apart from the array itself. When there
// is no string key above it, the answer is that suffix alone — [elementOnly] —
// and the caller spells the site differently for it. That is not the exotic
// case: it is the ONLY Items-nested pattern in the registry today.
func propertyKey(parents []ast.Node) string {
	suffix := ""
	for i := len(parents) - 1; i >= 0; i-- {
		kv, ok := parents[i].(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		switch key := kv.Key.(type) {
		case *ast.BasicLit:
			if key.Kind != token.STRING {
				continue
			}
			name, err := strconv.Unquote(key.Value)
			if err != nil {
				continue
			}
			return name + suffix
		case *ast.Ident:
			if key.Name == "Items" {
				suffix = "[]"
			}
		}
	}
	return suffix
}

// ruleCallArg returns the rule name in an apischema.Rule("x") call.
func ruleCallArg(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Rule" {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "apischema" {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return name, true
}

// renderExpr prints an expression back as source, so that the failure message
// shows the reader the literal that replaced the rule rather than only its line.
func renderExpr(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return "<unprintable>"
	}
	return buf.String()
}

// quoteAll renders a site's rules for a message.
func quoteAll(rules []string) string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, strconv.Quote(r))
	}
	return strings.Join(out, ", ")
}

// unionKeys is the key set of both maps.
func unionKeys(a, b map[string][]string) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}
