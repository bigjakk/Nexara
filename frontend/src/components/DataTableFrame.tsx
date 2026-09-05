import type { ReactNode } from "react";
import { Table } from "@/components/ui/table";
import type { ColumnLayout } from "@/hooks/useColumnLayout";

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
      <Table className="table-fixed" style={{ width: layout.totalWidth }}>
        {children}
      </Table>
    </div>
  );
}
