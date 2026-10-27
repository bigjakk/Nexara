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

export const SOURCE_LABEL: Record<HistorySource, string> = {
  nexara: "Nexara",
  proxmox: "Proxmox",
  detected: "No task",
};

export const SOURCE_OPTIONS: {
  value: HistorySource | "";
  label: string;
}[] = [
  { value: "", label: "All sources" },
  { value: "nexara", label: "Done in Nexara" },
  { value: "proxmox", label: "Done in Proxmox" },
  { value: "detected", label: "No task (seen by Nexara)" },
];

export const WINDOW_OPTIONS: { value: HistoryWindow; label: string }[] = [
  { value: "", label: "All time" },
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
  { value: "90d", label: "Last 90 days" },
];

/** The History table's columns. None sorts: the timeline is newest first. */
export type HistoryColumnKey =
  | "time"
  | "event"
  | "summary"
  | "who"
  | "node"
  | "outcome";

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

/**
 * The Event column. A Nexara row is named by Nexara's action, which says more
 * than the Proxmox task behind it ("DRS migration", not "Migrate (HA)"); a
 * Proxmox row by the task Proxmox ran. Both names come from the kind's
 * namespace (`actions`, `taskTypes`), else the action, title-cased.
 */
export function historyEventLabel(
  kind: HistoryKind,
  entry: HistoryEntry,
  t: TFunction,
): string {
  const own = kind.eventLabel?.(entry, t);
  if (own !== undefined) return own;
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

/** Who did it — see deriveActor; "Proxmox" for an ingested task naming nobody. */
export function historyActor(entry: HistoryEntry): string {
  if (entry.kind === "status") return "";
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
