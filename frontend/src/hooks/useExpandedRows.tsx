import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import type { ColumnDef } from "@/hooks/useColumnLayout";

/**
 * Which rows of a table are open, for tables that let several open at once.
 *
 * `scope` is what the open set belongs to — the Veeam server whose rows these
 * are. Row ids are scoped to it, so when it changes the set has to be dropped:
 * it only ever grows, and a row expanded before the switch silently reopens on
 * the way back. Omit `scope` for a table whose rows are not scoped to anything
 * (the server list itself); the reset then never fires.
 *
 * The reset is a render-phase state adjustment rather than an effect. Row ids
 * are globally unique, so a stale set is inert against the new scope's rows
 * either way and nothing wrong would render — what this buys is one fewer
 * commit, and it is the shape React documents for adjusting state when a prop
 * changes.
 *
 * The real win is being a hook at all. The reset has to run before the
 * caller's early return — a scope with no rows still renders, and a component
 * that bailed out before recording the switch would come back to the first
 * scope believing nothing changed — and a hook cannot be called after one.
 * What used to be a comment asking the next editor not to move it is now
 * enforced by rules-of-hooks.
 */
export function useExpandedRows(scope?: string) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [expandedFor, setExpandedFor] = useState(scope);
  if (expandedFor !== scope) {
    setExpandedFor(scope);
    setExpanded(new Set());
  }

  function toggle(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  /**
   * Open a row without regard to its current state, for showing the operator
   * an answer they did not click for — a connection test writing its result
   * into a row that happens to be closed.
   */
  function expand(id: string) {
    setExpanded((prev) => new Set(prev).add(id));
  }

  return { expanded, toggle, expand };
}

/**
 * The leading chevron column for a table driven by useExpandedRows.
 *
 * Unlabelled, unsortable and `fixed` so a drag cannot deposit it in the middle
 * of the data — the affordance only reads as one while it is the first thing
 * in the row.
 *
 * Call it inside the module-scope COLUMNS array with the rest of them, never
 * in a component body: it returns a fresh object, and useColumnLayout needs
 * the array it is in to be a stable reference.
 */
export function expandColumn<
  Row extends { id: string },
  K extends string,
  Ctx extends { expanded: Set<string> },
>(key: K): ColumnDef<Row, K, Ctx> {
  return {
    key,
    label: "",
    width: 40,
    fixed: true,
    cell: (row, ctx) =>
      ctx.expanded.has(row.id) ? (
        <ChevronDown className="h-4 w-4" />
      ) : (
        <ChevronRight className="h-4 w-4" />
      ),
  };
}
