import type { ReactNode } from "react";
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react";
import { TableHead } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { SortDirection } from "@/hooks/useTableSort";

function ariaSort(
  direction: SortDirection | null,
): "none" | "ascending" | "descending" {
  if (direction === null) return "none";
  return direction === "asc" ? "ascending" : "descending";
}

function SortIcon({ direction }: { direction: SortDirection | null }) {
  if (direction === "asc") {
    return <ArrowUp className="h-3 w-3 shrink-0" />;
  }
  if (direction === "desc") {
    return <ArrowDown className="h-3 w-3 shrink-0" />;
  }
  // Neutral arrows appear only on hover or keyboard focus — a persistent icon
  // on seven of nine columns reads as noise — but they still occupy their
  // space, so the header does not reflow under the pointer.
  return (
    <ChevronsUpDown className="h-3 w-3 shrink-0 opacity-0 transition-opacity group-hover:opacity-50 group-focus-visible:opacity-50" />
  );
}

interface SortableTableHeadProps {
  children: ReactNode;
  /** The active direction for THIS column; null when another column is sorted. */
  direction: SortDirection | null;
  onSort: () => void;
  /** Match the column's cells — numeric columns are right-aligned. */
  align?: "left" | "right";
  className?: string;
}

/**
 * A `<TableHead>` whose label is a sort control.
 *
 * The active column's arrow is always shown, so the current ordering never
 * depends on where the pointer is.
 */
export function SortableTableHead({
  children,
  direction,
  onSort,
  align = "left",
  className,
}: SortableTableHeadProps) {
  return (
    <TableHead
      aria-sort={ariaSort(direction)}
      className={cn(align === "right" && "text-right", className)}
    >
      <button
        type="button"
        onClick={onSort}
        className={cn(
          "group inline-flex w-full items-center gap-1 font-medium transition-colors hover:text-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring rounded-sm",
          align === "right" && "justify-end",
        )}
      >
        {children}
        <SortIcon direction={direction} />
      </button>
    </TableHead>
  );
}
