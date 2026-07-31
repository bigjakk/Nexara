package changelog

import (
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
