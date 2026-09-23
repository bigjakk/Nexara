package changelog

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxHighlightsPerRelease bounds how many bullets one release contributes to
// the popup, so a pathological release body cannot blow up the payload.
//
// It was 8, which silently dropped real content: v1.8.0 shipped 12 bullets
// across Features, Bug Fixes and Refactoring and the popup showed the first 8
// with nothing to say four were missing. The dialog scrolls, so the cap exists
// only as a payload guard now, and ParseBody reports what it dropped so the UI
// can point at the full notes instead of quietly ending the list.
const maxHighlightsPerRelease = 30

// ParseBody extracts highlights from a GitHub release body using these rules:
//  1. If the body has a "## Highlights" (or "What's new") section, parse only
//     that section. Otherwise, parse all top-level bullets in the body, with
//     boilerplate sections (Chores, CI, Container Image, …) stripped.
//  2. Each bullet matches `- **Title** SEPARATOR Description` where SEPARATOR
//     is one of: " — ", " – ", ": ", " - ".
//  3. Bullets without a separator become title-only highlights.
//  4. Each bullet is typed by the heading it sat under, so the dialog can chip
//     it; a bullet marked breaking (`!` or `BREAKING:`) is typed breaking
//     whatever its heading, curated or none. Any other bullet with nothing to
//     classify by carries the empty ChangeType.
//  5. Cap at maxHighlightsPerRelease items.
//
// dropped is how many parseable bullets the cap discarded — 0 whenever the
// release fit, which is the normal case.
func ParseBody(body string) (highlights []Highlight, dropped int) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	sections := splitSections(body)

	// A curated Highlights section wins outright — it exists precisely so a
	// release author can say what belongs in the popup.
	//
	// Every selection below falls through when it yields nothing, and that is
	// the point: an entry with no highlights is DROPPED by service.go, so the
	// version leaves the feed entirely, getEntriesToShow can no longer find the
	// running version, and the popup then silently skips EVERY release the user
	// had not seen — not just this one. Rendering something slightly untidy
	// always beats that.
	for _, sec := range sections {
		if !isHighlightHeading(sec.heading) {
			continue
		}
		if curated := collectHighlights([]bodySection{{body: sec.body}}, true); len(curated) > 0 {
			return applyCap(curated)
		}
		break
	}

	kept := make([]bodySection, 0, len(sections))
	for _, sec := range sections {
		if skipHeadings[sec.heading] {
			continue
		}
		kept = append(kept, sec)
	}

	if out := collectHighlights(kept, true); len(out) > 0 {
		return applyCap(out)
	}
	// Every parseable bullet was a merge subject — a tag cut over a range that
	// contained nothing else. Show them rather than blanking the release.
	return applyCap(collectHighlights(kept, false))
}

// collectHighlights parses the bullets of each section, typing them by the
// section's heading. dropMerges filters out git's own merge subjects; the
// caller turns it off rather than return nothing at all.
func collectHighlights(sections []bodySection, dropMerges bool) []Highlight {
	out := make([]Highlight, 0, 16)
	for _, sec := range sections {
		changeType := headingTypes[sec.heading]
		for _, b := range extractBullets(sec.body) {
			if dropMerges && mergeCommitRe.MatchString(b) {
				continue
			}
			h := parseBullet(b)
			if h.Title == "" {
				continue
			}
			// Commit subjects are lower-case by convention, so the generated
			// bodies arrive as "- share the backup coverage computation …".
			// Capitalizing every title is what keeps a release from rendering
			// half sentence-case and half not.
			h.Title = capitalizeFirst(h.Title)
			h.Type = changeType
			// A `!` marker outranks the section: "Other Changes" has no type at
			// all, and even under Features "breaking" is the more useful chip.
			if breakingPrefixRe.MatchString(b) {
				h.Type = ChangeBreaking
			}
			out = append(out, h)
		}
	}
	return out
}

// applyCap bounds one release's contribution to the payload, reporting what it
// dropped so the dialog can point at the full notes instead of ending the list
// without explanation.
//
// Breaking changes are kept first, in their original positions. The cap is
// otherwise positional, and generate-release-notes.sh emits "Other Changes"
// LAST — which is exactly where a `!` subject lands, because the script's own
// prefix match cannot handle the `!`. A plain head-of-list cap would therefore
// drop the entries an operator most needs first: v1.11.0 already parses to 59
// bullets against a cap of 30.
func applyCap(all []Highlight) (highlights []Highlight, dropped int) {
	if len(all) <= maxHighlightsPerRelease {
		return all, 0
	}
	keep := make([]bool, len(all))
	slots := maxHighlightsPerRelease
	for i, h := range all {
		if h.Type == ChangeBreaking && slots > 0 {
			keep[i] = true
			slots--
		}
	}
	for i := range all {
		if slots == 0 {
			break
		}
		if !keep[i] {
			keep[i] = true
			slots--
		}
	}
	out := make([]Highlight, 0, maxHighlightsPerRelease)
	for i, h := range all {
		if keep[i] {
			out = append(out, h)
		}
	}
	return out, len(all) - len(out)
}

var (
	headingRe = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)
	bulletRe  = regexp.MustCompile(`^\s{0,3}[-*]\s+(.+)$`)
	boldRe    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	italicRe  = regexp.MustCompile(`(?:^|[^\*])\*([^\*]+)\*`)
	codeRe    = regexp.MustCompile("`([^`]+)`")
	linkRe    = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	prRefRe   = regexp.MustCompile(`\s*\(?(?:by @[\w-]+\s+in\s+)?#\d+\)?\s*$`)
	byRefRe   = regexp.MustCompile(`\s*by @[\w-]+\s*$`)

	// mergeCommitRe matches the subjects git writes for a merge. They reach a
	// release body whenever a merge lands without a conventional prefix, and
	// they name a branch rather than a change — "Merge pull request #15 from
	// owner/fix/inventory-write-amplification" is not something to tell an
	// operator about.
	//
	// It matches on git's full payload shape, not just the leading keyword:
	// "Merge branch protection rules into one policy editor" is a real change
	// and has to survive, and this filter runs over a curated Highlights
	// section too, which is precisely where such a bullet gets hand-written.
	// Case-sensitive for the same reason — git always capitalizes, prose may not.
	mergeCommitRe = regexp.MustCompile(
		`^Merge (?:` +
			// GitHub, then Gitea — this repo's own history carries both, and the
			// Gitea form is live in the published v1.11.0 body.
			`pull request (?:#\d+ from \S|'[^']*' \(#\d+\) from \S)` +
			// "branches" covers an octopus merge. Git quotes every ref it names
			// here, including a commit, which is why none of these is bare.
			`|(?:remote-tracking )?branch(?:es)? '[^']+'` +
			`|tag '[^']+'` +
			`|commit '[^']+'` +
			`)`)

	// conventionalPrefixRe matches release-please / conventional-commit style
	// bullet prefixes that should be stripped before rendering:
	//   **(scope)**:    feat(scope):    fix:    **feat(scope)**:
	conventionalPrefixRe = regexp.MustCompile(
		`^(?:` +
			`\*\*\([\w./-]+\)\*\*` +
			`|` +
			`\*\*(?i:feat|fix|chore|docs|refactor|test|perf|build|ci|style|revert|security|deps)(?:\([\w./-]+\))?!?\*\*` +
			`|` +
			`(?i:feat|fix|chore|docs|refactor|test|perf|build|ci|style|revert|security|deps)(?:\([\w./-]+\))?!?` +
			`|BREAKING` +
			`)\s*:\s*`)

	// breakingPrefixRe spots the `!` of a conventional breaking-change subject.
	// It is checked against the raw bullet, because conventionalPrefixRe strips
	// the marker along with the prefix.
	breakingPrefixRe = regexp.MustCompile(
		`^\*{0,2}(?:` +
			`(?i:feat|fix|chore|docs|refactor|test|perf|build|ci|style|revert|security|deps)(?:\([\w./-]+\))?!` +
			// CLAUDE.md mandates a literal `BREAKING:` prefix for a breaking
			// migration, so that spelling has to count too.
			`|BREAKING` +
			`)\*{0,2}\s*:`)

	// revertRe matches git's own revert subject. Its inner `: ` would otherwise
	// split the quoted original subject in half, leaving a title of `Revert "feat`.
	// "Reapply" is what git writes when reverting a revert, and the closing
	// quote is not anchored to end-of-line because a subject may carry trailing
	// prose ("… because it broke the collector").
	revertRe = regexp.MustCompile(`^(?:Revert|Reapply) ".*"`)
)

// skipHeadings is the set of release-note section headings whose bullets
// shouldn't surface in the popup — boilerplate, dependency bumps, build /
// CI churn, etc. Match is case-insensitive and against the trimmed heading.
var skipHeadings = map[string]bool{
	"chore":                  true,
	"chores":                 true,
	"container image":        true,
	"container":              true,
	"full changelog":         true,
	"internal":               true,
	"internal changes":       true,
	"dependencies":           true,
	"dependency updates":     true,
	"build":                  true,
	"ci":                     true,
	"continuous integration": true,
	"tests":                  true,
	"test":                   true,

	// "Other Changes" is deliberately NOT here. scripts/generate-release-notes.sh
	// matches `^<prefix>(\(scope\))?: `, which the `!` of a conventional
	// breaking-change subject defeats — so `feat(api)!:`, `chore(deps)!:` and
	// every `Revert "…"` land in that section rather than under Features. The
	// published v1.5.0 and v1.7.0 bodies both carry a `!` subject there.
	// Skipping the section would hide exactly the changes an operator most
	// needs; the merge subjects that also land there are dropped by
	// mergeCommitRe instead, which discriminates on the line rather than the
	// section.
}

// headingTypes maps a release-note section heading to the chip its bullets get.
// The canonical headings are the ones scripts/generate-release-notes.sh emits
// (Features, Bug Fixes, Refactoring, Performance, Documentation); the rest are
// the Keep a Changelog spellings a hand-written body is likely to reach for.
//
// An unmapped heading yields the empty ChangeType, so an unrecognized section
// renders without a chip rather than with a wrong one.
var headingTypes = map[string]ChangeType{
	"features":      ChangeNew,
	"feature":       ChangeNew,
	"added":         ChangeNew,
	"new":           ChangeNew,
	"new features":  ChangeNew,
	"bug fixes":     ChangeFix,
	"bug fix":       ChangeFix,
	"fixes":         ChangeFix,
	"fixed":         ChangeFix,
	"performance":   ChangeImproved,
	"refactoring":   ChangeImproved,
	"improvements":  ChangeImproved,
	"improved":      ChangeImproved,
	"changed":       ChangeImproved,
	"security":      ChangeSecurity,
	"documentation": ChangeDocs,
	"docs":          ChangeDocs,
}

// bodySection is a run of release-body lines under one heading. heading is ""
// for the lines before the first heading, and for a body with no headings at
// all — both of which are real shapes a release body arrives in.
type bodySection struct {
	heading string
	body    string
}

// splitSections breaks the body into its heading-delimited runs, in order and
// unfiltered — ParseBody decides which of them to parse. A body with no
// headings at all comes back as one unlabelled section.
func splitSections(body string) []bodySection {
	var sections []bodySection
	heading := ""
	var buf []string
	flush := func() {
		if len(buf) > 0 {
			sections = append(sections, bodySection{heading: heading, body: strings.Join(buf, "\n")})
			buf = nil
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if m := headingRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			flush()
			heading = strings.ToLower(strings.TrimSpace(m[1]))
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return sections
}

func isHighlightHeading(h string) bool {
	switch h {
	case "highlights", "what's new", "whats new", "what is new":
		return true
	}
	return false
}

// extractBullets returns the text of each top-level bullet, joining
// continuation lines (paragraph wraps within the same bullet).
func extractBullets(section string) []string {
	lines := strings.Split(section, "\n")
	var out []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			out = append(out, strings.TrimSpace(current.String()))
			current.Reset()
		}
	}
	for _, line := range lines {
		if m := bulletRe.FindStringSubmatch(line); m != nil {
			flush()
			current.WriteString(m[1])
			continue
		}
		if current.Len() == 0 {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		// Indented continuation — fold into the previous bullet.
		if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
			current.WriteString(" ")
			current.WriteString(trimmed)
			continue
		}
		// Non-indented non-bullet line ends the list.
		flush()
	}
	flush()
	return out
}

// Em-dash and en-dash come first because they're more distinctive than
// hyphen-with-spaces, which can also appear inside titles.
var separators = []string{" — ", " – ", ": ", " - "}

func parseBullet(text string) Highlight {
	text = strings.TrimSpace(stripTrailingRefs(strings.TrimSpace(text)))
	if text == "" {
		return Highlight{}
	}

	if revertRe.MatchString(text) {
		return Highlight{Title: stripMarkdown(text)}
	}

	// Strip release-please / conventional-commit prefixes BEFORE the bold-title
	// pathway, so `**(vms)**: live preview` becomes "Live preview" rather than
	// title=`(vms)`, description=`live preview`.
	if conventionalPrefixRe.MatchString(text) {
		rest := strings.TrimSpace(conventionalPrefixRe.ReplaceAllString(text, ""))
		if rest != "" {
			// Allow `**(scope)**: Title — Description` to still split on a
			// separator in the remainder.
			for _, sep := range separators {
				if idx := strings.Index(rest, sep); idx > 0 {
					return Highlight{
						Title:       stripMarkdown(strings.TrimSpace(rest[:idx])),
						Description: stripMarkdown(strings.TrimSpace(rest[idx+len(sep):])),
					}
				}
			}
			return Highlight{Title: stripMarkdown(rest)}
		}
	}

	if title, rest, ok := boldPrefix(text); ok {
		title = stripMarkdown(title)
		for _, sep := range separators {
			if strings.HasPrefix(rest, sep) {
				return Highlight{
					Title:       title,
					Description: stripMarkdown(strings.TrimSpace(rest[len(sep):])),
				}
			}
		}
		if rest == "" {
			return Highlight{Title: title}
		}
		// Bold prefix not followed by a separator — keep as title.
		combined := title + " " + stripMarkdown(strings.TrimSpace(rest))
		return Highlight{Title: strings.TrimSpace(combined)}
	}

	for _, sep := range separators {
		if idx := strings.Index(text, sep); idx > 0 {
			return Highlight{
				Title:       stripMarkdown(strings.TrimSpace(text[:idx])),
				Description: stripMarkdown(strings.TrimSpace(text[idx+len(sep):])),
			}
		}
	}

	return Highlight{Title: stripMarkdown(text)}
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if !unicode.IsLetter(r) {
		return s
	}
	if isIdentifierWord(s[size:]) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// isIdentifierWord reports whether the rest of a title's first word marks it as
// a name rather than prose: an internal capital (noVNC, iSCSI, vCenter, macOS)
// or an embedded digit or dot (i18n, xterm.js, v1.2.3). Capitalizing those
// mangles them, and noVNC and xterm.js are both in this project's own stack —
// "vCenter-style" is already in its commit history.
//
// Trailing sentence punctuation is stripped first so a one-word bullet like
// "done." still reads as prose and gets capitalized.
func isIdentifierWord(rest string) bool {
	if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimRight(rest, ".,:;!?")
	return strings.ContainsFunc(rest, func(r rune) bool {
		return unicode.IsUpper(r) || unicode.IsDigit(r) || r == '.'
	})
}

func boldPrefix(text string) (title, rest string, ok bool) {
	if !strings.HasPrefix(text, "**") {
		return "", "", false
	}
	end := strings.Index(text[2:], "**")
	if end < 0 {
		return "", "", false
	}
	title = strings.TrimSpace(text[2 : 2+end])
	rest = text[2+end+2:]
	return title, rest, true
}

// stripMarkdown removes inline markdown so the dialog renders clean text.
func stripMarkdown(text string) string {
	text = boldRe.ReplaceAllString(text, "$1")
	text = codeRe.ReplaceAllString(text, "$1")
	text = linkRe.ReplaceAllString(text, "$1")
	// Italic uses a leading lookbehind workaround — restore the captured prefix.
	text = italicRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := italicRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		// Preserve the character before the asterisk, drop the asterisks.
		prefix := strings.TrimSuffix(strings.TrimSuffix(match, "*"+sub[1]+"*"), "")
		return prefix + sub[1]
	})
	return strings.TrimSpace(text)
}

// stripTrailingRefs removes GitHub PR / contributor refs that auto-generated
// release notes append to bullets, e.g. " by @user in #123".
func stripTrailingRefs(text string) string {
	for {
		updated := prRefRe.ReplaceAllString(text, "")
		updated = byRefRe.ReplaceAllString(updated, "")
		updated = strings.TrimSpace(updated)
		if updated == text {
			return updated
		}
		text = updated
	}
}
