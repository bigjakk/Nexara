import { useMemo } from "react";
import {
  sortAccessorsFrom,
  useColumnLayout,
  type ColumnDef,
} from "@/hooks/useColumnLayout";
import { useTableSort, type SortState } from "@/hooks/useTableSort";

/**
 * A client-sorted table's column layout and row ordering, from one call.
 *
 * The two hooks are always used together and always read the same column
 * declarations, so pairing them here keeps a table from sorting on one set of
 * columns while rendering another. It also memoises the accessors it derives
 * from `columns`, which is half of what useTableSort asks for: rebuilding that
 * object every render would re-sort every render.
 *
 * The other half is yours. `columns` AND `rowKey` must both be stable
 * references — module-scope constants, not inline arrows — or the sort runs
 * again on every render. Use the exported `byId` where the rows have one.
 *
 * Note the accessors come from the full `columns`, not `layout.columns`: below
 * `md` the layout drops `hideBelowMd` columns, and a sort left active on one
 * would then find no accessor at all.
 *
 * Server-sorted tables do not use this; they own their sort state and adopt
 * only useColumnLayout.
 */
export function useDataTable<Row, K extends string, Ctx>(
  tableId: string,
  columns: readonly ColumnDef<Row, K, Ctx>[],
  rows: Row[],
  rowKey: (row: Row) => string,
  /** Column to sort by on first render. Passed through to useTableSort. */
  initial: SortState<K> | null = null,
) {
  const layout = useColumnLayout(tableId, columns);
  const accessors = useMemo(() => sortAccessorsFrom(columns), [columns]);
  const sort = useTableSort(rows, accessors, rowKey, initial);
  return { layout, ...sort };
}
