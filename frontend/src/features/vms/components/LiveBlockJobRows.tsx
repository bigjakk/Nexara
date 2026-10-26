import { Loader2 } from "lucide-react";
import { formatBytes, formatBytesPerSecond } from "@/lib/format";
import { ApiClientError } from "@/lib/api-client";
import { useBlockJobs } from "../api/vm-queries";
import { useJobPace } from "../lib/job-pace";

function formatEta(seconds: number): string {
  if (seconds < 60) return `${String(Math.ceil(seconds))} s`;
  const minutes = Math.ceil(seconds / 60);
  if (minutes < 60) return `${String(minutes)} min`;
  return `${String(Math.floor(minutes / 60))} h ${String(minutes % 60)} min`;
}

/**
 * A running VM's QEMU block jobs — a disk move's mirror, a live restore's
 * stream, a backup — as live rows at the top of its History, read from QEMU's
 * monitor while the tab is open. Nothing renders while there are none: the
 * jobs are what is happening now, and the history below says what happened.
 *
 * A QEMU too stuck to answer (a hung storage backend, say) shows as the
 * error, which is itself the answer.
 */
export function LiveBlockJobRows({
  clusterId,
  vmId,
  colSpan,
}: {
  clusterId: string;
  vmId: string;
  /** The table's column count, which each live row spans. */
  colSpan: number;
}) {
  const { data, error, dataUpdatedAt } = useBlockJobs(clusterId, vmId, true);
  const pace = useJobPace(data?.jobs, dataUpdatedAt);

  if (error) {
    return (
      <tr className="border-b">
        <td colSpan={colSpan} className="px-4 py-2 text-xs text-destructive">
          {error instanceof ApiClientError
            ? error.message
            : "Could not read the block jobs."}
        </td>
      </tr>
    );
  }
  if (!data?.running) return null;

  return (
    <>
      {data.jobs.map((job) => {
        const pct = job.total > 0 ? (job.completed / job.total) * 100 : 0;
        const p = pace.get(job.device);
        return (
          <tr key={job.device} className="border-b bg-blue-500/5">
            <td colSpan={colSpan} className="px-4 py-2">
              <div data-testid="block-job" className="space-y-1">
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm">
                  <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin self-center text-blue-500" />
                  <span className="font-medium">
                    Block job: <span className="capitalize">{job.type}</span>
                  </span>
                  <span className="font-mono text-xs text-muted-foreground">
                    {job.device}
                  </span>
                  <span className="ml-auto tabular-nums">
                    {formatBytes(job.completed)} of {formatBytes(job.total)} (
                    {pct.toFixed(1)}%)
                  </span>
                </div>
                <div className="h-1.5 w-full overflow-hidden rounded bg-muted">
                  <div
                    className="h-full bg-primary"
                    style={{ width: `${String(Math.min(100, pct))}%` }}
                  />
                </div>
                <div className="flex flex-wrap gap-x-4 text-xs text-muted-foreground">
                  <span>
                    Speed limit:{" "}
                    {job.speed_limit > 0
                      ? formatBytesPerSecond(job.speed_limit)
                      : "none"}
                  </span>
                  {p && <span>Rate: {formatBytesPerSecond(p.bps)}</span>}
                  {p?.etaSeconds != null && (
                    <span>About {formatEta(p.etaSeconds)} left</span>
                  )}
                </div>
              </div>
            </td>
          </tr>
        );
      })}
      {data.unparsed.length > 0 && (
        <tr className="border-b bg-blue-500/5">
          <td colSpan={colSpan} className="px-4 py-2">
            <p className="text-xs text-muted-foreground">QEMU also reported:</p>
            <pre className="mt-1 whitespace-pre-wrap font-mono text-xs">
              {data.unparsed.join("\n")}
            </pre>
          </td>
        </tr>
      )}
    </>
  );
}
