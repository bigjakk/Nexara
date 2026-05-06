package changelog

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxHighlightsPerRelease = 8

// ParseBody extracts highlights from a GitHub release body using these rules:
//  1. If the body has a "## Highlights" (or "What's new") section, parse only
//     that section. Otherwise, parse all top-level bullets in the body.
//  2. Each bullet matches `- **Title** SEPARATOR Description` where SEPARATOR
//     is one of: " — ", " – ", ": ", " - ".
//  3. Bullets without a separator become title-only highlights.
//  4. Hard-cap at maxHighlightsPerRelease items.
func ParseBody(body string) []Highlight {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	section := extractHighlightsSection(body)
	if section == "" {
		section = body
	}
	bullets := extractBullets(section)
	out := make([]Highlight, 0, len(bullets))
	for _, b := range bullets {
		h := parseBullet(b)
		if h.Title == "" {
			continue
		}
		out = append(out, h)
		if len(out) >= maxHighlightsPerRelease {
			break
		}
	}
	return out
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

	// conventionalPrefixRe matches release-please / conventional-commit style
	// bullet prefixes that should be stripped before rendering:
	//   **(scope)**:    feat(scope):    fix:    **feat(scope)**:
	conventionalPrefixRe = regexp.MustCompile(
		`^(?:` +
			`\*\*\([\w./-]+\)\*\*` +
			`|` +
			`\*\*(?i:feat|fix|chore|docs|refactor|test|perf|build|ci|style|revert|security|deps)(?:\([\w./-]+\))?\*\*` +
			`|` +
			`(?i:feat|fix|chore|docs|refactor|test|perf|build|ci|style|revert|security|deps)(?:\([\w./-]+\))?` +
			`)\s*:\s*`)
)

// skipHeadings is the set of release-note section headings whose bullets
// shouldn't surface in the popup — boilerplate, dependency bumps, build /
// CI churn, etc. Match is case-insensitive and against the trimmed heading.
var skipHeadings = map[string]bool{
	"chore":               true,
	"chores":              true,
	"container image":     true,
	"container":           true,
	"full changelog":      true,
	"internal":            true,
	"internal changes":    true,
	"dependencies":        true,
	"dependency updates":  true,
	"build":               true,
	"ci":                  true,
	"continuous integration": true,
	"tests":               true,
	"test":                true,
}

// extractHighlightsSection picks the relevant region of the release body:
//   - If there's an explicit "Highlights" / "What's New" heading, return ONLY
//     that section (lets release authors curate what shows in the popup).
//   - Otherwise, return the body with any boilerplate sections (Chores,
//     Container Image, Full changelog, etc.) stripped out.
//   - Returns "" if the body has no headings at all — caller falls back to
//     scanning the entire body.
func extractHighlightsSection(body string) string {
	lines := strings.Split(body, "\n")

	if hl := extractNamedSection(lines, isHighlightHeading); hl != "" {
		return hl
	}

	var out []string
	skipping := false
	sawHeading := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			sawHeading = true
			heading := strings.ToLower(strings.TrimSpace(m[1]))
			skipping = skipHeadings[heading]
			continue
		}
		if !skipping {
			out = append(out, line)
		}
	}
	if !sawHeading {
		return ""
	}
	return strings.Join(out, "\n")
}

func extractNamedSection(lines []string, match func(string) bool) string {
	inSection := false
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			heading := strings.ToLower(strings.TrimSpace(m[1]))
			if match(heading) {
				inSection = true
				continue
			}
			if inSection {
				return strings.Join(out, "\n")
			}
			continue
		}
		if inSection {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
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
						Title:       capitalizeFirst(stripMarkdown(strings.TrimSpace(rest[:idx]))),
						Description: stripMarkdown(strings.TrimSpace(rest[idx+len(sep):])),
					}
				}
			}
			return Highlight{Title: capitalizeFirst(stripMarkdown(rest))}
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
	return string(unicode.ToUpper(r)) + s[size:]
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
