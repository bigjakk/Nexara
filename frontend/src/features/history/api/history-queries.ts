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
  kind: string;
  target: HistoryTarget;
  page: number;
  category: string;
  source: HistorySource | "";
  timeWindow: HistoryWindow;
  enabled: boolean;
}

/**
 * One page of an object's History, newest first. Keyed under
 * ["history", cluster] so the WS events that add to it (task_created,
 * task_update, audit_entry) can invalidate every page of the cluster's
 * Histories at once (useEventInvalidation). A status change no task explains
 * is shown only once the collector has classified it, minutes after it
 * happened, so the 60s poll is what brings it in.
 *
 * The key holds the window, not the timestamp it resolves to: the timestamp is
 * taken when the request is sent, so a refetch looks back from then rather
 * than from when the filter was picked.
 */
export function useHistory({
  kind,
  target,
  page,
  category,
  source,
  timeWindow,
  enabled,
}: HistoryParams) {
  return useQuery({
    queryKey: [
      "history",
      target.clusterId,
      kind,
      target.objectKey,
      page,
      category,
      source,
      timeWindow,
    ],
    queryFn: () => {
      const params = new URLSearchParams();
      params.set("limit", String(HISTORY_PAGE_SIZE));
      params.set("offset", String(page * HISTORY_PAGE_SIZE));
      if (category) params.set("category", category);
      if (source) params.set("source", source);
      if (timeWindow) {
        params.set(
          "start_time",
          new Date(Date.now() - WINDOW_MS[timeWindow]).toISOString(),
        );
      }
      return apiClient.page<HistoryEntry>(target.path(params));
    },
    enabled: enabled && target.clusterId.length > 0,
    // Keep the previous page (and its total) while the next one loads, as the
    // task tables do, so the pager does not collapse on every page turn.
    placeholderData: (prev) => prev,
    refetchInterval: 60_000, // WS events invalidate immediately; this is a fallback
  });
}
