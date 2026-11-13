import { cn } from "@/lib/utils";

// Red and amber are for what needs acting on; the rest are plain chips.
const severityConfig = {
  critical: {
    label: "Critical",
    className: "bg-red-500/15 text-red-700 dark:text-red-400",
  },
  high: {
    label: "High",
    className: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  },
  medium: { label: "Medium", className: "bg-foreground/[0.08]" },
  low: {
    label: "Low",
    className: "bg-foreground/[0.05] text-muted-foreground",
  },
  unknown: {
    label: "Unknown",
    className: "bg-foreground/[0.05] text-muted-foreground",
  },
} as const;

interface SeverityBadgeProps {
  severity: string;
  count?: number;
  className?: string;
}

export function SeverityBadge({
  severity,
  count,
  className,
}: SeverityBadgeProps) {
  const config =
    severity in severityConfig
      ? severityConfig[severity as keyof typeof severityConfig]
      : severityConfig.unknown;

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 whitespace-nowrap rounded-md px-1.5 py-0.5 text-xs font-medium",
        config.className,
        className,
      )}
    >
      {config.label}
      {count !== undefined && <span className="tabular-nums">{count}</span>}
    </span>
  );
}
