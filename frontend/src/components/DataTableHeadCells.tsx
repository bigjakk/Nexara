import { use } from "react";
import { TableHead, TableRow } from "@/components/ui/table";
import { DataTableFillerContext } from "@/components/data-table-filler";
import { DataTableHead } from "@/components/DataTableHead";
import type { SortDirection } from "@/hooks/useTableSort";
import type { ColumnLayout } from "@/hooks/useColumnLayout";

interface HeadProps<Row, K extends string, Ctx> {
  layout: ColumnLayout<Row, K, Ctx>;
  /** The active direction for one column; null when another is sorted. */
  directionFor: (key: K) => SortDirection | null;
  onSort: (key: K) => void;
}

/**
 * Every heading in the table, in the user's column order.
 *
 * The counterpart to DataTableCells: both map over `layout.columns`, which is
 * what makes a reorder move a heading and its values together. Kept separate
 * from DataTableHeadRow below because the tables are split between the shadcn
 * `<Table>` and a plain `<table>`, and the two `<tr>`s are not interchangeable
 * — TableRow carries hover and selection styling a `<thead>` row of the plain
 * tables does not want.
 */
export function DataTableHeadCells<Row, K extends string, Ctx>({
  layout,
  directionFor,
  onSort,
}: HeadProps<Row, K, Ctx>) {
  const filler = use(DataTableFillerContext);
  return (
    <>
      {layout.columns.map((col) => (
        <DataTableHead
          key={col.key}
          column={col}
          layout={layout}
          direction={directionFor(col.key)}
          onSort={() => {
            onSort(col.key);
          }}
        />
      ))}
      {/* DataTableFrame's filler: takes the slack, so no column is widened. */}
      {filler && <TableHead aria-hidden="true" className="p-0" />}
    </>
  );
}

/** The header row for a shadcn `<Table>`: goes straight inside TableHeader. */
export function DataTableHeadRow<Row, K extends string, Ctx>(
  props: HeadProps<Row, K, Ctx>,
) {
  return (
    <TableRow>
      <DataTableHeadCells {...props} />
    </TableRow>
  );
}
