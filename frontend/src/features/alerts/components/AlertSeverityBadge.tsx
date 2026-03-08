import { cn } from "@/lib/utils";

const severityStyles: Record<string, string> = {
  critical: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  warning: "bg-orange-100 text-orange-800 dark:bg-orange-900/30 dark:text-orange-400",
  info: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
};

export function AlertSeverityBadge({ severity }: { severity: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium capitalize",
        severityStyles[severity] ?? "bg-muted text-muted-foreground",
      )}
    >
      {severity}
    </span>
  );
}
