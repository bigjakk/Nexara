import type { DisplayStatus } from "@/components/layout/task-status";

const FILL: Record<DisplayStatus, string> = {
  running: "bg-blue-500",
  ok: "bg-emerald-500",
  failed: "bg-red-500",
};
const TEXT: Record<DisplayStatus, string> = {
  running: "text-blue-500",
  ok: "text-muted-foreground",
  failed: "text-red-500",
};

/**
 * The progress readout shared by the Tasks tables and the Activity panel.
 *
 * A running task with no reported fraction gets an indeterminate bar rather
 * than an empty one — Proxmox reports no progress for most task types, and a
 * still track next to a spinning status icon reads as stalled. A terminal task
 * with none gets an em dash: there is nothing left to finish, so neither a bar
 * nor a 0% readout would be true.
 *
 * `value` comes from displayProgress(); pass null for a row that is not a task
 * at all, which renders the same em dash.
 */
export function TaskProgressCell({
  display,
  value,
}: {
  /** null for a non-task row (an audit entry with no UPID). */
  display: DisplayStatus | null;
  value: number | null;
}) {
  // A non-task row has no fraction either — decorateActivity passes null for
  // both — so the two cases collapse: without a status there is no fill colour
  // to pick, and the drawn bar below can take `display` as non-null.
  if (display === null || value === null) {
    if (display !== "running") {
      return <span className="text-muted-foreground">—</span>;
    }
    return (
      <div
        className="flex items-center gap-1.5"
        title="Proxmox reports no progress for this task type"
      >
        <div className="h-1.5 w-16 overflow-hidden rounded-full bg-muted">
          <div className="h-full w-[30%] animate-task-indeterminate rounded-full bg-blue-500" />
        </div>
        <span className="text-[10px] tabular-nums text-muted-foreground">
          —
        </span>
      </div>
    );
  }

  const pct = Math.round(value * 100);
  return (
    <div className="flex items-center gap-1.5">
      <div className="h-1.5 w-16 overflow-hidden rounded-full bg-muted">
        <div
          className={`h-full rounded-full transition-all duration-500 ${FILL[display]}`}
          style={{ width: `${String(pct)}%` }}
        />
      </div>
      <span className={`text-[10px] tabular-nums ${TEXT[display]}`}>
        {pct}%
      </span>
    </div>
  );
}
