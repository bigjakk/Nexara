/**
 * Which release-note section the server found a highlight under, used to pick
 * the chip beside it. The strings are the wire values of `changelog.ChangeType`
 * in internal/changelog/types.go; TestHighlight_TypeWireFormat pins each one's
 * JSON, and TestChangeTypesMatchTheFrontendUnion (internal/changelog/
 * wire_contract_test.go) pins this union against that vocabulary.
 *
 * Absent whenever there was no section to classify by — a curated
 * `## Highlights` section, a heading the parser does not map (above all
 * `## Other Changes`), a bullet before the first heading, or a release body
 * with no headings at all — and the row then renders without a chip rather
 * than with a guessed one. The one exception is "breaking", which comes from
 * the bullet itself (a `!` or a `BREAKING:` prefix) and so is present
 * wherever such a bullet appears.
 */
export type ChangelogChangeType =
  "new" | "improved" | "fix" | "security" | "docs" | "breaking";

export interface ChangelogHighlight {
  title: string;
  description?: string;
  type?: ChangelogChangeType;
}

export interface ChangelogEntry {
  version: string;
  date: string;
  highlights: ChangelogHighlight[];
  url?: string;
  /**
   * How many further highlights the release body had that the server's cap
   * dropped. Zero (and therefore absent) for essentially every real release.
   * When non-zero the dialog says so rather than ending the list silently.
   */
  more_count?: number;
}

export function extractBaseVersion(
  raw: string | null | undefined,
): string | null {
  if (!raw) return null;
  const match = /^v?(\d+\.\d+\.\d+)/.exec(raw);
  return match?.[1] ?? null;
}

// Returns the entries to display in the popup, given the user's last-seen
// version and the current version. Entries are expected to be sorted newest
// first.
//
// - If lastSeenVersion is null (first visit), returns just the current entry.
// - If the user has skipped versions, returns all entries from the current
//   release back to (but not including) the last-seen one.
// - If lastSeenVersion isn't in the changelog (rolled back, or unknown
//   version), falls back to showing just the current entry.
export function getEntriesToShow(
  currentVersion: string,
  lastSeenVersion: string | null,
  changelog: ChangelogEntry[],
): ChangelogEntry[] {
  if (lastSeenVersion === currentVersion) return [];

  const currentIdx = changelog.findIndex((e) => e.version === currentVersion);
  if (currentIdx === -1) return [];

  if (!lastSeenVersion) {
    const current = changelog[currentIdx];
    return current ? [current] : [];
  }

  const lastIdx = changelog.findIndex((e) => e.version === lastSeenVersion);
  if (lastIdx === -1 || lastIdx <= currentIdx) {
    const current = changelog[currentIdx];
    return current ? [current] : [];
  }

  return changelog.slice(currentIdx, lastIdx);
}
