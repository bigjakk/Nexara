import { Loader2 } from "lucide-react";
import { UsageBar } from "@/components/UsageBar";
import { Panel } from "@/components/page/Panel";
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
        <td
          colSpan={colSpan}
          className="px-[18px] py-2.5 text-[12.5px] text-destructive"
        >
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
          // What is happening now, on a faintly raised row; the measured
          // progress is in the data colour like every other bar.
          <tr key={job.device} className="border-b bg-foreground/[0.03]">
            <td colSpan={colSpan} className="px-[18px] py-2.5">
              <div data-testid="block-job" className="space-y-1.5">
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm">
                  <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin self-center text-muted-foreground" />
                  <span className="font-medium">
                    Block job: <span className="capitalize">{job.type}</span>
                  </span>
                  <span className="font-mono text-[12.5px] text-muted-foreground">
                    {job.device}
                  </span>
                  <span className="ml-auto tabular-nums">
                    {formatBytes(job.completed)} of {formatBytes(job.total)} (
                    {pct.toFixed(1)}%)
                  </span>
                </div>
                <UsageBar percent={pct} progress />
                <div className="flex flex-wrap gap-x-4 text-[12.5px] text-muted-foreground">
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
        <tr className="border-b bg-foreground/[0.03]">
          <td colSpan={colSpan} className="px-[18px] py-2.5">
            <p className="text-[12.5px] text-muted-foreground">
              QEMU also reported:
            </p>
            <pre className="mt-1 whitespace-pre-wrap font-mono text-[12.5px]">
              {data.unparsed.join("\n")}
            </pre>
          </td>
        </tr>
      )}
    </>
  );
}

/**
 * The same rows as a card of their own, for the Overview of someone who may
 * view the VM but has no History tab (view:audit and view:task): the block-job
 * route asks only view:vm. The same query as the History tab's, and nothing
 * shows while there is no job.
 */
export function LiveBlockJobsCard({
  clusterId,
  vmId,
}: {
  clusterId: string;
  vmId: string;
}) {
  const { data, error } = useBlockJobs(clusterId, vmId, true);
  if (!error && !data?.running) return null;
  return (
    <Panel flush title="Block jobs">
      <table className="w-full">
        <tbody>
          <LiveBlockJobRows clusterId={clusterId} vmId={vmId} colSpan={1} />
        </tbody>
      </table>
    </Panel>
  );
}
