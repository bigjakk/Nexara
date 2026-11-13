import { formatBytes } from "@/lib/format";
import { usageLevel, type UsageLevel } from "@/lib/usage-level";
import type { StorageResponse } from "@/types/api";
import { STORAGE_TYPE_LABELS } from "../types/storage";

/**
 * What a store may hold, in words as they read mid-sentence: one item, and
 * the kind as a whole.
 */
const CONTENT_KINDS: Record<string, { one: string; many: string }> = {
  images: { one: "disk image", many: "disk images" },
  rootdir: { one: "container volume", many: "container volumes" },
  iso: { one: "ISO image", many: "ISO images" },
  vztmpl: { one: "CT template", many: "CT templates" },
  backup: { one: "backup", many: "backups" },
  snippets: { one: "snippet", many: "snippets" },
  import: { one: "import", many: "imports" },
};

function sentenceCase(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** The content kinds a store declares, as Proxmox names them. */
export function contentKinds(content: string): string[] {
  return content
    .split(",")
    .map((kind) => kind.trim())
    .filter((kind) => kind !== "" && kind !== "none");
}

/** One item of a content kind, as a cell or label shows it: "Disk image". */
export function contentKindName(kind: string): string {
  return sentenceCase(CONTENT_KINDS[kind]?.one ?? kind);
}

/** A content kind as a whole, as it reads mid-sentence: "disk images". */
export function contentKindNoun(kind: string): string {
  return CONTENT_KINDS[kind]?.many ?? kind;
}

/** A content kind as a whole, as a label shows it: "Disk images". */
export function contentKindPlural(kind: string): string {
  return sentenceCase(contentKindNoun(kind));
}

function countOfKind(kind: string, n: number): string {
  const words = CONTENT_KINDS[kind];
  const noun = words === undefined ? kind : n === 1 ? words.one : words.many;
  return `${String(n)} ${noun}`;
}

const KIND_ORDER = Object.keys(CONTENT_KINDS);

/**
 * Content kinds in one order for every store: Proxmox lists them in the order
 * they were set, which differs from store to store.
 */
export function sortContentKinds(kinds: readonly string[]): string[] {
  const rank = (kind: string) => {
    const i = KIND_ORDER.indexOf(kind);
    return i === -1 ? KIND_ORDER.length : i;
  };
  return [...kinds].sort((a, b) => rank(a) - rank(b));
}

/** "Disk images, container volumes, ISO images", or "" for a store that holds nothing. */
export function contentList(content: string): string {
  return sentenceCase(
    sortContentKinds(contentKinds(content)).map(contentKindNoun).join(", "),
  );
}

/** "3 ISO images, 1 backup": what a list of content items is made of. */
export function contentBreakdown(kinds: readonly string[]): string {
  const counts = new Map<string, number>();
  for (const kind of kinds) counts.set(kind, (counts.get(kind) ?? 0) + 1);
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1])
    .map(([kind, n]) => countOfKind(kind, n))
    .join(", ");
}

/** Types Proxmox reports that the add and edit forms do not offer. */
const OTHER_TYPE_LABELS: Record<string, string | undefined> = {
  esxi: "ESXi",
};

/** "NFS", "LVM-Thin": the type as the forms name it, or as Proxmox does. */
export function storageTypeLabel(type: string): string {
  return (
    (STORAGE_TYPE_LABELS as Record<string, string | undefined>)[type] ??
    OTHER_TYPE_LABELS[type] ??
    type
  );
}

export type StoreState = "active" | "inactive" | "disabled";

export const STATE_LABEL: Record<StoreState, string> = {
  active: "Active",
  inactive: "Inactive",
  disabled: "Disabled",
};

/** What StatusIcon draws for each: a green dot, a red one, a hollow ring. */
export const STATE_ICON: Record<StoreState, string> = {
  active: "active",
  inactive: "offline",
  disabled: "stopped",
};

/** Disabled is a choice; enabled but not active is a store Proxmox cannot reach. */
export function storeState(pool: StorageResponse): StoreState {
  if (!pool.enabled) return "disabled";
  return pool.active ? "active" : "inactive";
}

/** Used share of a store's capacity, null when it reports no size. */
export function storePercent(pool: StorageResponse): number | null {
  return pool.total > 0 ? (pool.used / pool.total) * 100 : null;
}

export interface StoreRow {
  pool: StorageResponse;
  /** The node a local store is on; "" for a shared one. */
  nodeName: string;
  percent: number | null;
  level: UsageLevel;
}

/**
 * The stores of one cluster as the list shows them. A shared store is one
 * store reported by every node, so it is kept once; a local store is a
 * different one on each node. Shared first, then by node, then by name.
 */
export function storeRows(
  pools: readonly StorageResponse[],
  nodeNames: ReadonlyMap<string, string>,
): StoreRow[] {
  const sharedSeen = new Set<string>();
  const rows: StoreRow[] = [];
  for (const pool of pools) {
    if (pool.shared) {
      if (sharedSeen.has(pool.storage)) continue;
      sharedSeen.add(pool.storage);
    }
    const percent = storePercent(pool);
    rows.push({
      pool,
      nodeName: pool.shared ? "" : (nodeNames.get(pool.node_id) ?? ""),
      percent,
      level: usageLevel(percent),
    });
  }
  return rows.sort(
    (a, b) =>
      Number(b.pool.shared) - Number(a.pool.shared) ||
      a.nodeName.localeCompare(b.nodeName) ||
      a.pool.storage.localeCompare(b.pool.storage),
  );
}

export interface StorageTotals {
  stores: number;
  total: number;
  used: number;
  free: number;
  percent: number | null;
  /** Stores at or past the amber threshold, the full ones included. */
  nearFull: number;
  /** Stores at or past the red threshold. */
  full: number;
  /** Enabled stores Proxmox cannot reach. */
  inactive: number;
}

export function storageTotals(rows: readonly StoreRow[]): StorageTotals {
  let total = 0;
  let used = 0;
  let free = 0;
  let nearFull = 0;
  let full = 0;
  let inactive = 0;
  for (const row of rows) {
    total += row.pool.total;
    used += row.pool.used;
    free += row.pool.avail;
    if (row.level !== "ok") nearFull++;
    if (row.level === "err") full++;
    if (storeState(row.pool) === "inactive") inactive++;
  }
  return {
    stores: rows.length,
    total,
    used,
    free,
    percent: total > 0 ? (used / total) * 100 : null,
    nearFull,
    full,
    inactive,
  };
}

function count(n: number, one: string, many: string): string {
  return `${String(n)} ${n === 1 ? one : many}`;
}

/**
 * The Storage page's summary sentence: "14 stores on 2 clusters, 3.1 TiB of
 * 8.4 TiB used, 1 near full". `clusters` are the ones that answered; one that
 * could not be read holds none of the stores counted, and is said apart.
 * Undefined when there is nothing to say.
 */
export function storageSummary(
  totals: StorageTotals,
  clusters: number,
  unread = 0,
): string | undefined {
  const parts: string[] = [];
  if (totals.stores > 0) {
    parts.push(
      `${count(totals.stores, "store", "stores")} on ${count(clusters, "cluster", "clusters")}`,
    );
    if (totals.total > 0) {
      parts.push(
        `${formatBytes(totals.used)} of ${formatBytes(totals.total)} used`,
      );
    }
    if (totals.nearFull > 0) parts.push(`${String(totals.nearFull)} near full`);
    if (totals.inactive > 0) parts.push(`${String(totals.inactive)} inactive`);
  }
  if (unread > 0) {
    parts.push(`${count(unread, "cluster", "clusters")} could not be read`);
  }
  return parts.length > 0 ? parts.join(", ") : undefined;
}
