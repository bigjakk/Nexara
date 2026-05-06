export interface ChangelogHighlight {
  title: string;
  description?: string;
}

export interface ChangelogEntry {
  version: string;
  date: string;
  highlights: ChangelogHighlight[];
  url?: string;
}

export function extractBaseVersion(raw: string | null | undefined): string | null {
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
