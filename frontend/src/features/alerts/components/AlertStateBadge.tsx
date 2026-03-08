import { cn } from "@/lib/utils";

const stateStyles: Record<string, string> = {
  pending: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
  firing: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  acknowledged: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
  resolved: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
};

export function AlertStateBadge({ state }: { state: string }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium capitalize",
        stateStyles[state] ?? "bg-muted text-muted-foreground",
      )}
    >
      {state}
    </span>
  );
}
