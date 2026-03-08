import { Clock, Loader2 } from "lucide-react";
import {
  useCVEScanSchedule,
  useUpdateCVEScanSchedule,
} from "../api/cve-queries";

const INTERVAL_OPTIONS = [
  { label: "Every hour", value: 1 },
  { label: "Every 6 hours", value: 6 },
  { label: "Every 12 hours", value: 12 },
  { label: "Every 24 hours", value: 24 },
  { label: "Every 48 hours", value: 48 },
  { label: "Weekly", value: 168 },
] as const;

export function ScanScheduleCard({ clusterId }: { clusterId: string }) {
  const { data: schedule, isLoading } = useCVEScanSchedule(clusterId);
  const updateSchedule = useUpdateCVEScanSchedule();

  if (isLoading) {
    return (
      <div className="h-24 animate-pulse rounded-lg border bg-card" />
    );
  }

  const enabled = schedule?.enabled ?? true;
  const intervalHours = schedule?.interval_hours ?? 24;

  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Clock className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-semibold">Scan Schedule</h3>
        </div>

        {updateSchedule.isPending && (
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        )}
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-4">
        {/* Enable/disable toggle */}
        <label className="flex items-center gap-2 text-sm">
          <button
            type="button"
            role="switch"
            aria-checked={enabled}
            onClick={() =>
              updateSchedule.mutate({
                clusterId,
                enabled: !enabled,
                intervalHours,
              })
            }
            className={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors ${
              enabled ? "bg-primary" : "bg-muted"
            }`}
          >
            <span
              className={`pointer-events-none inline-block h-4 w-4 rounded-full bg-white shadow transition-transform ${
                enabled ? "translate-x-4" : "translate-x-0"
              }`}
            />
          </button>
          <span className="text-muted-foreground">
            {enabled ? "Automatic scanning enabled" : "Automatic scanning disabled"}
          </span>
        </label>

        {/* Interval selector */}
        {enabled && (
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Interval:</span>
            <select
              value={intervalHours}
              onChange={(e) =>
                updateSchedule.mutate({
                  clusterId,
                  enabled,
                  intervalHours: Number(e.target.value),
                })
              }
              className="rounded-md border bg-background px-2 py-1 text-sm"
            >
              {INTERVAL_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </div>
        )}
      </div>
    </div>
  );
}
