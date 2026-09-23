package changelog

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseBody_HighlightsSection(t *testing.T) {
	body := `## Highlights

- **Live VNC console preview on VM detail page** — See a live thumbnail of the guest console without opening the full console window.
- **Fix: stale-VM pruning scoped to synced nodes** — Collector no longer drops VMs from a node that failed to sync.

## Full changelog
- ignore me
- ignore me too`

	got, _ := ParseBody(body)
	if len(got) != 2 {
		t.Fatalf("expected 2 highlights, got %d: %#v", len(got), got)
	}
	if got[0].Title != "Live VNC console preview on VM detail page" {
		t.Errorf("title 0: %q", got[0].Title)
	}
	if got[0].Description != "See a live thumbnail of the guest console without opening the full console window." {
		t.Errorf("desc 0: %q", got[0].Description)
	}
	if got[1].Title != "Fix: stale-VM pruning scoped to synced nodes" {
		t.Errorf("title 1: %q", got[1].Title)
	}
}

func TestParseBody_NoHighlightsSection_FallsBackToBullets(t *testing.T) {
	body := `Some intro text.

- **First** — Description one.
- **Second**: Description two.
- Plain bullet without bold

End of release.`

	got, _ := ParseBody(body)
	if len(got) != 3 {
		t.Fatalf("expected 3 highlights, got %d: %#v", len(got), got)
	}
	if got[0].Title != "First" || got[0].Description != "Description one." {
		t.Errorf("bullet 0: %#v", got[0])
	}
	if got[1].Title != "Second" || got[1].Description != "Description two." {
		t.Errorf("bullet 1: %#v", got[1])
	}
	if got[2].Title != "Plain bullet without bold" || got[2].Description != "" {
		t.Errorf("bullet 2: %#v", got[2])
	}
}

func TestParseBody_AcceptsMultipleSeparators(t *testing.T) {
	body := `- **Em** — em-dash desc
- **En** – en-dash desc
- **Colon**: colon desc
- **Hyphen** - hyphen desc`

	got, _ := ParseBody(body)
	if len(got) != 4 {
		t.Fatalf("expected 4 highlights, got %d", len(got))
	}
	for i, expectDesc := range []string{"em-dash desc", "en-dash desc", "colon desc", "hyphen desc"} {
		if got[i].Description != expectDesc {
			t.Errorf("bullet %d: desc = %q want %q", i, got[i].Description, expectDesc)
		}
	}
}

func TestParseBody_StripsMarkdown(t *testing.T) {
	body := `- **Title with ` + "`code`" + `** — Description with [link](https://example.com) and **bold**.`

	got, _ := ParseBody(body)
	if len(got) != 1 {
		t.Fatalf("expected 1 highlight, got %d", len(got))
	}
	if got[0].Title != "Title with code" {
		t.Errorf("title = %q", got[0].Title)
	}
	if got[0].Description != "Description with link and bold." {
		t.Errorf("desc = %q", got[0].Description)
	}
}

func TestParseBody_StripsContributorRefs(t *testing.T) {
	body := `- **Live VNC console preview** — See a live thumbnail by @bigjakk in #123
- **Another fix** — Description here #456`

	got, _ := ParseBody(body)
	if len(got) != 2 {
		t.Fatalf("expected 2 highlights, got %d", len(got))
	}
	if got[0].Description != "See a live thumbnail" {
		t.Errorf("desc 0 should strip contributor ref: %q", got[0].Description)
	}
	if got[1].Description != "Description here" {
		t.Errorf("desc 1 should strip PR ref: %q", got[1].Description)
	}
}

func TestParseBody_CapsAtMaxHighlights(t *testing.T) {
	const over = maxHighlightsPerRelease + 7

	var body string
	for i := 0; i < over; i++ {
		body += "- **Item** — Description\n"
	}
	got, more := ParseBody(body)

	if len(got) != maxHighlightsPerRelease {
		t.Fatalf("returned %d highlights, want the cap of %d", len(got), maxHighlightsPerRelease)
	}
	// The count is the point: truncating is acceptable, doing it silently is
	// not — the dialog uses this to say how many are missing.
	if more != over-maxHighlightsPerRelease {
		t.Errorf("more = %d, want %d — a truncated release must report what it dropped",
			more, over-maxHighlightsPerRelease)
	}
}

// TestParseBody_KeepsRealisticReleaseIntact is the regression lock for the bug
// this cap actually caused. The v1.8.0 body carried 12 bullets across Features,
// Bug Fixes and Refactoring; the old cap of 8 returned the first 8 and gave the
// UI no way to know four were gone, so the popup ended mid-list looking
// complete.
func TestParseBody_KeepsRealisticReleaseIntact(t *testing.T) {
	body := `## Features
- **(vms)**: give VM import the full Create-VM option set (minus hardware)
- **(vms)**: flesh out VM import — browser upload, URL UX, deduped sources
- **(vms)**: import VMs from ESXi/OVA/disk into Proxmox

## Bug Fixes
- **(vms)**: move import VM-name field into the Identity section
- **(vms)**: show normalized vCPU topology in the import wizard
- **(vms)**: fold imported sockets into cores (VMware vCPU topology)
- **(vms)**: default imported guests to x86-64-v2-AES CPU (Win11 boots)
- **(vms)**: place imported disks on SATA so OVMF guests boot
- **(vms)**: create an EFI vars disk for OVMF guests on import
- **(vms)**: strip storage prefix from import-metadata volume
- **(dev)**: don't redirect edge-proxied or WebSocket traffic in dev Caddy

## Refactoring
- **(vms)**: fold review nits into VM import wizard

## Container Image
- ghcr.io/bigjakk/nexara:1.8.0
`

	got, more := ParseBody(body)

	if len(got) != 12 {
		t.Errorf("returned %d highlights, want all 12 — the cap must not eat real releases", len(got))
	}
	if more != 0 {
		t.Errorf("more = %d, want 0 — nothing should have been dropped", more)
	}
	// Container Image is boilerplate and stays out, so 12 rather than 13.
	for _, h := range got {
		if strings.Contains(h.Title, "ghcr.io") {
			t.Errorf("boilerplate section leaked into the highlights: %q", h.Title)
		}
	}
}

func TestParseBody_EmptyOrNonsense(t *testing.T) {
	cases := []string{
		"",
		"Just a paragraph with no bullets.",
		"## Other heading\nNothing useful here.",
	}
	for _, c := range cases {
		if got, _ := ParseBody(c); len(got) != 0 {
			t.Errorf("ParseBody(%q) = %#v, want empty", c, got)
		}
	}
}

func TestParseBody_MultilineBulletJoinsContinuations(t *testing.T) {
	body := `- **Long title** — A description that wraps
  across multiple lines but stays in
  the same bullet.
- **Second** — Short.`

	got, _ := ParseBody(body)
	if len(got) != 2 {
		t.Fatalf("expected 2 highlights, got %d: %#v", len(got), got)
	}
	want := "A description that wraps across multiple lines but stays in the same bullet."
	if got[0].Description != want {
		t.Errorf("desc 0: %q\nwant %q", got[0].Description, want)
	}
}

func TestParseBody_BoldOnlyTitle(t *testing.T) {
	body := `- **Title only, no separator**
- **Another** — with desc`

	got, _ := ParseBody(body)
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].Title != "Title only, no separator" || got[0].Description != "" {
		t.Errorf("title-only bullet: %#v", got[0])
	}
}

func TestParseBody_HandlesWhatsNewHeading(t *testing.T) {
	body := `## What's New

- **Feature** — Description.
- **Fix** — Bug fix.`

	got, _ := ParseBody(body)
	if len(got) != 2 {
		t.Fatalf("expected 2 highlights, got %d", len(got))
	}
}

func TestParseBody_StripsConventionalCommitPrefix(t *testing.T) {
	cases := []struct {
		body  string
		title string
	}{
		{"- **(vms)**: live VNC console preview on VM detail page",
			"Live VNC console preview on VM detail page"},
		{"- **(collector)**: scope stale-VM pruning to nodes that synced successfully",
			"Scope stale-VM pruning to nodes that synced successfully"},
		{"- feat(vms): live VNC console preview",
			"Live VNC console preview"},
		{"- feat: simplify the dashboard",
			"Simplify the dashboard"},
		{"- fix: prevent crash on null clusters",
			"Prevent crash on null clusters"},
		{"- **fix(api)**: handle empty body",
			"Handle empty body"},
	}
	for _, c := range cases {
		got, _ := ParseBody(c.body)
		if len(got) != 1 {
			t.Errorf("body %q: got %d highlights, want 1", c.body, len(got))
			continue
		}
		if got[0].Title != c.title {
			t.Errorf("body %q: title=%q want %q", c.body, got[0].Title, c.title)
		}
		if got[0].Description != "" {
			t.Errorf("body %q: unexpected description %q", c.body, got[0].Description)
		}
	}
}

func TestParseBody_ConventionalPrefixWithDescription(t *testing.T) {
	body := `- **(vms)**: Live VNC preview — Shows a thumbnail without opening the full console.`
	got, _ := ParseBody(body)
	if len(got) != 1 {
		t.Fatalf("expected 1 highlight, got %d", len(got))
	}
	if got[0].Title != "Live VNC preview" {
		t.Errorf("title=%q", got[0].Title)
	}
	if got[0].Description != "Shows a thumbnail without opening the full console." {
		t.Errorf("desc=%q", got[0].Description)
	}
}

func TestParseBody_DoesNotStripPlainBoldTitles(t *testing.T) {
	// A bare bold title that's not in conventional-commit form (mixed case,
	// multi-word, no parens) must keep working as a title-with-description.
	body := `- **Live VNC console preview** — See a live thumbnail.`
	got, _ := ParseBody(body)
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].Title != "Live VNC console preview" || got[0].Description != "See a live thumbnail." {
		t.Errorf("got %#v", got[0])
	}
}

func TestParseBody_SkipsBoilerplateSections(t *testing.T) {
	body := `## Features

- **(ui)**: new dashboard widget

## Chores

- bump go to 1.24

## Container Image

` + "```bash\ndocker pull foo\n```"

	got, _ := ParseBody(body)
	if len(got) != 1 {
		t.Fatalf("expected 1 highlight, got %d: %#v", len(got), got)
	}
	if got[0].Title != "New dashboard widget" {
		t.Errorf("title=%q", got[0].Title)
	}
}

func TestParseBody_ReleasePleaseStyleEnd2End(t *testing.T) {
	// A real release-please-shaped body (matches Nexara v0.2.33).
	body := `## Features

- **(vms)**: live VNC console preview on VM detail page

## Bug Fixes

- **(collector)**: scope stale-VM pruning to nodes that synced successfully

## Chores

- bump to v0.2.33

---

**Full changelog**: ` + "`v0.2.32...v0.2.33`" + `

## Container Image

` + "```bash\ndocker pull ghcr.io/bigjakk/nexara:0.2.33\n```"

	got, _ := ParseBody(body)
	if len(got) != 2 {
		t.Fatalf("expected 2 highlights, got %d: %#v", len(got), got)
	}
	if got[0].Title != "Live VNC console preview on VM detail page" {
		t.Errorf("highlight 0 title=%q", got[0].Title)
	}
	if got[1].Title != "Scope stale-VM pruning to nodes that synced successfully" {
		t.Errorf("highlight 1 title=%q", got[1].Title)
	}
}

func TestParseBody_TypesBulletsBySection(t *testing.T) {
	body := `## Features
- add a thing

## Bug Fixes
- stop dropping a thing

## Refactoring
- move a thing

## Performance
- batch a thing

## Documentation
- describe a thing

## Security
- lock a thing down`

	want := []ChangeType{
		ChangeNew, ChangeFix, ChangeImproved, ChangeImproved, ChangeDocs, ChangeSecurity,
	}

	got, _ := ParseBody(body)
	if len(got) != len(want) {
		t.Fatalf("expected %d highlights, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i].Type != w {
			t.Errorf("highlight %d (%q): type = %q, want %q", i, got[i].Title, got[i].Type, w)
		}
	}
}

// A curated section carries no SECTION type: its heading says nothing about
// what kind of change a bullet is. A bullet marked breaking is the exception —
// the mark is on the bullet, not the heading, so it is typed breaking there too.
func TestParseBody_CuratedHighlightsCarryNoSectionType(t *testing.T) {
	body := `## Highlights

- **Live VNC console preview** — See a thumbnail without opening the console.
- feat(api)!: one collection envelope

## Bug Fixes

- stop dropping a thing`

	got, _ := ParseBody(body)
	if len(got) != 2 {
		t.Fatalf("expected 2 highlights, got %d: %#v", len(got), got)
	}
	if got[0].Type != "" {
		t.Errorf("type = %q, want empty — a non-breaking curated highlight must render without a chip", got[0].Type)
	}
	if got[1].Type != ChangeBreaking {
		t.Errorf("type = %q, want %q — a breaking mark is on the bullet, so a curated section keeps it",
			got[1].Type, ChangeBreaking)
	}
}

// A body with no headings at all has no section to type a bullet by, and the
// same exception holds: a breaking bullet is still typed breaking.
func TestParseBody_HeadinglessBodyTypesOnlyBreakingBullets(t *testing.T) {
	got, _ := ParseBody("- add a thing\n- BREAKING: rekey alert rules")
	if len(got) != 2 {
		t.Fatalf("expected 2 highlights, got %d: %#v", len(got), got)
	}
	if got[0].Type != "" {
		t.Errorf("type = %q, want empty — a heading-less bullet has nothing to be typed by", got[0].Type)
	}
	if got[1].Type != ChangeBreaking {
		t.Errorf("type = %q, want %q", got[1].Type, ChangeBreaking)
	}
}

func TestParseBody_UnknownHeadingKeepsBulletButNotAType(t *testing.T) {
	body := `## Notes For Operators

- rotate the cluster token after upgrading`

	got, _ := ParseBody(body)
	if len(got) != 1 {
		t.Fatalf("expected 1 highlight, got %d: %#v", len(got), got)
	}
	if got[0].Type != "" {
		t.Errorf("type = %q, want empty — an unmapped heading must not be guessed at", got[0].Type)
	}
}

// generate-release-notes.sh matches `^<prefix>(\(scope\))?: `, which the `!` of
// a conventional breaking-change subject defeats — so every BREAKING change
// lands under "Other Changes" rather than Features. The published v1.5.0 and
// v1.7.0 bodies both carry one there. Skipping that section as boilerplate
// would hide exactly the changes an operator most needs to read.
func TestParseBody_KeepsBreakingChangesUnderOtherChanges(t *testing.T) {
	body := `## Features
- add a thing

## Other Changes
- chore(deps)!: upgrade Fiber v2 to v3
- Revert "feat: database seed export/import for fresh deployments"
- Merge pull request #15 from owner/fix/inventory-write-amplification`

	got, _ := ParseBody(body)
	if len(got) != 3 {
		t.Fatalf("expected 3 highlights, got %d: %#v", len(got), got)
	}
	// The `!` prefix is stripped like any other, and the marker becomes the chip
	// rather than being lost with it.
	if got[1].Title != "Upgrade Fiber v2 to v3" || got[1].Type != ChangeBreaking {
		t.Errorf("breaking change = %#v, want title %q typed %q",
			got[1], "Upgrade Fiber v2 to v3", ChangeBreaking)
	}
	// A revert stays one title; its inner ": " must not split it at `Revert "feat`.
	if got[2].Title != `Revert "feat: database seed export/import for fresh deployments"` {
		t.Errorf("revert = %#v", got[2])
	}
	if got[2].Description != "" {
		t.Errorf("revert gained a description: %q", got[2].Description)
	}
}

func TestParseBody_DropsMergeCommits(t *testing.T) {
	body := `## Features
- Merge pull request #15 from owner/fix/inventory-write-amplification
- Merge pull request 'Feat/guest tools' (#8) from feat/guest-tools into master
- Merge branch 'release/1.2' into master
- Merge branches 'a' and 'b'
- Merge remote-tracking branch 'origin/master'
- Merge tag 'v1.2.3'
- Merge commit 'a1b2c3d4e5f6789'
- add a thing`

	got, _ := ParseBody(body)
	if len(got) != 1 {
		t.Fatalf("expected 1 highlight, got %d: %#v", len(got), got)
	}
	if got[0].Title != "Add a thing" {
		t.Errorf("highlight 0 = %q", got[0].Title)
	}
}

// Every bullet here opens on a merge keyword and none is a merge subject. They
// are what stops the filter being widened into something that eats real
// changes: the first needs the payload shape (not just the keyword), the second
// needs case-sensitivity, and the third needs the leading anchor.
func TestParseBody_KeepsRealChangesThatOpenLikeMerges(t *testing.T) {
	body := `## Features
- Merge branch protection rules into one policy editor
- merge branch 'stale' cleanup into the collector sweep
- Document how to Merge branch 'legacy' into master safely`

	got, _ := ParseBody(body)
	if len(got) != 3 {
		t.Fatalf("expected all 3 to survive, got %d: %#v", len(got), got)
	}
}

// generate-release-notes.sh emits "- <subject>" for any commit without a scope,
// and commit subjects are lower-case by convention. Before every path
// capitalized, a release rendered half sentence-case and half not.
func TestParseBody_CapitalizesUnprefixedTitles(t *testing.T) {
	body := `## Refactoring
- share the backup coverage computation between the coverage page and reports`

	got, _ := ParseBody(body)
	if len(got) != 1 {
		t.Fatalf("expected 1 highlight, got %d: %#v", len(got), got)
	}
	if got[0].Title != "Share the backup coverage computation between the coverage page and reports" {
		t.Errorf("title = %q — an unprefixed bullet must be capitalized like a prefixed one", got[0].Title)
	}
}

// The chip the dialog renders is keyed off these exact strings, and an empty
// type has to be absent from the payload so the frontend's optional field is
// genuinely optional. Renaming a constant without touching
// frontend/src/lib/changelog.ts would otherwise break the chips silently.
func TestHighlight_TypeWireFormat(t *testing.T) {
	want := map[ChangeType]string{
		ChangeNew:      `{"title":"T","type":"new"}`,
		ChangeImproved: `{"title":"T","type":"improved"}`,
		ChangeFix:      `{"title":"T","type":"fix"}`,
		ChangeSecurity: `{"title":"T","type":"security"}`,
		ChangeDocs:     `{"title":"T","type":"docs"}`,
		ChangeBreaking: `{"title":"T","type":"breaking"}`,
		"":             `{"title":"T"}`,
	}
	// Driven off allChangeTypes so a new constant with no pinned wire string
	// fails here instead of shipping unpinned — which holds only because
	// TestAllChangeTypesCoversEveryConstantAndEveryHeading keeps
	// allChangeTypes complete.
	for _, ct := range append([]ChangeType{""}, allChangeTypes...) {
		if _, ok := want[ct]; !ok {
			t.Errorf("ChangeType %q has no pinned wire format", ct)
		}
	}
	for ct, wantJSON := range want {
		b, err := json.Marshal(Highlight{Title: "T", Type: ct})
		if err != nil {
			t.Fatalf("marshal %q: %v", ct, err)
		}
		if string(b) != wantJSON {
			t.Errorf("Highlight{Type: %q} = %s, want %s", ct, b, wantJSON)
		}
	}
}

// An unusable curated section must not blank the release. service.go drops an
// entry with no highlights, so the version leaves the feed entirely,
// getEntriesToShow can no longer find the running version, and the popup then
// skips every release the user had not seen — not just this one.
func TestParseBody_UnusableHighlightsSectionFallsThrough(t *testing.T) {
	cases := map[string]string{
		"empty": `## Highlights

## Features

- add a thing

## Bug Fixes

- stop dropping a thing`,
		"prose only": `## Highlights

This release is all about reporting.

## Features

- add a thing

## Bug Fixes

- stop dropping a thing`,
		"only a merge subject": `## Highlights

- Merge branch 'release/1.2' into master

## Features

- add a thing

## Bug Fixes

- stop dropping a thing`,
	}
	for name, body := range cases {
		got, _ := ParseBody(body)
		if len(got) != 2 {
			t.Errorf("%s: got %d highlights, want 2 — an unusable Highlights section must fall through: %#v",
				name, len(got), got)
			continue
		}
		if got[0].Type != ChangeNew || got[1].Type != ChangeFix {
			t.Errorf("%s: fell through but lost the section types: %#v", name, got)
		}
	}
}

// A first word whose casing is load-bearing must survive. noVNC and xterm.js
// are both in this project's stack and "vCenter-style" is already in its
// history, so blanket capitalization would mangle real release notes.
func TestParseBody_LeavesIdentifierTitlesAlone(t *testing.T) {
	for body, want := range map[string]string{
		"- noVNC preview on the VM detail page": "noVNC preview on the VM detail page",
		"- xterm.js console resize handling":    "xterm.js console resize handling",
		"- iSCSI portals in the storage wizard": "iSCSI portals in the storage wizard",
		"- vCenter-style folders for guests":    "vCenter-style folders for guests",
		"- i18n coverage for the alerts page":   "i18n coverage for the alerts page",
		"- share the backup coverage report":    "Share the backup coverage report",
		"- done.":                               "Done.",
	} {
		got, _ := ParseBody(body)
		if len(got) != 1 {
			t.Errorf("%q: got %d highlights, want 1", body, len(got))
			continue
		}
		if got[0].Title != want {
			t.Errorf("%q: title = %q, want %q", body, got[0].Title, want)
		}
	}
}

// A tag cut over a range that contained only merges must still render. Dropping
// them all leaves zero highlights, and service.go then removes the entry from
// the feed entirely — which suppresses the popup for every unseen release, not
// just this one.
func TestParseBody_MergeOnlyReleaseStillRenders(t *testing.T) {
	body := `## Other Changes

- Merge pull request #16 from owner/fix/thing
- Merge branch 'release/1.2' into master

## Container Image

- ghcr.io/example/app:1.13.2`

	got, _ := ParseBody(body)
	if len(got) == 0 {
		t.Fatal("a merge-only release parsed to nothing — the entry would vanish from the feed")
	}
	if len(got) != 2 {
		t.Errorf("got %d highlights, want both merge subjects back: %#v", len(got), got)
	}
	for _, h := range got {
		if strings.Contains(h.Title, "ghcr.io") {
			t.Errorf("boilerplate leaked in during the fallback: %q", h.Title)
		}
	}
}

// generate-release-notes.sh emits "Other Changes" last and that is where every
// `!` subject lands, so a positional cap would drop the breaking change first.
// v1.11.0 already parses to 59 bullets against a cap of 30.
func TestApplyCap_KeepsBreakingChanges(t *testing.T) {
	var body strings.Builder
	body.WriteString("## Features\n")
	for i := 0; i < maxHighlightsPerRelease+5; i++ {
		fmt.Fprintf(&body, "- add filler thing %d\n", i)
	}
	body.WriteString("\n## Other Changes\n- feat(api)!: rekey the alert rules\n")

	got, dropped := ParseBody(body.String())
	if len(got) != maxHighlightsPerRelease {
		t.Fatalf("got %d highlights, want the cap of %d", len(got), maxHighlightsPerRelease)
	}
	if dropped != 6 {
		t.Errorf("dropped = %d, want 6", dropped)
	}
	last := got[len(got)-1]
	if last.Type != ChangeBreaking {
		t.Fatalf("the breaking change was capped away; last kept is %#v", last)
	}
	if last.Title != "Rekey the alert rules" {
		t.Errorf("breaking title = %q", last.Title)
	}
	// It is kept in place, not hoisted — the list still reads in release order.
	if got[0].Title != "Add filler thing 0" {
		t.Errorf("cap reordered the list; first is %q", got[0].Title)
	}
}

func TestParseBody_RevertAndBreakingSpellings(t *testing.T) {
	for body, want := range map[string]struct {
		title      string
		changeType ChangeType
	}{
		`- Revert "feat: database seed export/import"`: {
			`Revert "feat: database seed export/import"`, "",
		},
		`- Reapply "feat: database seed export/import"`: {
			`Reapply "feat: database seed export/import"`, "",
		},
		`- Revert "feat: seed export" because it broke the collector`: {
			`Revert "feat: seed export" because it broke the collector`, "",
		},
		// CLAUDE.md's mandated spelling for a breaking migration.
		"- BREAKING: rekey VM-scoped alert rules to (cluster_id, vmid)": {
			"Rekey VM-scoped alert rules to (cluster_id, vmid)", ChangeBreaking,
		},
		"- feat(api)!: one collection envelope": {
			"One collection envelope", ChangeBreaking,
		},
	} {
		got, _ := ParseBody(body)
		if len(got) != 1 {
			t.Errorf("%q: got %d highlights, want 1: %#v", body, len(got), got)
			continue
		}
		if got[0].Title != want.title {
			t.Errorf("%q: title = %q, want %q", body, got[0].Title, want.title)
		}
		if got[0].Type != want.changeType {
			t.Errorf("%q: type = %q, want %q", body, got[0].Type, want.changeType)
		}
		if got[0].Description != "" {
			t.Errorf("%q: unexpected description %q", body, got[0].Description)
		}
	}
}
