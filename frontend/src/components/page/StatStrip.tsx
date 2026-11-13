import type { ReactNode } from "react";
import { Sparkline } from "@/components/Sparkline";
import { UsageBar } from "@/components/UsageBar";
import { cn } from "@/lib/utils";

/**
 * A row of headline numbers divided by hairlines, in place of a row of stat
 * cards. Wraps to as many columns as fit; the clipped negative margin hides
 * the rule at the start of each line.
 */
export function StatStrip({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("overflow-hidden", className)}>
      <div className="-ml-px grid grid-cols-[repeat(auto-fit,minmax(min(9.5rem,100%),1fr))] gap-y-5">
        {children}
      </div>
    </div>
  );
}

export type StatTone = "default" | "quiet" | "warn" | "bad";

const TONE_CLASS: Record<StatTone, string> = {
  default: "",
  // A zero, or a number with nothing to say: present, not emphasised.
  quiet: "text-muted-foreground",
  warn: "text-amber-700 dark:text-amber-400",
  bad: "text-red-700 dark:text-red-400",
};

interface StatProps {
  label: ReactNode;
  value: ReactNode;
  /** A line of context under the value. */
  sub?: ReactNode;
  /** Colour is state: leave it default unless the number is a problem. */
  tone?: StatTone;
  /** Recent readings, drawn as a sparkline under the number. */
  trend?: readonly number[] | undefined;
  /** The sparkline's ceiling, e.g. 100 for a percentage. */
  trendMax?: number | undefined;
  /** 0–100, drawn as a usage bar under the number. Null draws an empty track. */
  percent?: number | null | undefined;
  /** With onSelect, the cell is a toggle button (it picks the chart below). */
  selected?: boolean | undefined;
  onSelect?: (() => void) | undefined;
  className?: string;
}

export function Stat({
  label,
  value,
  sub,
  tone = "default",
  trend,
  trendMax,
  percent,
  selected,
  onSelect,
  className,
}: StatProps) {
  const body = (
    <>
      <div className="truncate text-sm text-muted-foreground">{label}</div>
      <div
        className={cn(
          "mt-0.5 truncate text-2xl font-[650] leading-[1.15] [font-stretch:108%]",
          TONE_CLASS[tone],
        )}
        // A long reading ("1023.9 KiB/s") can be cut off in a narrow cell.
        title={typeof value === "string" ? value : undefined}
      >
        {value}
      </div>
      {sub !== undefined && (
        <div
          className="truncate text-[12.5px] text-muted-foreground/75"
          title={typeof sub === "string" ? sub : undefined}
        >
          {sub}
        </div>
      )}
      {percent !== undefined && (
        <UsageBar percent={percent} className="mt-2.5" />
      )}
      {trend !== undefined && trend.length >= 2 && (
        // Pinned to the foot of the cell, so the trends of one row share a
        // baseline when one cell's context runs to a second line.
        <div className="mt-auto pt-2">
          <Sparkline
            points={[...trend]}
            max={trendMax}
            className="h-7 w-full text-data"
          />
        </div>
      )}
    </>
  );
  if (onSelect !== undefined) {
    return (
      <button
        type="button"
        aria-pressed={selected === true}
        onClick={onSelect}
        className={cn(
          "relative flex min-w-0 flex-col items-stretch border-l px-[18px] pb-4 text-left outline-hidden transition-colors hover:bg-foreground/[0.03] focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
          // The selected cell wears a rule along its bottom edge, as the
          // selected tab does: what it selects is drawn right below.
          "after:absolute after:inset-x-[18px] after:bottom-0 after:h-0.5 after:rounded-full aria-pressed:after:bg-foreground",
          className,
        )}
      >
        {body}
      </button>
    );
  }
  return (
    <div className={cn("flex min-w-0 flex-col border-l px-[18px]", className)}>
      {body}
    </div>
  );
}
