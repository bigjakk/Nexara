/**
 * Persisted per-table column layout: the order the columns sit in, and how
 * wide the user dragged each one.
 *
 * Storage mirrors features/inventory/lib/column-presets.ts, which already
 * persists that table's column visibility: one localStorage key per table,
 * everything wrapped in try/catch because localStorage throws outright in a
 * private window or with site data blocked, and a table that cannot remember
 * its layout must still render.
 */

const STORAGE_PREFIX = "nexara-columns:";

/** Never let a drag shrink a column to the point its header is unreadable. */
export const MIN_COLUMN_WIDTH = 56;
/** A ceiling only so a runaway drag cannot write a nonsense number to storage. */
export const MAX_COLUMN_WIDTH = 900;

export interface StoredColumnLayout {
  /** Column keys, left to right. Keys no longer in the table are ignored. */
  order?: string[] | undefined;
  /** Key -> width in px. Missing keys fall back to the column's default. */
  widths?: Record<string, number> | undefined;
}

function storageKey(tableId: string): string {
  return STORAGE_PREFIX + tableId;
}

export function loadColumnLayout(tableId: string): StoredColumnLayout {
  try {
    const raw = localStorage.getItem(storageKey(tableId));
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null) return {};
    const { order, widths } = parsed as StoredColumnLayout;
    return {
      // Storage is user-editable and survives releases that rename or drop a
      // column, so nothing read back here is trusted to be a valid key — the
      // reconciler below intersects it with the table's real columns.
      order: Array.isArray(order) ? order.filter((k) => typeof k === "string") : undefined,
      widths:
        widths && typeof widths === "object"
          ? Object.fromEntries(
              Object.entries(widths).filter(
                ([, v]) => typeof v === "number" && Number.isFinite(v),
              ),
            )
          : undefined,
    };
  } catch {
    return {};
  }
}

export function saveColumnLayout(
  tableId: string,
  layout: StoredColumnLayout,
): void {
  try {
    localStorage.setItem(storageKey(tableId), JSON.stringify(layout));
  } catch {
    // localStorage may be unavailable; the layout simply does not persist.
  }
}

export function clearColumnLayout(tableId: string): void {
  try {
    localStorage.removeItem(storageKey(tableId));
  } catch {
    // ignore
  }
}

/**
 * Reconcile a stored order against the columns the table actually has.
 *
 * A stored order is a snapshot from whenever the user last dragged a header,
 * and the table has moved on since: a release adds a column, drops one, or
 * renames a key; a permission or a viewport hides one. Trusting the stored
 * list outright would drop the new column off the table entirely and keep
 * rendering one that no longer exists.
 *
 * So: keep the stored order for columns that still exist, then append any
 * column the stored order has never seen, in its declared position relative to
 * the other newcomers. A brand-new column shows up at the end rather than
 * vanishing, which is the failure that would actually cost someone data.
 */
export function reconcileOrder<K extends string>(
  declared: readonly K[],
  stored: readonly string[] | undefined,
): K[] {
  if (!stored || stored.length === 0) return [...declared];
  const known = new Set<string>(declared);
  const seen = new Set<string>();
  const result: K[] = [];
  for (const key of stored) {
    if (known.has(key) && !seen.has(key)) {
      seen.add(key);
      result.push(key as K);
    }
  }
  for (const key of declared) {
    if (!seen.has(key)) result.push(key);
  }
  return result;
}

export function clampWidth(width: number): number {
  return Math.min(MAX_COLUMN_WIDTH, Math.max(MIN_COLUMN_WIDTH, Math.round(width)));
}

/**
 * Move `key` to sit immediately before or after `target`.
 *
 * Always returns a new array, including for a move that changes nothing — a
 * header dropped back where it started still costs one storage write. There is
 * no identity-return shortcut because the caller does not check for one.
 *
 * A `target` the order does not contain is a no-op, and that covers `key`
 * itself: the dragged key is filtered out before the target is located, so a
 * column dropped onto itself finds nothing to insert beside.
 */
export function moveColumn<K extends string>(
  order: readonly K[],
  key: K,
  target: K,
  side: "before" | "after",
): K[] {
  const without = order.filter((k) => k !== key);
  const at = without.indexOf(target);
  if (at === -1) return [...order];
  const insertAt = side === "after" ? at + 1 : at;
  const next = [...without];
  next.splice(insertAt, 0, key);
  return next;
}
