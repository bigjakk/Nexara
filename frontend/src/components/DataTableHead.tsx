import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react";
import { TableHead } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { SortDirection } from "@/hooks/useTableSort";
import type { ColumnDef, ColumnLayout } from "@/hooks/useColumnLayout";

function ariaSort(
  direction: SortDirection | null,
): "none" | "ascending" | "descending" {
  if (direction === null) return "none";
  return direction === "asc" ? "ascending" : "descending";
}

function SortIcon({ direction }: { direction: SortDirection | null }) {
  if (direction === "asc") return <ArrowUp className="h-3 w-3 shrink-0" />;
  if (direction === "desc") return <ArrowDown className="h-3 w-3 shrink-0" />;
  // Neutral arrows appear only on hover or keyboard focus — a persistent icon
  // on every column reads as noise — but they still occupy their space, so the
  // header does not reflow under the pointer.
  return (
    <ChevronsUpDown className="h-3 w-3 shrink-0 opacity-0 transition-opacity group-hover:opacity-50 group-focus-visible:opacity-50" />
  );
}

/**
 * A column heading that sorts, resizes and reorders.
 *
 * Three gestures share one element, so they are separated by intent rather
 * than by target:
 *   * a press on the right-edge grip resizes (it stops propagation, so it
 *     never also starts a drag);
 *   * a press on the label that travels past a few pixels reorders;
 *   * a press that does not travel sorts.
 * That last rule is what keeps the sorting shipped earlier working with a
 * shaky hand rather than demanding a perfectly still click.
 *
 * Keyboard users are not asked to drag: Enter/Space sorts, and the grip is not
 * a tab stop. Reordering and resizing are pointer affordances over a layout
 * that is already usable without them, and the Reset control restores the
 * default for anyone who cannot undo a drag by dragging.
 */
export function DataTableHead<Row, K extends string, Ctx>({
  column,
  layout,
  direction,
  onSort,
}: {
  column: ColumnDef<Row, K, Ctx>;
  layout: ColumnLayout<Row, K, Ctx>;
  /** The active direction for THIS column; null when another is sorted. */
  direction: SortDirection | null;
  onSort: () => void;
}) {
  // Explicit `sortable` wins, so a server-sorted column with no local
  // accessor still gets its control; otherwise having an accessor is what
  // makes a column sortable.
  const sortable = column.sortable ?? column.sortValue !== undefined;
  const isDragging = layout.draggingKey === column.key;
  const drop =
    layout.dropTarget?.key === column.key ? layout.dropTarget.side : null;

  return (
    <TableHead
      // Read by the reorder drag to identify which heading the pointer is over.
      data-column-key={column.key}
      aria-sort={sortable ? ariaSort(direction) : undefined}
      style={{ width: layout.widths[column.key] }}
      className={cn(
        "relative select-none",
        column.align === "right" && "text-right",
        isDragging && "opacity-40",
        drop === "before" && "shadow-[inset_2px_0_0_0_hsl(var(--primary))]",
        drop === "after" && "shadow-[inset_-2px_0_0_0_hsl(var(--primary))]",
        column.className,
      )}
    >
      <button
        type="button"
        // The press is handled on pointerdown so a drag can start, which means
        // this cannot rely on the click event. onKeyDown keeps it operable
        // from the keyboard, where there is no drag to disambiguate.
        onPointerDown={(e) => {
          // Only a primary-button press; a right-click should open the context
          // menu, not begin a drag.
          if (e.button !== 0) return;
          layout.startReorder(column.key, e, () => {
            if (sortable) onSort();
          });
        }}
        onKeyDown={(e) => {
          if (!sortable) return;
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onSort();
          }
        }}
        className={cn(
          "group inline-flex w-full items-center gap-1 rounded-sm font-medium transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-hidden",
          column.align === "right" && "justify-end",
          column.fixed ? "cursor-default" : "cursor-grab active:cursor-grabbing",
        )}
      >
        <span className="truncate">{column.label}</span>
        {sortable && <SortIcon direction={direction} />}
      </button>

      {/* Resize grip. Sits half outside the cell so the hit area straddles the
          boundary the user is aiming at, and is hidden from assistive tech and
          the tab order — it does nothing a keyboard user can act on. */}
      <span
        role="presentation"
        aria-hidden="true"
        onPointerDown={(e) => {
          if (e.button !== 0) return;
          layout.startResize(column.key, e);
        }}
        className={cn(
          "absolute top-0 -right-1 z-10 h-full w-2 cursor-col-resize touch-none",
          "after:absolute after:top-0 after:left-1 after:h-full after:w-px after:bg-transparent hover:after:bg-primary",
          layout.resizingKey === column.key && "after:bg-primary",
        )}
      />
    </TableHead>
  );
}
