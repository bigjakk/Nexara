import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { ApiPath } from "@/lib/api-path";
import type {
  HistoryEntry,
  HistoryPage,
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

/**
 * The instant a time filter looks back to, in epoch milliseconds, or null for
 * the whole history. The live rows above a History's first page are held to it
 * as the entries below them are.
 */
export function windowStartMs(
  timeWindow: HistoryWindow,
  now = Date.now(),
): number | null {
  return timeWindow === "" ? null : now - WINDOW_MS[timeWindow];
}

/** Rows per page, as on the task tables. */
export const HISTORY_PAGE_SIZE = 50;

interface HistoryFilters {
  target: HistoryTarget;
  category: string;
  /** Categories left out (historyExclusions). */
  excludeCategories?: readonly string[];
  source: HistorySource | "";
  timeWindow: HistoryWindow;
  /** No window on a kind whose route reads a default one without it. */
  allTime?: boolean;
  /** The guests' own activity, on a kind with a guest toggle. */
  includeGuests?: boolean;
  enabled: boolean;
}

interface HistoryParams extends HistoryFilters {
  page: number;
}

/** Where a read's window starts, when it has one: taken as it is sent. */
function windowStart(f: HistoryFilters): Date | undefined {
  const at = windowStartMs(f.timeWindow);
  return at === null ? undefined : new Date(at);
}

/** A read's route with its filters, from offset, and below end when set. */
function historyPath(
  f: HistoryFilters,
  offset: number,
  start: Date | undefined,
  end?: string,
): ApiPath {
  const params = new URLSearchParams();
  params.set("limit", String(HISTORY_PAGE_SIZE));
  params.set("offset", String(offset));
  if (f.category) params.set("category", f.category);
  const excluded = f.excludeCategories ?? [];
  if (excluded.length > 0) params.set("exclude_category", excluded.join(","));
  if (f.source) params.set("source", f.source);
  if (f.includeGuests === true) params.set("include_guests", "true");
  if (f.allTime === true) params.set("all_time", "true");
  if (start) params.set("start_time", start.toISOString());
  if (end) params.set("end_time", end);
  return f.target.path(params);
}

/** The filters' part of a read's cache key. */
function filterKey(f: HistoryFilters): unknown[] {
  return [
    f.category,
    (f.excludeCategories ?? []).join(","),
    f.source,
    f.timeWindow,
    f.includeGuests === true,
  ];
}

/**
 * The route a target reads, without its query: the rest of the cache key.
 * apiPath refuses a value it cannot send; the queryFn then throws the same
 * error into the query's error state, so the key only has to differ.
 */
export function routeKey(target: HistoryTarget): string {
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
export function useHistory(p: HistoryParams) {
  const { target, page, enabled } = p;
  return useQuery({
    queryKey: [
      "history",
      target.clusterId ?? "global",
      routeKey(target),
      page,
      ...filterKey(p),
    ],
    queryFn: (): Promise<HistoryPage> =>
      apiClient.page<HistoryEntry>(
        historyPath(p, page * HISTORY_PAGE_SIZE, windowStart(p)),
      ),
    enabled: enabled && target.clusterId !== "",
    // Keep the previous page (and its total) while the next one loads, as the
    // task tables do, so the pager does not collapse on every page turn.
    placeholderData: (prev) => prev,
    refetchInterval: 60_000, // WS events invalidate immediately; this is a fallback
  });
}

/** Where an older read goes on: further into its stretch while the stretch
 *  holds more than it has listed, then below where it stopped being whole. */
interface OlderParam {
  end: string;
  offset: number;
}

/** A page of an older read, with where in its stretch it was read. */
export interface OlderHistoryPage extends HistoryPage {
  at: OlderParam;
}

/**
 * The entries below where a read stopped being whole (its searched_from), for
 * Show older: each stretch whole down to its own searched_from, read a page at
 * a time with end_time, then the next stretch below it, until a stretch is
 * whole to the window's start; from undefined reads nothing.
 *
 * Not under ["history", cluster]: what lies below a read's searched_from is
 * past, and the events that re-read every History would re-read each page
 * of it in turn, every few seconds on a busy cluster.
 */
export function useOlderHistory({
  from,
  ...f
}: HistoryFilters & { from: string | undefined }) {
  const start: OlderParam = { end: from ?? "", offset: 0 };
  return useInfiniteQuery({
    queryKey: [
      "history-older",
      f.target.clusterId ?? "global",
      routeKey(f.target),
      from ?? "",
      ...filterKey(f),
    ],
    initialPageParam: start,
    queryFn: async ({ pageParam }): Promise<OlderHistoryPage> => {
      // A window that has moved up past the stretch's top holds none of it,
      // and the server refuses an end_time at or before start_time.
      const since = windowStart(f);
      const page =
        since && new Date(pageParam.end) <= since
          ? { items: [], total: 0 }
          : await apiClient.page<HistoryEntry>(
              historyPath(f, pageParam.offset, since, pageParam.end),
            );
      return { ...page, at: pageParam };
    },
    // A stretch that stops where it began is one source's cap's worth of rows
    // at a single instant: reading it again would stop there again.
    getNextPageParam: (last): OlderParam | undefined =>
      last.at.offset + last.items.length < last.total
        ? { end: last.at.end, offset: last.at.offset + HISTORY_PAGE_SIZE }
        : last.searched_from === undefined || last.searched_from === last.at.end
          ? undefined
          : { end: last.searched_from, offset: 0 },
    enabled: f.enabled && f.target.clusterId !== "" && from !== undefined,
  });
}
