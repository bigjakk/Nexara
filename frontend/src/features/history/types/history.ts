/*
 * How to add a kind of object with a History (the guest is features/vms/lib/guest-history-kind.ts):
 *  1. The server side first: a HistoryKind and its route (internal/api/handlers/history.go says how).
 *  2. Its strings: locales/en/history-<name>.json, with `categories` in the server's order (Go's
 *     TestHistoryCategoryVocabulary holds the two together) and `empty`; register it in lib/i18n.ts.
 *     What every kind shares (filters, observed changes) is history.json's.
 *  3. A HistoryKind below, in your feature's lib/, built with historyKind(); override eventLabel,
 *     summary or note only where the shared naming (lib/history.ts) reads wrong for your rows.
 *  4. Show it: <HistoryTimeline> for a tab, or <HistoryButton> in a list row — shown with view:audit
 *     and view:task, its drawer keeps its own clicks from the row. The target's path must name the
 *     object: the cache is keyed on it. A global kind's target has clusterId null.
 */
import type { TFunction } from "i18next";
import type { LucideIcon } from "lucide-react";
import type { ListResponse } from "@/lib/api-client";
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
   * a guest's part of a backup job — a vzdump of several guests, whose
   * Proxmox task names none of them — or an observed change: one Nexara saw
   * in Proxmox that nothing on record explains (action added, removed or
   * changed) — or a run: a burst of one repeated row, a cluster's sync
   * failures, as one entry — or a move: a guest seen arriving on a node or
   * leaving it (action arrived or left, node the other one), or, with no
   * migration of it on record, its VMID seen on another node (now_on); a
   * move's from_status and to_status are its nodes.
   */
  kind: "audit" | "task" | "status" | "backup" | "observed" | "run" | "move";
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
  /** A status change: when the guest was last seen in from_status; an observed
   *  change: when the object was last seen as it was. The change happened
   *  after this and by occurred_at — minutes apart, unless Nexara or the node
   *  was unreachable in between. */
  last_seen_at?: string;
  /** An observed change: the names of the fields that changed. */
  fields?: string[];
  /** A backup entry: the lines of the job's task log that are this guest's
   *  part, by Proxmox's line numbers. */
  log_start?: number;
  log_end?: number;
  /** A run — a cluster's sync failures, or a node's status changes or a
   *  guest's moves past the hourly cap — how many it counts, from
   *  last_seen_at (its first) to occurred_at (its last); details are the last
   *  one's. */
  run_count?: number;
}

/** One page of a History, as its route returns it. */
export interface HistoryPage extends ListResponse<HistoryEntry> {
  /** Set when a source reached its cap (HistoryKind.RowCap): the read is
   *  whole back to here, and older entries of that source were not read. */
  searched_from?: string;
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
  /** The time filter's first choice: an object's History costs what its whole
   *  history costs, so a busy kind starts at a window. Unset: all time. Its
   *  route reads that window when none is named (HistoryKind.DefaultWindow),
   *  so All time asks for all_time. */
  defaultWindow?: HistoryWindow;
  /** Categories of routine checks (a node's daily package-list refresh), left
   *  out until the reader asks for them. */
  routine?: readonly string[];
  /** The route takes include_guests (HistoryKind.GuestToggle on the server):
   *  its guests' own activity is left out until the reader brings it in. */
  guestToggle?: boolean;
  /** The server's cap on an unfiltered read (HistoryKind.RowCap): a total of
   *  rowCap + 1 means more than rowCap, of which rowCap can be paged. */
  rowCap?: number;
  /** The cap on a read narrowed by type or source (HistoryKind.FilteredRowCap):
   *  each source's newest filteredRowCap rows are searched. */
  filteredRowCap?: number;
}

/** A guest an entry names, as the page showing the History links it. */
export interface HistoryGuestLink {
  name: string;
  path: string;
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
