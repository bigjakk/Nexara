import type { ReactNode } from "react";
import { TableCell, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";

/**
 * The full-width row a table opens underneath an expanded row.
 *
 * `colSpan` has to be the live column count rather than a constant: columns
 * drop out of the layout below `md`, and a detail row spanning more columns
 * than the table has renders a phantom cell past the last heading.
 */
export function ExpandedDetailRow({
  colSpan,
  spacing = "space-y-3",
  children,
}: {
  colSpan: number;
  /**
   * Gap between the blocks stacked inside.
   *
   * An enum rather than a class, so nobody can compute one: Tailwind's scanner
   * only emits a rule for a class it can see written out, and an interpolated
   * `space-y-${n}` would typecheck, lint, and render with no gap at all.
   */
  spacing?: "space-y-3" | "space-y-4";
  children: ReactNode;
}) {
  return (
    <TableRow>
      <TableCell colSpan={colSpan} className="bg-muted/30">
        <div className={cn(spacing, "px-2 py-3")}>{children}</div>
      </TableCell>
    </TableRow>
  );
}
