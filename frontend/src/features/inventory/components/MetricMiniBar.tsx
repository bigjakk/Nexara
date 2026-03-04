import { cn } from "@/lib/utils";

interface MetricMiniBarProps {
  value: number | null;
  label?: string;
}

function getBarColor(value: number): string {
  if (value >= 90) return "bg-red-500";
  if (value >= 75) return "bg-yellow-500";
  return "bg-green-500";
}

export function MetricMiniBar({ value, label }: MetricMiniBarProps) {
  if (value === null) {
    return <span className="text-xs text-muted-foreground">--</span>;
  }

  const clamped = Math.max(0, Math.min(100, value));
  const display = `${String(Math.round(clamped))}%`;

  return (
    <div className="flex items-center gap-2">
      <div className="h-2 w-16 rounded-full bg-muted">
        <div
          className={cn("h-full rounded-full transition-all", getBarColor(clamped))}
          style={{ width: `${String(clamped)}%` }}
        />
      </div>
      <span className="text-xs tabular-nums text-muted-foreground">
        {label ? `${label} ${display}` : display}
      </span>
    </div>
  );
}
