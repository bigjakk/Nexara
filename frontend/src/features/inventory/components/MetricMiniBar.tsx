import { UsageBar } from "@/components/UsageBar";
import { USAGE_TEXT_CLASS, usageLevel } from "@/lib/usage-level";
import { cn } from "@/lib/utils";

interface MetricMiniBarProps {
  value: number | null;
  label?: string;
  /** False where there is no room for the bar: the number alone, coloured. */
  bar?: boolean;
}

/** A table cell's utilisation: the shared bar and the number beside it. */
export function MetricMiniBar({
  value,
  label,
  bar = true,
}: MetricMiniBarProps) {
  if (value === null) {
    return <span className="text-muted-foreground/60">—</span>;
  }

  const clamped = Math.max(0, Math.min(100, value));
  const display = `${String(Math.round(clamped))}%`;
  const level = usageLevel(clamped);

  return (
    <div className="flex items-center gap-1.5">
      {bar && <UsageBar percent={clamped} className="w-10 shrink-0" />}
      <span
        className={cn(
          "text-[12.5px] tabular-nums",
          bar && "min-w-8 text-right",
          level === "ok" ? "text-muted-foreground" : USAGE_TEXT_CLASS[level],
        )}
      >
        {label ? `${label} ${display}` : display}
      </span>
    </div>
  );
}
