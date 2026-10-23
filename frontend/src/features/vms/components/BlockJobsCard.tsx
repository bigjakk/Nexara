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
 * stream, a backup — read from QEMU's monitor while the card is open. A QEMU too stuck to answer (a hung storage backend, say) shows as the
 * error, which is itself the answer.
 */
export function BlockJobsCard({
  clusterId,
  vmId,
}: {
  clusterId: string;
  vmId: string;
}) {
  const { data, error, isPending, dataUpdatedAt } = useBlockJobs(
    clusterId,
    vmId,
    true,
  );
  const pace = useJobPace(data?.jobs, dataUpdatedAt);

  let body;
  // Pending, not loading: a first fetch paused while the browser is offline
  // is pending without loading, and must not read as "Not running".
  if (isPending) {
    body = <p className="text-sm text-muted-foreground">Checking...</p>;
  } else if (error) {
    body = (
      <p className="text-sm text-destructive">
        {error instanceof ApiClientError
          ? error.message
          : "Could not read the block jobs."}
      </p>
    );
  } else if (!data.running) {
    body = <p className="text-sm text-muted-foreground">Not running</p>;
  } else if (data.jobs.length === 0 && data.unparsed.length === 0) {
    body = (
      <p className="text-sm text-muted-foreground">No active block jobs</p>
    );
  } else {
    body = (
      <div className="space-y-3">
        {data.jobs.map((job) => {
          const pct = job.total > 0 ? (job.completed / job.total) * 100 : 0;
          const p = pace.get(job.device);
          return (
            <div key={job.device} data-testid="block-job" className="space-y-1">
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm">
                <span className="font-medium capitalize">{job.type}</span>
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
          );
        })}
        {data.unparsed.length > 0 && (
          <div>
            <p className="text-xs text-muted-foreground">QEMU also reported:</p>
            <pre className="mt-1 whitespace-pre-wrap font-mono text-xs">
              {data.unparsed.join("\n")}
            </pre>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="rounded-lg border p-4">
      <h3 className="mb-3 text-sm font-semibold">Block jobs</h3>
      {body}
    </div>
  );
}
