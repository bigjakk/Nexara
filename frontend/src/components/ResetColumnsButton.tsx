import { RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { ColumnLayout } from "@/hooks/useColumnLayout";

/**
 * Restores a table's default column order and widths.
 *
 * Not decoration: a drag can put a table into a state a drag cannot easily
 * undo — a column shoved to the far edge, or every column squeezed to the
 * minimum — and the layout persists across reloads, so without this the user
 * would be stuck with it. It is also the only way back for anyone who cannot
 * use the pointer gestures at all.
 *
 * Hidden until there is something to undo, so it does not sit on every table
 * offering to reset a layout nobody has touched.
 */
export function ResetColumnsButton<Row, K extends string, Ctx>({
  layout,
  className,
}: {
  layout: ColumnLayout<Row, K, Ctx>;
  className?: string;
}) {
  if (!layout.isCustomized) return null;
  return (
    <Button
      variant="ghost"
      size="sm"
      className={className}
      onClick={layout.reset}
      title="Restore the default column order and widths"
    >
      <RotateCcw className="mr-1 h-3 w-3" />
      Reset columns
    </Button>
  );
}
