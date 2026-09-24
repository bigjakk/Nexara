package apischema

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The catalogue is documentation that is allowed to be WRONG only if a test
// fails, which is the difference between it and a comment. These tests hold
// three lines:
//
//	completeness   every registered format has an entry, and every entry
//	               names a rule that exists
//	provenance     a Proxmox rule names the file and function it came from
//	               and quotes the upstream rule; a Nexara rule says so by
//	               claiming no upstream
//	truthfulness   every entry's witnesses are run through the rule AS THE
//	               REQUEST PATH ENFORCES IT, so a Permits line that stops
//	               being true stops the build
//
// The third is the one that matters. A catalogue whose prose can drift from
// the compiled rule is worse than no catalogue: it is a wrong answer that
// reads like a checked one.

// ruleProperty builds the property a declaration would write for d, so that
// a witness is validated through the same path a request takes — Validate,
// with the format registry or the pattern cache behind it — rather than
// through a regexp this test compiled for itself.
func ruleProperty(d RuleDoc) Property {
	p := Property{Type: String}
	switch d.Kind {
	case KindFormat:
		p.Format = d.Name
	case KindPattern:
		p.Pattern = d.Rule
	}
	return p
}

// accepted reports whether the rule as declared admits value.
func accepted(t *testing.T, d RuleDoc, value string) bool {
	t.Helper()
	props := Properties{"v": ruleProperty(d)}
	_, err := props.Validate(map[string]any{"v": value})
	if err != nil && !isValidationError(err) {
		t.Fatalf("%s: %q produced a SCHEMA error, not a rejection: %v", d.Name, value, err)
	}
	return err == nil
}

// isValidationError separates "the caller sent something bad" from "this
// declaration is malformed". Only the first is a rejection; the second
// means the catalogue entry could not be declared at all, which no witness
// result should be read from.
func isValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// TestEveryCataloguedFormatIsRegistered holds the one direction of
// completeness that is still a question.
//
// The other direction — a registered format with no entry — is not tested
// here because it is not testable: RegisterFormat refuses such a format at
// the moment of registration, from any file, in any init order, so it can
// never reach the registry to be found. That gate is pinned by
// TestRegisterFormatRequiresACatalogueEntry. What remains open is an entry
// whose format was never registered, which a declaration would name and
// then fail to compile its schema against.
func TestEveryCataloguedFormatIsRegistered(t *testing.T) {
	t.Parallel()

	for _, d := range Catalogue() {
		if d.Kind != KindFormat {
			continue
		}
		if _, ok := LookupFormat(d.Name); !ok {
			t.Errorf("catalogue lists format %q, which is not registered: a declaration naming it "+
				"would fail to compile its schema, and the entry documents a rule nothing applies", d.Name)
		}
	}
}

func TestCatalogueEntriesAreWellFormed(t *testing.T) {
	t.Parallel()

	for _, d := range Catalogue() {
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()

			switch d.Kind {
			case KindFormat, KindPattern:
			default:
				t.Fatalf("kind = %q, want %q or %q", d.Kind, KindFormat, KindPattern)
			}
			if strings.TrimSpace(d.Permits) == "" {
				t.Error("no Permits line: the entry names a rule without stating what it allows, which is " +
					"the thing the catalogue exists to fix")
			}
			if strings.TrimSpace(d.Rule) == "" {
				t.Error("no Rule: a reader has nothing to check the Permits line against")
			}
			if d.RuleIsRegex {
				if _, err := regexp.Compile(d.Rule); err != nil {
					t.Errorf("Rule is marked a regex but does not compile: %v", err)
				}
			}

			switch d.Origin {
			case OriginProxmox:
				// An upstream reference has to be specific enough to open.
				// "pve-common" alone sends the next reader back to grepping
				// three repositories, which is the cost this field exists
				// to remove.
				if fields := strings.Fields(d.Upstream); len(fields) < 3 {
					t.Errorf("Upstream = %q, want at least <repo> <path> <function>", d.Upstream)
				}
				if strings.TrimSpace(d.UpstreamRule) == "" {
					t.Error("no UpstreamRule: re-verifying against Proxmox then means reading the Perl " +
						"again rather than diffing two lines")
				}
			case OriginNexara:
				if d.Upstream != "" || d.UpstreamRule != "" {
					t.Errorf("origin is %q but names upstream %q / %q; a Nexara rule has no upstream to "+
						"check it against and must not imply one", OriginNexara, d.Upstream, d.UpstreamRule)
				}
			default:
				t.Fatalf("origin = %q, want %q or %q", d.Origin, OriginProxmox, OriginNexara)
			}

			// Witnesses are what keep Permits honest. An entry with none
			// is prose again.
			if len(d.Accepts) == 0 {
				t.Error("no Accepts witness: nothing holds the Permits line to what the rule admits")
			}
			if len(d.Rejects) == 0 {
				t.Error("no Rejects witness: a rule that turns nothing away would pass every accept test")
			}
			if dup := firstDuplicate(append(slices.Clone(d.Accepts), d.Rejects...)); dup != "" {
				t.Errorf("witness %q appears twice, and possibly on both sides", dup)
			}
		})
	}
}

// TestCatalogueWitnessesMatchTheEnforcedRule is the anti-drift test. Every
// witness goes through Validate, which is the path a request takes, so an
// entry cannot describe a rule the code does not implement.
func TestCatalogueWitnessesMatchTheEnforcedRule(t *testing.T) {
	t.Parallel()

	for _, d := range Catalogue() {
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()

			for _, v := range d.Accepts {
				if !accepted(t, d, v) {
					t.Errorf("%q is listed as accepted but the rule refuses it; either the rule changed "+
						"or the Permits line was never true: %s", v, d.Permits)
				}
			}
			for _, v := range d.Rejects {
				if accepted(t, d, v) {
					t.Errorf("%q is listed as rejected but the rule admits it; either the rule was "+
						"widened or the Permits line overstates it: %s", v, d.Permits)
				}
			}
		})
	}
}

// TestCatalogueRegexRulesAreTheCompiledOnes pins the four formats whose
// regex format.go builds out of the catalogue. It is the check that the
// wiring is real rather than parallel: if someone writes the regex out
// again in format.go, the catalogue becomes a second copy and this fails.
func TestCatalogueRegexRulesAreTheCompiledOnes(t *testing.T) {
	t.Parallel()

	for name, compiled := range map[string]*regexp.Regexp{
		"storage-id":   storageIDRe,
		"node-name":    nodeNameRe,
		"pve-configid": configIDRe,
		"uuid":         uuidRe,
	} {
		d, ok := LookupRule(name)
		if !ok {
			t.Errorf("%s has no catalogue entry", name)
			continue
		}
		if compiled.String() != d.Rule {
			t.Errorf("%s: format.go enforces %q but the catalogue states %q — a reader consulting the "+
				"catalogue would be told the wrong rule", name, compiled.String(), d.Rule)
		}
	}
}

// orEmptyCorpus is a deliberately MIXED bag: every witness in the
// catalogue, whatever rule it belongs to, plus the substring-search traps
// the -or-empty derivation exists to survive.
//
// The point is that it is not derived from any one rule. An -or-empty rule
// is built as `^$|` + its base, so comparing the two over the base's own
// witnesses would agree by construction and prove nothing. Over a corpus
// that includes "..foo" and "foo\nbar", a derivation that dropped an anchor
// — which is the documented way this goes wrong, because Go's regexp is a
// substring search — disagrees immediately.
func orEmptyCorpus() []string {
	out := []string{
		"", "..foo", "foo/..", "../etc/passwd", "a\nb", "\nstore01", "store01\n",
		"x..", "..", ".", "%2e%2e", " ", "a b",
	}
	for _, d := range Catalogue() {
		out = append(out, d.Accepts...)
		out = append(out, d.Rejects...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// TestOrEmptyRulesAcceptTheirBasePlusNothingElse holds the pairing that
// this catalogue was built to stop drifting. emptyOrNodeName and the
// node-name format were two hand-written copies of one rule: correcting one
// left the other wrong, silently, on every route that carried it.
func TestOrEmptyRulesAcceptTheirBasePlusNothingElse(t *testing.T) {
	t.Parallel()

	for _, d := range Catalogue() {
		baseName, isVariant := strings.CutSuffix(d.Name, "-or-empty")
		if !isVariant {
			continue
		}
		t.Run(d.Name, func(t *testing.T) {
			t.Parallel()

			base, ok := LookupRule(baseName)
			if !ok {
				t.Fatalf("derives from %q, which is not catalogued", baseName)
			}
			if !base.RuleIsRegex {
				t.Fatalf("base %q does not state its rule as a regex, so the pair cannot be compared", baseName)
			}
			baseRe := regexp.MustCompile(base.Rule)
			variantRe := regexp.MustCompile(d.Rule)

			for _, v := range orEmptyCorpus() {
				want := v == "" || baseRe.MatchString(v)
				if got := variantRe.MatchString(v); got != want {
					t.Errorf("%q: %s matches = %v, but %s matches = %v (empty is %v). The variant must "+
						"admit its base's language and the empty string, and nothing else — an "+
						"unanchored branch lets a traversal segment through from the middle of the value",
						v, d.Name, got, baseName, baseRe.MatchString(v), v == "")
				}
			}
		})
	}
}

// TestOrEmptyRulesInheritTheirBaseWitnesses pins what derive() copies into
// a sentinel variant: every accept witness of its base, plus the empty
// string, and every reject witness the base's REGEX refuses (carryRejects).
//
// The test above holds the variant's RULE to its base's; this holds its
// WITNESSES. Without it, a derive() that stopped copying them — keeping
// only "" — would leave every catalogue test green while each variant's
// sites were checked against nothing but the empty string, and the base
// witnesses internal/api runs at those sites would be the only check left.
func TestOrEmptyRulesInheritTheirBaseWitnesses(t *testing.T) {
	t.Parallel()

	variants := 0
	for _, d := range Catalogue() {
		baseName, isVariant := strings.CutSuffix(d.Name, "-or-empty")
		if !isVariant {
			continue
		}
		variants++
		base, ok := LookupRule(baseName)
		if !ok {
			t.Errorf("%s derives from %q, which is not catalogued", d.Name, baseName)
			continue
		}
		baseRe := regexp.MustCompile(base.Rule)

		wantAccepts := append([]string{""}, base.Accepts...)
		var wantRejects []string
		for _, v := range base.Rejects {
			if v != "" && !baseRe.MatchString(v) {
				wantRejects = append(wantRejects, v)
			}
		}
		if !slices.Equal(slices.Sorted(slices.Values(d.Accepts)), slices.Sorted(slices.Values(wantAccepts))) {
			t.Errorf("%s accepts witnesses %q, want its base's %q plus the empty string",
				d.Name, d.Accepts, base.Accepts)
		}
		if !slices.Equal(slices.Sorted(slices.Values(d.Rejects)), slices.Sorted(slices.Values(wantRejects))) {
			t.Errorf("%s rejects witnesses %q, want every reject of %s that its regex refuses: %q",
				d.Name, d.Rejects, baseName, wantRejects)
		}
	}
	if variants == 0 {
		t.Fatal("the catalogue has no -or-empty rule, so this test checked nothing")
	}
}

// TestCreateConfigIDIsASubsetOfTheAddressingRule holds the pairing that
// decides pve-configid's ceiling.
//
// pve-configid is the CREATE rule; pve-configid-existing is what the routes
// that address an existing object carry. A create rule that admits a name
// the addressing rule refuses produces an object this API cannot then read
// or delete — the same failure ceph-pool-name's entry records, arrived at
// from the other side. So create has to stay a SUBSET of addressing, and
// this asserts it over every witness in the catalogue rather than over
// pve-configid's own, which would agree by construction.
//
// What this canNOT see is the length half, and that is worth saying plainly
// rather than leaving a reader to assume it is covered: pve-configid-existing
// carries no length bound in its rule: its cap is a MaxLength: 128 at each
// declaration (snapshotNameParam in internal/api/registry_vms.go,
// haConfigIDParam in internal/api/registry_ha.go), which this package cannot
// reach. pve-configid's own ceiling was raised from 40 to exactly 128 to
// match those, so RAISING IT FURTHER WITHOUT RAISING THEM re-opens the gap
// and nothing here will fail. That check belongs in internal/api, where both
// halves are visible.
func TestCreateConfigIDIsASubsetOfTheAddressingRule(t *testing.T) {
	t.Parallel()

	create, ok := LookupRule("pve-configid")
	if !ok {
		t.Fatal("pve-configid is not catalogued")
	}
	existing, ok := LookupRule("pve-configid-existing")
	if !ok {
		t.Fatal("pve-configid-existing is not catalogued")
	}
	existingRe := regexp.MustCompile(existing.Rule)

	for _, v := range orEmptyCorpus() {
		if accepted(t, create, v) && !existingRe.MatchString(v) {
			t.Errorf("pve-configid accepts %q but pve-configid-existing refuses it: an object created "+
				"under that name could not then be addressed, so it would be neither readable nor "+
				"deletable through this API", v)
		}
	}
}

// TestPoolIDNewIsPoolIDMinusTheTwoDotSegments holds pve-poolid-new to what
// its entry says it is: pve-poolid with exactly "." and ".." taken out,
// nothing more and nothing less.
//
// The oracles are the two OTHER entries, never this one's own witnesses,
// which would agree with it by construction. pve-poolid, as a regex, says
// what the language is; path-safe-dotted-name, as a regex, says what the
// carve-out is for a single segment — pve-poolid-new's first three branches
// claim to be exactly its language, and the second assertion holds them to
// that. pve-poolid-new itself goes through Validate, the path a request
// takes.
//
// The corpus has two halves. Every string of up to five characters over an
// alphabet that reaches each CHARACTER boundary the regexes draw: the dot, a
// letter, a digit, dash and underscore (both in the charset, and neither a
// dot), the slash that separates segments, and a space, which is outside the
// charset. Five characters cannot reach the DEPTH boundary — the shortest
// four-level id, "a/b/c/d", is seven — so the second half joins one to five
// segments, each drawn from ".", "..", "a", "a.b" and "", which reaches the
// three-level limit from both sides, dot segments and empty segments
// included.
func TestPoolIDNewIsPoolIDMinusTheTwoDotSegments(t *testing.T) {
	t.Parallel()

	newRule, ok := LookupRule("pve-poolid-new")
	if !ok {
		t.Fatal("pve-poolid-new is not catalogued")
	}
	lookupRe := func(name string) *regexp.Regexp {
		d, ok := LookupRule(name)
		if !ok || !d.RuleIsRegex {
			t.Fatalf("%s is not catalogued as a regex rule", name)
		}
		return regexp.MustCompile(d.Rule)
	}
	poolID, segment := lookupRe("pve-poolid"), lookupRe("path-safe-dotted-name")

	corpus := []string{""}
	for frontier := []string{""}; len(frontier[0]) < 5; {
		var next []string
		for _, prefix := range frontier {
			for _, c := range []string{".", "a", "0", "-", "_", "/", " "} {
				next = append(next, prefix+c)
			}
		}
		corpus = append(corpus, next...)
		frontier = next
	}
	segments := []string{".", "..", "a", "a.b", ""}
	for depth, joined := 1, []string{""}; depth <= 5; depth++ {
		var next []string
		for _, prefix := range joined {
			for _, seg := range segments {
				if depth == 1 {
					next = append(next, seg)
				} else {
					next = append(next, prefix+"/"+seg)
				}
			}
		}
		corpus = append(corpus, next...)
		joined = next
	}
	slices.Sort(corpus)
	corpus = slices.Compact(corpus)

	var admitted, refusedDots int
	for _, v := range corpus {
		got := accepted(t, newRule, v)
		if want := poolID.MatchString(v) && v != "." && v != ".."; got != want {
			t.Errorf("%q: pve-poolid-new accepts = %v, want %v (pve-poolid accepts = %v)",
				v, got, want, poolID.MatchString(v))
		}
		if !strings.Contains(v, "/") && got != segment.MatchString(v) {
			t.Errorf("%q: pve-poolid-new accepts = %v but path-safe-dotted-name accepts = %v; for a "+
				"single segment the two must be one language", v, got, segment.MatchString(v))
		}
		if got {
			admitted++
		}
		if poolID.MatchString(v) && !got {
			refusedDots++
		}
	}
	// Anti-vacuity, both ways: a corpus the rule admitted none of, or one in
	// which pve-poolid never differed from it, would pass every comparison.
	if admitted < 1000 || refusedDots != 2 {
		t.Errorf("admitted %d of %d strings and refused %d that pve-poolid admits; want well over a "+
			"thousand and exactly the two dot segments", admitted, len(corpus), refusedDots)
	}
}

// TestRuleRefusesAFormat pins the one misuse the accessor can catch: a
// declaration reaching a normalizing rule through Pattern, which would
// check the shape and silently drop the normalization the format exists
// for.
func TestRuleRefusesAFormat(t *testing.T) {
	t.Parallel()

	mustPanic(t, "declare it as Format", func() { _ = Rule("uuid") })
	mustPanic(t, "unknown pattern rule", func() { _ = Rule("no-such-rule") })

	if got := Rule("pve-object-id"); got == "" {
		t.Error("Rule returned an empty rule for a catalogued pattern")
	}
}

func TestLookupRuleAndCatalogueAreCopies(t *testing.T) {
	t.Parallel()

	if _, ok := LookupRule("no-such-rule"); ok {
		t.Error("LookupRule reported an uncatalogued name as present")
	}

	d, ok := LookupRule("pve-object-id")
	if !ok {
		t.Fatal("pve-object-id is not catalogued")
	}
	d.Accepts[0] = "mutated"
	again, _ := LookupRule("pve-object-id")
	if again.Accepts[0] == "mutated" {
		t.Error("LookupRule handed back the catalogue's own slice, so a caller can corrupt it for " +
			"every later reader")
	}

	all := Catalogue()
	all[0].Rejects[0] = "mutated"
	fresh, ok := LookupRule(all[0].Name)
	if !ok {
		t.Fatalf("%q is not catalogued", all[0].Name)
	}
	if slices.Contains(fresh.Rejects, "mutated") {
		t.Error("Catalogue handed back the catalogue's own slices, so a caller that sorts or edits a " +
			"rendered listing corrupts the rules themselves")
	}
	if !slices.IsSortedFunc(all, func(a, b RuleDoc) int { return strings.Compare(a.Name, b.Name) }) {
		t.Error("Catalogue is not ordered by name, so a rendered listing would shuffle between builds")
	}
}

func firstDuplicate(values []string) string {
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		if seen[v] {
			return v
		}
		seen[v] = true
	}
	return ""
}
