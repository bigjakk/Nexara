import { cn } from "@/lib/utils";

const STATUS_LABEL: Record<string, string> = {
  pending: "Pending",
  checking: "Checking",
  migrating: "Migrating",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};

/**
 * A migration job's state in words. A job on the move is the data colour, a
 * failed one red; pending, completed and cancelled are plain text.
 */
export function MigrationStatusText({ status }: { status: string }) {
  return (
    <span
      className={cn(
        "whitespace-nowrap",
        status === "checking" || status === "migrating"
          ? "font-medium text-data"
          : status === "failed"
            ? "font-medium text-red-700 dark:text-red-400"
            : "text-muted-foreground",
      )}
    >
      {STATUS_LABEL[status] ?? status}
    </span>
  );
}
