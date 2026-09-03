import type { ReactNode } from "react";

const VALUE_CLASS = {
  mono: "font-mono",
  muted: "text-muted-foreground",
  plain: undefined,
} as const;

/**
 * One `label / value` pair inside a `<dl>` detail block.
 *
 * The expanded row under a table is where the fields that did not fit in a
 * column go, and every one of them is the same three lines of `<div><dt><dd>`.
 * Written by hand they drift: a `text-xs` missed here, a `font-mono` added
 * there, and two detail blocks on the same screen stop looking related.
 *
 * The value's styling is declared, not passed as a class, for the same reason
 * ColumnDef has no className: a free-form hook invites a layout rule into a
 * place that has no business carrying one.
 */
export function DetailField({
  label,
  variant,
  children,
}: {
  label: string;
  /**
   * What kind of value this is.
   *
   * `mono` is machine data — a duration, a size, a version — monospaced so
   * digits line up down the column. `muted` is not a value at all but a note
   * about its absence, so "Never" or "Hidden" reads as the placeholder it is.
   *
   * One prop rather than two booleans: they are exhaustive and exclusive, and
   * two of them would make a caller spell the same predicate twice, negated,
   * where updating one and not the other is silent.
   */
  variant?: "mono" | "muted";
  children: ReactNode;
}) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      {/* Undefined, not "", so a plain value renders `<dd>` and not
          `<dd class="">`. */}
      <dd className={VALUE_CLASS[variant ?? "plain"]}>{children}</dd>
    </div>
  );
}
