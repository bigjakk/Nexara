import { cn } from "@/lib/utils";
import { usageLevel, type UsageLevel } from "@/lib/usage-level";

/**
 * A utilisation bar in the data colour, amber and red at the shared
 * thresholds. Null (no reading yet) draws an empty track, never 0%.
 * Decorative: the number beside it is what a screen reader gets.
 */
export function UsageBar({
  percent,
  thresholds = true,
  progress = false,
  level: givenLevel,
  className,
}: {
  percent: number | null;
  /** False for a bar that measures a share or a charge, not something filling up. */
  thresholds?: boolean;
  /**
   * A job's progress, not a measurement: drawn in the "under way" colour,
   * which no accent recolours, and never at a threshold.
   */
  progress?: boolean;
  /**
   * The state to draw, where the caller knows it and the thresholds do not:
   * a battery that is draining is amber however full it is.
   */
  level?: UsageLevel | undefined;
  className?: string;
}) {
  const clamped = percent === null ? null : Math.min(100, Math.max(0, percent));
  const level =
    givenLevel ?? (thresholds && !progress ? usageLevel(clamped) : "ok");
  return (
    <div
      aria-hidden="true"
      className={cn(
        "h-1.5 min-w-8 overflow-hidden rounded-full bg-foreground/10",
        className,
      )}
    >
      {clamped !== null && (
        <div
          className={cn(
            "h-full rounded-full transition-[width] duration-700",
            level === "err"
              ? "bg-red-500"
              : level === "warn"
                ? "bg-amber-500"
                : progress
                  ? "bg-info"
                  : "bg-data",
          )}
          style={{ width: `${String(Math.round(clamped))}%` }}
        />
      )}
    </div>
  );
}
