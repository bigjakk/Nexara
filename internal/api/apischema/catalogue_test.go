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
