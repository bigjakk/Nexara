import { usePreferencesStore } from "@/stores/preferences-store";

/** The fastest a general refresh may run, whatever the saved value says. */
export const MIN_REFRESH_MS = 10_000;

/** The preference every site's designed rate is written against. */
const BASELINE_SECONDS = 30;

/**
 * The refetchInterval for a list or status that refreshes on a general
 * cadence. Settings → Appearance → Default refresh interval (seconds) scales
 * the site's own designed rate: designed × (preference ÷ 30), so the default
 * of 30 changes nothing, 10 is three times as fast and 300 ten times as slow;
 * never under MIN_REFRESH_MS. 0 is manual: `false`, no timer (the page still
 * refetches after the person's own changes and on live events). A saved value
 * that is not a usable number leaves the designed rate.
 *
 * A poll whose rate is part of how a feature works (a running task's progress,
 * a log tail, a live chart, a run in flight) keeps its own rate and does not
 * use this.
 */
export function refreshIntervalMs(
  seconds: unknown,
  designedMs: number,
): number | false {
  if (seconds === 0) return false;
  if (typeof seconds !== "number" || !Number.isFinite(seconds) || seconds < 0) {
    return designedMs;
  }
  return Math.max((designedMs * seconds) / BASELINE_SECONDS, MIN_REFRESH_MS);
}

/**
 * The general refetchInterval for a site designed to poll every `designedMs`,
 * read in the query hook so a change of preference re-renders the hook and
 * TanStack Query re-arms its timer at once.
 */
export function useRefreshInterval(designedMs: number): number | false {
  return refreshIntervalMs(
    usePreferencesStore((s) => s.preferences.refreshInterval),
    designedMs,
  );
}
