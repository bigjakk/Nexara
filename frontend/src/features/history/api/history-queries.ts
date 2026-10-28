import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  HistoryEntry,
  HistorySource,
  HistoryTarget,
  HistoryWindow,
} from "../types/history";

const WINDOW_MS: Record<Exclude<HistoryWindow, "">, number> = {
  "24h": 24 * 3600_000,
  "7d": 7 * 24 * 3600_000,
  "30d": 30 * 24 * 3600_000,
  "90d": 90 * 24 * 3600_000,
};

/** Rows per page, as on the task tables. */
export const HISTORY_PAGE_SIZE = 50;

interface HistoryParams {
  target: HistoryTarget;
  page: number;
  category: string;
  /** Categories left out (historyExclusions). */
  excludeCategories?: readonly string[];
  source: HistorySource | "";
  timeWindow: HistoryWindow;
  /** The guests' own activity, on a kind with a guest toggle. */
  includeGuests?: boolean;
  enabled: boolean;
}

/**
 * The route a target reads, without its query: the rest of the cache key.
 * apiPath refuses a value it cannot send; the queryFn then throws the same
 * error into the query's error state, so the key only has to differ.
 */
function routeKey(target: HistoryTarget): string {
  try {
    return String(target.path(new URLSearchParams()));
  } catch (err) {
    return `unsendable: ${err instanceof Error ? err.message : String(err)}`;
  }
}

/**
 * One page of an object's History, newest first. Keyed under
 * ["history", cluster] — "global" for a global kind's — so the WS events that
 * add to it (task_created, task_update, audit_entry) can invalidate every page
 * of the cluster's Histories at once (useEventInvalidation), and then on the
 * route itself. A status change no task explains is shown only once the
 * collector has classified it, minutes after it happened, so the 60s poll is
 * what brings it in.
 *
 * The key holds the window, not the timestamp it resolves to: the timestamp is
 * taken when the request is sent, so a refetch looks back from then rather
 * than from when the filter was picked.
 */
export function useHistory({
  target,
  page,
  category,
  excludeCategories = [],
  source,
  timeWindow,
  includeGuests = false,
  enabled,
}: HistoryParams) {
  return useQuery({
    queryKey: [
      "history",
      target.clusterId ?? "global",
      routeKey(target),
      page,
      category,
      excludeCategories.join(","),
      source,
      timeWindow,
      includeGuests,
    ],
    queryFn: () => {
      const params = new URLSearchParams();
      params.set("limit", String(HISTORY_PAGE_SIZE));
      params.set("offset", String(page * HISTORY_PAGE_SIZE));
      if (category) params.set("category", category);
      if (excludeCategories.length > 0) {
        params.set("exclude_category", excludeCategories.join(","));
      }
      if (source) params.set("source", source);
      if (includeGuests) params.set("include_guests", "true");
      if (timeWindow) {
        params.set(
          "start_time",
          new Date(Date.now() - WINDOW_MS[timeWindow]).toISOString(),
        );
      }
      return apiClient.page<HistoryEntry>(target.path(params));
    },
    enabled: enabled && target.clusterId !== "",
    // Keep the previous page (and its total) while the next one loads, as the
    // task tables do, so the pager does not collapse on every page turn.
    placeholderData: (prev) => prev,
    refetchInterval: 60_000, // WS events invalidate immediately; this is a fallback
  });
}
