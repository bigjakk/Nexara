import { cn } from "@/lib/utils";

const severityConfig = {
  critical: { label: "Critical", className: "bg-red-600 text-white" },
  high: { label: "High", className: "bg-orange-500 text-white" },
  medium: { label: "Medium", className: "bg-yellow-500 text-black" },
  low: { label: "Low", className: "bg-blue-500 text-white" },
  unknown: { label: "Unknown", className: "bg-gray-500 text-white" },
} as const;

interface SeverityBadgeProps {
  severity: string;
  count?: number;
  className?: string;
}

export function SeverityBadge({ severity, count, className }: SeverityBadgeProps) {
  const config = (severity in severityConfig ? severityConfig[severity as keyof typeof severityConfig] : severityConfig.unknown);

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-semibold",
        config.className,
        className,
      )}
    >
      {config.label}
      {count !== undefined && <span>({count})</span>}
    </span>
  );
}
