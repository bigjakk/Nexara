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
import { detailPairs } from "@/features/events/lib/audit-details";
import { resourceTypeLabel } from "@/features/events/lib/resource-types";
import {
  isOkExit,
  parseDetails,
  type DisplayStatus,
} from "@/components/layout/task-status";
import type {
  HistoryEntry,
  HistoryGuestLink,
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

/** The categories a timeline leaves out: the kind's routine ones, unless the
 *  reader shows them or filters to one of them. */
export function historyExclusions(
  kind: Pick<HistoryKind, "routine">,
  showRoutine: boolean,
  category: string,
): string[] {
  if (showRoutine) return [];
  return (kind.routine ?? []).filter((c) => c !== category);
}

/** The History table's columns. None sorts: the timeline is newest first. A
 *  History of many guests (a folder's) has a guest column too. */
export type HistoryColumnKey =
  "time" | "event" | "guest" | "summary" | "who" | "node" | "outcome";

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
  /** The guest the entry is filed under or names, with its link when the
   *  page has one; null for an entry naming no guest. */
  guest: { vmid: number; link: HistoryGuestLink | undefined } | null;
}

/** The guest an entry is filed under, else the one it names; 0 for none. */
export function historyGuestVmid(entry: HistoryEntry): number {
  return entry.subject_vmid || entry.vmid;
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
 *  gained and lost, and moved, as a rule set's rules. */
function listCounts(
  v: unknown,
): { added: number; removed: number; moved: number } | null {
  if (v === null || typeof v !== "object") return null;
  const { added, removed, moved } = v as Record<string, unknown>;
  return typeof added === "number" && typeof removed === "number"
    ? { added, removed, moved: typeof moved === "number" ? moved : 0 }
    : null;
}

function num(d: Record<string, unknown>, key: string): number | undefined {
  const v = d[key];
  return typeof v === "number" ? v : undefined;
}

/** A count as the reader's locale writes it: 12,000. */
function formatCount(n: number): string {
  return n.toLocaleString();
}

/** An observer's notice (detail.notice): its own word on a cluster or a kind
 *  of object — the token's privileges moved, a kind it cannot hold whole and
 *  what came, went and changed of it a pass past the cap — a point event
 *  rather than a change made between two readings. The kind is named with
 *  the parent and the node whose objects it counts. */
function noticeLabel(
  notice: string,
  entry: HistoryEntry,
  d: Record<string, unknown>,
  t: TFunction,
): string {
  const parent = str(d, "parent");
  const node = str(d, "node");
  const count = (key: string) => formatCount(num(d, key) ?? 0);
  // Each sentence has its own spelling with the parent and the node it counts,
  // and the token's privileges one for a change during an upgrade
  // (notices.<notice>_parent, _node, _parentNode, _upgrade).
  const context =
    d["during_upgrade"] === true
      ? "upgrade"
      : parent && node
        ? "parentNode"
        : parent
          ? "parent"
          : node
            ? "node"
            : "";
  return t(`history:observed.notices.${notice}`, {
    defaultValue: t("history:observed.notices.other", { notice }),
    context,
    kind: resourceTypeLabel(entry.fields?.[0] ?? ""),
    parent,
    node,
    objects: count("objects"),
    cap: count("cap"),
    added: count("added"),
    removed: count("removed"),
    changed: count("changed"),
    guests: count("guests"),
    per_pass: count("per_pass"),
  });
}

/**
 * An observed change, every kind alike: "Created in Proxmox", "Removed in
 * Proxmox", or "Changed in Proxmox: " and what changed — a list by the items
 * it gained and lost (detail.counts, or the older shapes at the detail's top
 * level or under the list's own field), any other field by name. A parent
 * removed says how many of its own objects went with it (children), and an
 * object created while its kind was past the cap says so (while_over_cap); a
 * notice is the observer's own word.
 */
function observedLabel(entry: HistoryEntry, t: TFunction): string {
  const detail = parseDetails(entry.details);
  const notice = str(detail, "notice");
  if (notice) return noticeLabel(notice, entry, detail, t);
  if (entry.action === "added") {
    return detail["while_over_cap"] === true
      ? t("history:observed.addedPastCap")
      : t("history:observed.added");
  }
  if (entry.action === "removed") {
    const children = num(detail, "children");
    return children
      ? t("history:observed.removedWithChildren", {
          count: children,
          n: formatCount(children),
        })
      : t("history:observed.removed");
  }
  const counts =
    detail["counts"] !== null && typeof detail["counts"] === "object"
      ? (detail["counts"] as Record<string, unknown>)
      : {};
  const counted = (c: { added: number; removed: number; moved: number }) => ({
    count: c.added,
    n: formatCount(c.added),
    added: formatCount(c.added),
    removed: formatCount(c.removed),
    moved: formatCount(c.moved),
  });
  const listOf = (
    field: string,
    c: { added: number; removed: number; moved: number },
  ) =>
    t(
      c.moved > 0
        ? "history:observed.listCountsMoved"
        : "history:observed.listCounts",
      { field, ...counted(c) },
    );
  const ruleCounts = (c: { added: number; removed: number; moved: number }) =>
    t(
      c.moved > 0
        ? "history:observed.ruleCountsMoved"
        : "history:observed.ruleCounts",
      counted(c),
    );
  const top = listCounts(detail);
  const parts = top
    ? [ruleCounts(top)]
    : (entry.fields ?? []).map((field) => {
        const c = listCounts(counts[field]) ?? listCounts(detail[field]);
        if (c === null) return field;
        return field === "rules" || field.endsWith("_rules")
          ? ruleCounts(c)
          : listOf(field, c);
      });
  return parts.length > 0
    ? t("history:observed.changed", { fields: parts.join(", ") })
    : t("history:observed.changedUnnamed");
}

/** When an observed change was made: between the last sighting of the object
 *  as it was and the one that saw it changed — times alone on one day. A
 *  notice is a point event, when it was filed, and has none. */
function observedWindow(entry: HistoryEntry, t: TFunction): string {
  if (!entry.last_seen_at || isObservedNotice(entry)) return "";
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

/** What an observed change's detail marks beside its fields: the fields
 *  Proxmox filled in where there was none (fields_appeared), and the changes
 *  of the same reading it was one of (bulk, bulk_size). */
function observedMarks(entry: HistoryEntry, t: TFunction): string[] {
  const d = parseDetails(entry.details);
  const marks: string[] = [];
  const appeared = d["fields_appeared"];
  if (Array.isArray(appeared) && appeared.length > 0) {
    marks.push(
      t("history:observed.fieldsAppeared", { fields: appeared.join(", ") }),
    );
  }
  const bulk = num(d, "bulk_size");
  if (str(d, "bulk") && bulk !== undefined && bulk > 1) {
    marks.push(
      t("history:observed.bulk", { count: bulk - 1, n: formatCount(bulk - 1) }),
    );
  }
  return marks;
}

/** An audit row whose fields Nexara could not check against what Proxmox
 *  stored (fields_unverified). */
function unverifiedMark(entry: HistoryEntry, t: TFunction): string {
  return parseDetails(entry.details)["fields_unverified"] === true
    ? t("history:observed.unverified")
    : "";
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
  if (entry.kind === "run") {
    return t("history:run.label", {
      event: lookup(t, "actions", entry.action) ?? formatAction(entry.action),
      count: entry.run_count ?? 0,
    });
  }
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
 * The Event of an audit row whose action is a plain verb and whose resource
 * type the kind names in its `types`: "HA group created", "API token
 * regenerated". For a kind that reads several types, where "Created" alone
 * would not say what was.
 */
export function typedEventLabel(
  entry: HistoryEntry,
  t: TFunction,
): string | undefined {
  if (entry.kind !== "audit") return undefined;
  const type = lookup(t, "types", entry.resource_type);
  const verb = lookup(t, "history:verbs", entry.action);
  return type !== undefined && verb !== undefined
    ? t("history:typed", { type, verb })
    : undefined;
}

/**
 * The Summary column: the few details that say what this entry did — where a
 * migration went, which snapshot, which settings — rather than the whole
 * details blob, which the expanded row shows. A kind's own summary comes
 * first, and what the entry is about (HistoryKind.subject) before either. An
 * audit entry whose fields Nexara could not check says so.
 */
export function historySummary(
  kind: HistoryKind,
  entry: HistoryEntry,
  t: TFunction,
): string {
  return [
    kind.subject?.(entry, t),
    entrySummary(kind, entry, t),
    alsoChanged(entry, t),
    entry.kind === "audit" && unverifiedMark(entry, t),
  ]
    .filter(Boolean)
    .join(" · ");
}

/** What an audit entry's edit changed in Proxmox beyond what it names: its
 *  own object by the fields alone, another by its type and id too. */
function alsoChanged(entry: HistoryEntry, t: TFunction): string {
  const objects = entry.also_changed ?? [];
  if (objects.length === 0) return "";
  return t("history:observed.alsoChanged", {
    objects: objects
      .map((o) =>
        o.resource_type === entry.resource_type &&
        o.resource_id === entry.resource_id
          ? o.fields.join(", ")
          : t("history:observed.alsoChangedObject", {
              type: resourceTypeLabel(o.resource_type),
              id: o.resource_id,
              fields: o.fields.join(", "),
            }),
      )
      .join("; "),
  });
}

function entrySummary(
  kind: HistoryKind,
  entry: HistoryEntry,
  t: TFunction,
): string {
  const own = kind.summary?.(entry, t);
  if (own !== undefined) return own;
  if (entry.kind === "observed") {
    return [observedWindow(entry, t), ...observedMarks(entry, t)]
      .filter(Boolean)
      .join(" · ");
  }
  const d = parseDetails(entry.details);
  // A run: between its first row and its last, and the last one's error.
  if (entry.kind === "run") {
    const error = str(d, "error");
    return [observedWindow(entry, t), error && `Error: ${error}`]
      .filter(Boolean)
      .join(" · ");
  }
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
  // What a guest's config update moved (details.changed), else the fields an
  // update names: a list of names, or an object keyed by them.
  const changed = d["changed"];
  const fields = d["fields"];
  if (Array.isArray(changed)) {
    const moved = changed.filter((f): f is string => typeof f === "string");
    if (moved.length > 0) parts.push(moved.join(", "));
  } else if (Array.isArray(fields)) {
    const named = fields.filter((f): f is string => typeof f === "string");
    if (named.length > 0) parts.push(named.join(", "));
  } else if (fields && typeof fields === "object") {
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

/** Longer than this, a list in an entry's details is shown collapsed. */
const LONG_DETAIL_LIST = 10;

/**
 * An expanded entry's details: the label/value pairs, and apart from them the
 * lists too long to read inline — the ids a kind read past its cap names, up
 * to hundreds each way — which the row shows collapsed.
 */
export function historyDetailParts(details: string): {
  pairs: Array<[string, string]> | null;
  lists: Array<{ key: string; items: string[] }>;
} {
  const d = parseDetails(details);
  const lists: Array<{ key: string; items: string[] }> = [];
  const rest: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(d)) {
    if (
      Array.isArray(value) &&
      value.length > LONG_DETAIL_LIST &&
      value.every((v) => typeof v === "string")
    ) {
      lists.push({ key, items: value });
    } else {
      rest[key] = value;
    }
  }
  return { pairs: detailPairs(JSON.stringify(rest)), lists };
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
  if (isObservedNotice(entry)) return t("history:observed.noticeNote");
  return entry.kind === "observed" ? t("history:observed.note") : undefined;
}

/** An observer's notice (detail.notice): its word on how it reads a cluster,
 *  filed when it was written, rather than a change made in Proxmox. */
export function isObservedNotice(entry: HistoryEntry): boolean {
  return (
    entry.kind === "observed" &&
    str(parseDetails(entry.details), "notice") !== ""
  );
}

/** Where an entry comes from, in the shared namespace: a change Nexara saw in
 *  Proxmox reads "Detected in Proxmox", a notice is the observer's, a UPS's
 *  power event is read from the UPS, the rest read by their source. */
export function historySourceLabel(entry: HistoryEntry, t: TFunction): string {
  if (isObservedNotice(entry)) return t("history:observed.noticeSource");
  if (entry.kind === "power") return t("history:power.source");
  return entry.kind === "observed"
    ? t("history:observed.source")
    : t(`history:sources.${entry.source}`);
}

/** Who did it — see deriveActor; "Proxmox" for an ingested task naming nobody.
 *  Nobody, for what Nexara only saw. */
export function historyActor(entry: HistoryEntry): string {
  if (entry.kind === "status" || entry.kind === "observed" || entry.kind === "power") return "";
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
