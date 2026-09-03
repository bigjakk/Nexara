import { TableCell } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { ColumnLayout } from "@/hooks/useColumnLayout";

/**
 * One row's cells, rendered from the same ordered column list as the headings.
 *
 * This is what makes a reorder actually reorder: both the header row and every
 * body row map over `layout.columns`, so a dragged heading takes its values
 * with it. A table that hand-writes its `<td>` sequence cannot follow the drag,
 * and the values end up under the wrong headings.
 *
 * `ctx` is whatever the cells need beyond the row itself — handlers,
 * permission flags, a translator. Pass `undefined` for columns declared
 * without a context type.
 */
export function DataTableCells<Row, K extends string, Ctx>({
  row,
  layout,
  ctx,
}: {
  row: Row;
  layout: ColumnLayout<Row, K, Ctx>;
  ctx: Ctx;
}) {
  return (
    <>
      {layout.columns.map((col) => (
        <TableCell
          key={col.key}
          // Fixed, user-chosen widths mean a long value must clip rather than
          // force its column wider than the width that was dragged for it —
          // except where the content is the only copy of something diagnostic,
          // which wraps instead (see ColumnDef.wrap).
          className={cn(
            col.wrap ? "align-top break-words whitespace-normal" : "truncate",
            col.align === "right" && "text-right",
          )}
        >
          {col.cell(row, ctx)}
        </TableCell>
      ))}
    </>
  );
}
