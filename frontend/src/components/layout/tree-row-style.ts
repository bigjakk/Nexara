import type { CSSProperties } from "react";
import { cn } from "@/lib/utils";

/** The left gutter of a top-level row, and how far each level steps in. */
const GUTTER = 10;
const STEP = 14;
/** A chevron and the gap after it: what a leaf leaves blank to line up. */
const CHEVRON_SLOT = 20;

/**
 * A row's left padding. Rows run the full width of the panel and step in by
 * padding, so the selected row's tint and rule reach both edges at any depth.
 */
export function treeIndent(depth: number, leaf = false): CSSProperties {
  return { paddingLeft: GUTTER + depth * STEP + (leaf ? CHEVRON_SLOT : 0) };
}

/**
 * One 28px row of the inventory tree. `active` is the page that is open: the
 * primary colour at low strength and a 2px rule at the panel's left edge.
 */
export function treeRowClass({
  active = false,
  dropActive = false,
}: {
  active?: boolean;
  /** A dragged guest is over this row and can be dropped on it. */
  dropActive?: boolean;
} = {}): string {
  return cn(
    "group/row relative flex h-7 w-full min-w-0 items-center gap-1.5 pr-2 text-left text-[13px] text-foreground/85 outline-hidden transition-colors hover:bg-foreground/[0.05] focus-visible:bg-foreground/[0.07]",
    active &&
      "bg-primary/10 font-medium text-foreground before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:bg-primary hover:bg-primary/[0.14]",
    dropActive && "bg-primary/15 ring-1 ring-inset ring-primary",
  );
}

/** The part of a branch row that opens its page: everything but the chevron. */
export const TREE_ROW_MAIN =
  "flex h-full min-w-0 flex-1 items-center gap-1.5 rounded-sm text-left outline-hidden focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring";

/** The button around a branch row's chevron. */
export const TREE_ROW_TOGGLE =
  "grid h-5 w-3.5 shrink-0 place-items-center rounded-sm text-muted-foreground outline-hidden hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring";
