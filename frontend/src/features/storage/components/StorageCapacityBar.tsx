import { UsageBar } from "@/components/UsageBar";
import { formatBytes } from "@/lib/format";
import { USAGE_TEXT_CLASS, usageLevel } from "@/lib/usage-level";
import { cn } from "@/lib/utils";

interface StorageCapacityBarProps {
  used: number;
  total: number;
  className?: string;
}

/**
 * A store's used space in words over the shared usage bar. A store that
 * reports no size (it is inactive) says so over an empty track, never 0%.
 */
export function StorageCapacityBar({
  used,
  total,
  className,
}: StorageCapacityBarProps) {
  const percent = total > 0 ? (used / total) * 100 : null;
  return (
    <div className={cn("min-w-0 space-y-1.5", className)}>
      <div className="flex items-baseline justify-between gap-3 text-[12.5px] tabular-nums text-muted-foreground">
        <span className="truncate">
          {percent === null
            ? "No size reported"
            : `${formatBytes(used)} of ${formatBytes(total)}`}
        </span>
        {percent !== null && (
          <span
            className={cn(
              "text-foreground",
              USAGE_TEXT_CLASS[usageLevel(percent)],
            )}
          >
            {String(Math.round(percent))}%
          </span>
        )}
      </div>
      <UsageBar percent={percent} />
    </div>
  );
}
