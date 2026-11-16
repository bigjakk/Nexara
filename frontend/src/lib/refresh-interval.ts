import { usePreferencesStore } from "@/stores/preferences-store";

/** The fastest a general refresh may run, whatever the saved value says. */
export const MIN_REFRESH_MS = 10_000;

/** The default when the saved value is not a number (the store's default). */
const DEFAULT_REFRESH_MS = 30_000;

/**
 * The refetchInterval for a list or status that refreshes on a general
 * cadence, from Settings → Appearance → Default refresh interval (seconds):
 * `false` for 0 (manual: no timer; the page still refetches after the
 * person's own changes and on live events), never under MIN_REFRESH_MS.
 *
 * A poll whose rate is part of how a feature works (a running task's progress,
 * a log tail, a live chart, a run in flight) keeps its own rate and does not
 * use this.
 */
export function refreshIntervalMs(seconds: unknown): number | false {
  if (seconds === 0) return false;
  if (typeof seconds !== "number" || !Number.isFinite(seconds) || seconds < 0) {
    return DEFAULT_REFRESH_MS;
  }
  return Math.max(seconds * 1000, MIN_REFRESH_MS);
}

/**
 * The general refetchInterval, read in the query hook so a change of
 * preference re-renders the hook and TanStack Query re-arms its timer at once.
 */
export function useRefreshInterval(): number | false {
  return refreshIntervalMs(
    usePreferencesStore((s) => s.preferences.refreshInterval),
  );
}
