import { useMemo, useState } from "react";

export type SortDirection = "asc" | "desc";

export interface SortState<K extends string> {
  key: K;
  direction: SortDirection;
}

/**
 * What a column sorts on. `null` means "this row has no value" — not zero and
 * not the empty string, which are real values that belong in the ordering.
 */
export type SortValue = string | number | null;

export type SortAccessors<T, K extends string> = Record<
  K,
  (row: T) => SortValue
>;

function compare(a: SortValue, b: SortValue, direction: SortDirection): number {
  // Nulls sort last in BOTH directions, deliberately ahead of the direction
  // sign. A job that has never run is not the earliest job; it is unknown,
  // and floating a screenful of "Never" to the top on one click buries the
  // rows the operator asked to see.
  if (a === null && b === null) return 0;
  if (a === null) return 1;
  if (b === null) return -1;

  const sign = direction === "asc" ? 1 : -1;
  if (typeof a === "number" && typeof b === "number") {
    return (a - b) * sign;
  }
  // `numeric` so Job_2 precedes Job_10, `base` so case does not split
  // otherwise-adjacent names.
  return (
    String(a).localeCompare(String(b), undefined, {
      numeric: true,
      sensitivity: "base",
    }) * sign
  );
}

/**
 * The common row key. A single module-scope reference, so passing it does not
 * defeat the memo below the way an inline arrow would.
 */
export const byId = (row: { id: string }) => row.id;

/**
 * Client-side column sorting for the hand-rolled `<Table>`s.
 *
 * These tables render `rows.map(...)` directly and carry expandable detail
 * rows, action cells and per-row mutations, so adopting TanStack Table (used
 * once, by the inventory ResourceTable) would mean rewriting each of them.
 * This sorts the array before it is mapped and leaves everything else alone.
 *
 * `accessors` and `rowKey` must be stable references — define them at module
 * scope (or use the exported `byId`), not inline in the component, or every
 * render re-sorts.
 */
export function useTableSort<T, K extends string>(
  rows: T[],
  accessors: SortAccessors<T, K>,
  rowKey: (row: T) => string,
  initial: SortState<K> | null = null,
) {
  const [sort, setSort] = useState<SortState<K> | null>(initial);

  const sortedRows = useMemo(() => {
    if (sort === null) return rows;
    const accessor = accessors[sort.key];
    // Copied, not sorted in place: `rows` is query data owned by TanStack
    // Query's cache, and mutating it reorders every other consumer's view.
    return [...rows].sort((a, b) => {
      const primary = compare(accessor(a), accessor(b), sort.direction);
      if (primary !== 0) return primary;
      // Ties break on the row key, always ascending regardless of direction.
      //
      // Array.prototype.sort is stable, so without this ties would hold
      // whatever order the backend returned — and that is not stable across
      // refetches. `ORDER BY o.name` over orphaned objects is the clearest
      // case: duplicate names are the premise of that view (a host rebuilt
      // under its old name leaves the previous object behind), so sorting by
      // "Restore points" leaves a dozen rows tied on 1, and each collector
      // poll reshuffles them under an operator who is reading them.
      return rowKey(a).localeCompare(rowKey(b));
    });
  }, [rows, sort, accessors, rowKey]);

  function toggle(key: K) {
    setSort((prev) =>
      prev !== null && prev.key === key
        ? { key, direction: prev.direction === "asc" ? "desc" : "asc" }
        : { key, direction: "asc" },
    );
  }

  /** The active direction for `key`, or null when another column is sorted. */
  function directionFor(key: K): SortDirection | null {
    return sort !== null && sort.key === key ? sort.direction : null;
  }

  return { rows: sortedRows, sort, toggle, directionFor };
}
