/*
 * How to add a kind of object with a History (the guest is features/vms/lib/guest-history-kind.ts):
 *  1. The server side first: a HistoryKind and its route (internal/api/handlers/history.go says how).
 *  2. Its strings: locales/en/history-<name>.json, with `categories` in the server's order (Go's
 *     TestHistoryCategoryVocabulary holds the two together) and `empty`; register it in lib/i18n.ts.
 *  3. A HistoryKind below, in your feature's lib/, built with historyKind(); override eventLabel,
 *     summary or note only where the shared naming (lib/history.ts) reads wrong for your rows.
 *  4. Show it: <HistoryTimeline> for a tab, <HistoryDrawer> from a list row.
 */
import type { TFunction } from "i18next";
import type { LucideIcon } from "lucide-react";
import type { ApiPath } from "@/lib/api-path";

/** Who did it: Nexara, Proxmox (an ingested task), or no task at all. */
export type HistorySource = "nexara" | "proxmox" | "detected";

/**
 * How a task ended. Empty for an entry that is not a task, and for a task
 * whose end was never recorded.
 */
export type HistoryOutcome = "" | "running" | "ok" | "failed";

/** One entry of a History route (internal/api/handlers/history.go). */
export interface HistoryEntry {
  id: string;
  /** The entry's own cluster: the target's, or — on a global kind's History —
   *  the row's, or none. */
  cluster_id?: string;
  /**
   * An audit row, a task with no audit row, a status change no task explains,
   * or a guest's part of a backup job — a vzdump of several guests, whose
   * Proxmox task names none of them.
   */
  kind: "audit" | "task" | "status" | "backup";
  occurred_at: string;
  /** One of the kind's categories, derived on the server before the page is
   *  cut so the type filter applies to the whole history. */
  category: string;
  source: HistorySource;
  action: string;
  resource_type: string;
  resource_id: string;
  /** The guest the row names: the source's VMID on a clone INTO a guest. */
  vmid: number;
  /** The guest the entry is filed under; absent for an object's own row. */
  subject_vmid?: number;
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
  outcome: HistoryOutcome;
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
export type HistoryWindow = "" | "24h" | "7d" | "30d" | "90d";

/**
 * One kind of object with a History, as the timeline shows it. Its strings
 * are the i18n namespace `history-<name>`; the label functions are handed
 * that namespace's `t`.
 */
export interface HistoryKind {
  /** The server's name for the kind (HistoryKind.Name). */
  name: string;
  /** Where the timeline's column widths are kept (useColumnLayout). */
  layoutKey: string;
  /** The type filter's categories, in order: the namespace's `categories`. */
  categories: readonly string[];
  /** Each category's icon; a category without one shows a plain circle. */
  icons: Readonly<Record<string, LucideIcon>>;
  /** The Event column, where the shared naming reads wrong for an entry. */
  eventLabel?: (entry: HistoryEntry, t: TFunction) => string | undefined;
  /** The Summary column, likewise. */
  summary?: (entry: HistoryEntry, t: TFunction) => string | undefined;
  /** A note under an expanded entry. */
  note?: (entry: HistoryEntry, t: TFunction) => string | undefined;
}

/** Which object's History to read, and where. */
export interface HistoryTarget {
  /** The cluster it is read in, which the WS events invalidate it by: "" while
   *  it is not known yet, which reads nothing; null for a global kind's
   *  History (HistoryKind.Global on the server), read across clusters. */
  clusterId: string | null;
  /** The route, with the page's query string: GET path?limit&offset&…. The
   *  cache is keyed on it, so it must name the object: two targets with one
   *  path share their pages. */
  path: (query: URLSearchParams) => ApiPath;
}
