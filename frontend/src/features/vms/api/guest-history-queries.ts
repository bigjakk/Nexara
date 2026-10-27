import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

/**
 * What kind of thing an entry is — the server derives it (category in
 * queries/guest_history.sql) so the type filter can apply before the page is
 * cut. The route refuses any other value.
 */
export type GuestHistoryCategory =
  | "power"
  | "migration"
  | "snapshot"
  | "backup"
  | "clone"
  | "lifecycle"
  | "config"
  | "ha"
  | "console"
  | "other";

/** Who did it: Nexara, Proxmox (an ingested task), or no task at all. */
export type GuestHistorySource = "nexara" | "proxmox" | "detected";

/**
 * How a task ended. Empty for an entry that is not a task, and for a task
 * whose end was never recorded.
 */
export type GuestHistoryOutcome = "" | "running" | "ok" | "failed";

/** One entry of GET /api/v1/clusters/:cluster_id/guests/:vmid/history. */
export interface GuestHistoryEntry {
  id: string;
  /**
   * An audit row, a task with no audit row, a status change no task explains,
   * or the guest's part of a backup job — a vzdump of several guests, whose
   * Proxmox task names none of them.
   */
  kind: "audit" | "task" | "status" | "backup";
  occurred_at: string;
  category: GuestHistoryCategory;
  source: GuestHistorySource;
  action: string;
  resource_type: string;
  /** The guest the row names: the source's VMID on a clone INTO this guest. */
  vmid: number;
  user_id: string;
  user_email: string;
  user_display_name: string;
  /** The audit row's details JSON, "{}" for the other kinds. */
  details: string;
  upid: string;
  node: string;
  description: string;
  /** Proxmox's worker type when the entry is a task, e.g. qmstart. */
  task_type: string;
  outcome: GuestHistoryOutcome;
  exit_status: string;
  progress?: number;
  finished_at: string | null;
  from_status?: string;
  to_status?: string;
  /** A status change: when the guest was last seen in from_status. The change
   *  happened after this and by occurred_at — minutes apart, unless Nexara or
   *  the node was unreachable in between. */
  last_seen_at?: string;
  /** A backup entry: the lines of the job's task log that are this guest's
   *  part, by Proxmox's line numbers. */
  log_start?: number;
  log_end?: number;
}

/** The time filter: how far back to look, or "" for the whole history. */
export type GuestHistoryWindow = "" | "24h" | "7d" | "30d" | "90d";

const WINDOW_MS: Record<Exclude<GuestHistoryWindow, "">, number> = {
  "24h": 24 * 3600_000,
  "7d": 7 * 24 * 3600_000,
  "30d": 30 * 24 * 3600_000,
  "90d": 90 * 24 * 3600_000,
};

/** Rows per page, as on the task tables. */
export const GUEST_HISTORY_PAGE_SIZE = 50;

interface GuestHistoryParams {
  clusterId: string;
  vmid: number;
  page: number;
  category: GuestHistoryCategory | "";
  source: GuestHistorySource | "";
  timeWindow: GuestHistoryWindow;
  enabled: boolean;
}

/**
 * One page of a guest's history, newest first. Keyed under "guest-history" so
 * the WS events that add to it (task_created, task_update, audit_entry) can
 * invalidate every page at once (useEventInvalidation). A status change no
 * task explains is shown only once the collector has classified it, minutes
 * after it happened, so the 60s poll is what brings it in.
 *
 * The key holds the window, not the timestamp it resolves to: the timestamp is
 * taken when the request is sent, so a refetch looks back from then rather
 * than from when the filter was picked.
 */
export function useGuestHistory({
  clusterId,
  vmid,
  page,
  category,
  source,
  timeWindow,
  enabled,
}: GuestHistoryParams) {
  return useQuery({
    queryKey: [
      "guest-history",
      clusterId,
      vmid,
      page,
      category,
      source,
      timeWindow,
    ],
    queryFn: () => {
      const params = new URLSearchParams();
      params.set("limit", String(GUEST_HISTORY_PAGE_SIZE));
      params.set("offset", String(page * GUEST_HISTORY_PAGE_SIZE));
      if (category) params.set("category", category);
      if (source) params.set("source", source);
      if (timeWindow) {
        params.set(
          "start_time",
          new Date(Date.now() - WINDOW_MS[timeWindow]).toISOString(),
        );
      }
      return apiClient.page<GuestHistoryEntry>(
        apiPath`/api/v1/clusters/${clusterId}/guests/${String(vmid)}/history?${params}`,
      );
    },
    enabled: enabled && clusterId.length > 0 && vmid > 0,
    // Keep the previous page (and its total) while the next one loads, as the
    // task tables do, so the pager does not collapse on every page turn.
    placeholderData: (prev) => prev,
    refetchInterval: 60_000, // WS events invalidate immediately; this is a fallback
  });
}
