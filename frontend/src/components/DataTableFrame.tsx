import type { ReactNode } from "react";
import { Table } from "@/components/ui/table";
import type { ColumnLayout } from "@/hooks/useColumnLayout";
import { DataTableFillerContext } from "@/components/data-table-filler";

/**
 * The frame a resizable table has to have: `table-fixed` plus the layout's own
 * total width.
 *
 * Those two travel together or the feature breaks. Without `table-fixed` the
 * browser ignores the column widths; with it but without the explicit width,
 * the fixed layout spreads the container's slack across the columns, so a
 * column renders wider than the width just dragged for it and the resize grip
 * drifts out from under the pointer. Declaring the pair once means a new table
 * cannot adopt useColumnLayout and get half of it.
 *
 * In a panel wider than the columns, the table still spans the panel
 * (min-width 100%) so its row rules reach the edge: the slack goes to a
 * trailing filler cell with no width of its own, which DataTableHeadCells and
 * DataTableCells add inside this frame, so no column is widened. A row that
 * spans the columns by hand (colSpan={layout.columns.length}) leaves the
 * filler's slot empty.
 */
export function DataTableFrame<Row, K extends string, Ctx>({
  layout,
  children,
}: {
  layout: ColumnLayout<Row, K, Ctx>;
  children: ReactNode;
}) {
  return (
    <div className="overflow-x-auto">
      <Table
        className="table-fixed"
        style={{ width: layout.totalWidth, minWidth: "100%" }}
      >
        <DataTableFillerContext value={true}>{children}</DataTableFillerContext>
      </Table>
    </div>
  );
}
