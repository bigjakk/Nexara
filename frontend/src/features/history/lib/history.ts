/**
 * How a History's entries read, for every kind: their labels, one-line
 * summaries, who did them, and the outcome badge. JSX-free, so it is tested
 * without a renderer; the cells that show it are in history-column-defs.tsx.
 * A kind's own naming is its HistoryKind's, and its strings its namespace's.
 */
import type { TFunction } from "i18next";
import {
  deriveActor,
  formatAction,
} from "@/components/layout/activity-columns";
import { formatTimestampLong, formatTimestampShort } from "@/lib/format";
import {
  isOkExit,
  parseDetails,
  type DisplayStatus,
} from "@/components/layout/task-status";
import type {
  HistoryEntry,
  HistoryKind,
  HistorySource,
  HistoryWindow,
} from "../types/history";

/** The i18n namespace a kind's strings live in. */
export function historyNamespace(kind: Pick<HistoryKind, "name">): string {
  return `history-${kind.name}`;
}

/**
 * A HistoryKind whose categories are its namespace's — the keys of
 * `strings.categories`, in their order, which the server's vocabulary is
 * held to (TestHistoryCategoryVocabulary).
 */
export function historyKind(
  name: string,
  strings: { categories: Record<string, string> },
  rest: Omit<HistoryKind, "name" | "categories" | "layoutKey"> & {
    layoutKey?: string;
  },
): HistoryKind {
  const { layoutKey, ...labels } = rest;
  return {
    name,
    layoutKey: layoutKey ?? `history-${name}`,
    categories: Object.keys(strings.categories),
    ...labels,
  };
}

/** The source filter's choices, "" for every source; labelled from the shared
 *  namespace's `sourceOptions`. */
export const SOURCE_OPTIONS: readonly (HistorySource | "")[] = [
  "",
  "nexara",
  "proxmox",
  "detected",
];

/** The time filter's choices, "" for the whole history (`windows`). */
export const WINDOW_OPTIONS: readonly HistoryWindow[] = [
  "",
  "24h",
  "7d",
  "30d",
  "90d",
];

/** The History table's columns. None sorts: the timeline is newest first. */
export type HistoryColumnKey =
  "time" | "event" | "summary" | "who" | "node" | "outcome";

/**
 * What a History cell needs beyond the entry, resolved once per row so the
 * cells and the expanded row say the same thing.
 */
export interface HistoryCellCtx {
  label: string;
  summary: string;
  actor: string;
  /** null: not a task, or its end was never recorded. */
  display: DisplayStatus | null;
  progress: number | null;
  expanded: boolean;
  /** The kind's icon for the entry's category. */
  icon: HistoryKind["icons"][string] | undefined;
}

/** The Proxmox worker type a UPID names (field 5), or "". */
export function upidWorkerType(upid: string): string {
  return upid.split(":")[5] ?? "";
}

/** A label from the kind's namespace, or undefined when it has none. */
function lookup(t: TFunction, group: string, key: string): string | undefined {
  if (key === "") return undefined;
  const label = t(`${group}.${key}`, { defaultValue: "" });
  return label === "" ? undefined : label;
}

/** A list field's counts in an observed change's detail: the entries it
 *  gained and lost, as a rule set's rules. */
function listCounts(v: unknown): { added: number; removed: number } | null {
  if (v === null || typeof v !== "object") return null;
  const { added, removed } = v as Record<string, unknown>;
  return typeof added === "number" && typeof removed === "number"
    ? { added, removed }
    : null;
}

/**
 * An observed change, every kind alike: "Created in Proxmox", "Removed in
 * Proxmox", or "Changed in Proxmox: " and what changed — a rule set by the
 * rules it gained and lost, when its detail counts them (at its top level, or
 * under the list's own field), any other field by name.
 */
function observedLabel(entry: HistoryEntry, t: TFunction): string {
  if (entry.action === "added") return t("history:observed.added");
  if (entry.action === "removed") return t("history:observed.removed");
  const detail = parseDetails(entry.details);
  const ruleCounts = (c: { added: number; removed: number }) =>
    t("history:observed.ruleCounts", { count: c.added, removed: c.removed });
  const top = listCounts(detail);
  const parts = top
    ? [ruleCounts(top)]
    : (entry.fields ?? []).map((field) => {
        const c = listCounts(detail[field]);
        if (c === null) return field;
        return field === "rules" || field.endsWith("_rules")
          ? ruleCounts(c)
          : t("history:observed.listCounts", { field, ...c });
      });
  return parts.length > 0
    ? t("history:observed.changed", { fields: parts.join(", ") })
    : t("history:observed.changedUnnamed");
}

/** When an observed change was made: between the last sighting of the object
 *  as it was and the one that saw it changed — times alone on one day. */
function observedWindow(entry: HistoryEntry, t: TFunction): string {
  if (!entry.last_seen_at) return "";
  const from = new Date(entry.last_seen_at);
  const to = new Date(entry.occurred_at);
  const format =
    from.toDateString() === to.toDateString()
      ? formatTimestampShort
      : formatTimestampLong;
  return t("history:observed.window", {
    from: format(from.getTime()),
    to: format(to.getTime()),
  });
}

/**
 * The Event column. A Nexara row is named by Nexara's action, which says more
 * than the Proxmox task behind it ("DRS migration", not "Migrate (HA)"); a
 * Proxmox row by the task Proxmox ran. Both names come from the kind's
 * namespace (`actions`, `taskTypes`), else the action, title-cased. An
 * observed change reads the same in every kind (the shared `observed`).
 */
export function historyEventLabel(
  kind: HistoryKind,
  entry: HistoryEntry,
  t: TFunction,
): string {
  const own = kind.eventLabel?.(entry, t);
  if (own !== undefined) return own;
  if (entry.kind === "observed") return observedLabel(entry, t);
  if (entry.kind === "audit" && entry.source === "nexara") {
    return lookup(t, "actions", entry.action) ?? formatAction(entry.action);
  }
  return (
    lookup(t, "taskTypes", upidWorkerType(entry.upid)) ??
    lookup(t, "actions", entry.action) ??
    formatAction(entry.action)
  );
}

function str(d: Record<string, unknown>, key: string): string {
  const v = d[key];
  if (typeof v === "string") return v;
  if (typeof v === "number") return String(v);
  return "";
}

/**
 * The Summary column: the few details that say what this entry did — where a
 * migration went, which snapshot, which settings — rather than the whole
 * details blob, which the expanded row shows. A kind's own summary comes
 * first.
 */
export function historySummary(
  kind: HistoryKind,
  entry: HistoryEntry,
  t: TFunction,
): string {
  const own = kind.summary?.(entry, t);
  if (own !== undefined) return own;
  if (entry.kind === "observed") return observedWindow(entry, t);
  const d = parseDetails(entry.details);
  const parts: string[] = [];
  const from = str(d, "source_node");
  const to = str(d, "target_node") || str(d, "target");
  if (from && to) parts.push(`${from} → ${to}`);
  else if (to) parts.push(`→ ${to}`);
  // A clone from the entry's guest says where it went; a clone INTO it names
  // the guest itself, which its label already says.
  const newID = str(d, "new_id");
  if (newID && Number(newID) !== (entry.subject_vmid ?? 0)) {
    parts.push(`→ VM ${newID}`);
  }
  const snap = str(d, "snap_name");
  if (snap) parts.push(snap);
  const fields = d["fields"];
  if (fields && typeof fields === "object") {
    const f = fields as Record<string, unknown>;
    const changed = Object.keys(f).filter((k) => k !== "delete");
    if (changed.length > 0) parts.push(changed.join(", "));
    const removed = typeof f["delete"] === "string" ? f["delete"] : "";
    if (removed) parts.push(`removed ${removed.split(",").join(", ")}`);
  }
  const disk = str(d, "disk");
  const storage = str(d, "target_storage") || str(d, "storage");
  if (disk) parts.push(storage ? `${disk} → ${storage}` : disk);
  if ("new_pool" in d) {
    parts.push(
      `pool ${str(d, "old_pool") || "none"} → ${str(d, "new_pool") || "none"}`,
    );
  }
  const volid = str(d, "volid");
  if (volid) parts.push(volid);
  const state = str(d, "state");
  if (entry.resource_type === "ha_resource" && state)
    parts.push(`state ${state}`);
  const reason = str(d, "reason");
  if (reason) parts.push(reason);
  const error = str(d, "error");
  if (error) parts.push(`Error: ${error}`);
  if (parts.length === 0 && entry.kind === "task") return entry.description;
  return parts.join(" · ");
}

/** The note under an expanded entry: the kind's, else the shared one for an
 *  observed change. */
export function historyNote(
  kind: HistoryKind,
  entry: HistoryEntry,
  t: TFunction,
): string | undefined {
  const own = kind.note?.(entry, t);
  if (own !== undefined) return own;
  return entry.kind === "observed" ? t("history:observed.note") : undefined;
}

/** Where an entry comes from, in the shared namespace: a change Nexara saw in
 *  Proxmox reads "Detected in Proxmox", the rest by their source. */
export function historySourceLabel(entry: HistoryEntry, t: TFunction): string {
  return entry.kind === "observed"
    ? t("history:observed.source")
    : t(`history:sources.${entry.source}`);
}

/** Who did it — see deriveActor; "Proxmox" for an ingested task naming nobody.
 *  Nobody, for what Nexara only saw. */
export function historyActor(entry: HistoryEntry): string {
  if (entry.kind === "status" || entry.kind === "observed") return "";
  const actor = deriveActor(entry, parseDetails(entry.details));
  if (actor === "System" && entry.source === "proxmox") return "Proxmox";
  return actor;
}

/**
 * The outcome badge, or null for an entry that is not a task or whose end was
 * never recorded. A live poll finishes a row the server still calls running —
 * the server's terminal outcome is never overridden (deriveTaskStatus's rule).
 */
export function historyDisplayStatus(
  entry: HistoryEntry,
  live: { status: string; exit_status: string } | undefined,
): DisplayStatus | null {
  switch (entry.outcome) {
    case "ok":
      return "ok";
    case "failed":
      return "failed";
    case "running":
      if (live?.status === "stopped") {
        return isOkExit(live.exit_status) ? "ok" : "failed";
      }
      return "running";
  }
  return null;
}
