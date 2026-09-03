import { Fragment } from "react";
import { TableBody, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { formatBytes, formatDateTime } from "@/lib/format";
import { byId } from "@/hooks/useTableSort";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { expandColumn, useExpandedRows } from "@/hooks/useExpandedRows";
import {
  bottleneckLabel,
  processingRateLabel,
  resultVariant,
} from "./veeam-format";
import { VeeamJobActions } from "./VeeamJobActions";
import { VeeamTaskTable } from "./VeeamTaskTable";
import type { VeeamJob } from "../types/backup";

interface VeeamJobTableProps {
  jobs: VeeamJob[];
  /** The server these jobs belong to. Job control posts against it. */
  serverId: string;
}

/**
 * "Failed or cancelled", never a bare "Failed".
 *
 * Veeam records a job cancelled through its own API as result "Failed" with
 * isCanceled false and an empty session log, so there is genuinely no way to
 * tell an operator's stop from a real failure. Labelling it "Failed" would be
 * a confident wrong answer.
 */
function resultLabel(result: string): string {
  return result === "Failed" ? "Failed or cancelled" : result;
}

/**
 * Whether the job is running, by the same rule the Status cell paints.
 *
 * job.status trails the inventory pass by minutes, while a run started from
 * Nexara exists as a session immediately — so a job can be running with
 * status still "Stopped". Shared with the sort accessor below: a column that
 * ordered on the raw field would put a visibly-Running job under "Stopped".
 */
function isRunning(job: VeeamJob): boolean {
  return job.status === "Running" || job.running_session_id !== "";
}

/** Epoch ms, so date columns order chronologically rather than by their text. */
function toEpoch(value: string | null): number | null {
  if (value == null || value === "") return null;
  const parsed = new Date(value).getTime();
  return Number.isNaN(parsed) ? null : parsed;
}

type JobSortKey =
  | "expand"
  | "name"
  | "status"
  | "result"
  | "lastRun"
  | "nextRun"
  | "repository"
  | "guests"
  | "actions";

/** What the chevron and action cells need beyond the job row itself. */
interface JobCtx {
  serverId: string;
  expanded: Set<string>;
}

/**
 * Domain order for the two badge columns, not alphabetical.
 *
 * By label, Status ascending gives Disabled, Inactive, Running, Stopped —
 * burying the jobs actually in flight in third place — and Result ascending
 * only happens to put Failed first, which would quietly stop being true the
 * next time a label is reworded. Both cells still show their label; only the
 * ordering is ranked.
 */
const JOB_STATUS_RANK: Record<string, number> = {
  Running: 0,
  Stopped: 1,
  Inactive: 2,
  Disabled: 3,
};

const JOB_RESULT_RANK: Record<string, number> = {
  "Failed or cancelled": 0,
  Warning: 1,
  Success: 2,
};

/** Each column sorts on what its cell SHOWS, not on the underlying field. */
const COLUMNS: ColumnDef<VeeamJob, JobSortKey, JobCtx>[] = [
  expandColumn("expand"),
  {
    key: "name",
    label: "Job",
    width: 220,
    sortValue: (job) => job.name,
    cell: (job) => <span className="font-medium">{job.name}</span>,
  },
  {
    key: "status",
    label: "Status",
    width: 170,
    sortValue: (job) => {
      if (isRunning(job)) return JOB_STATUS_RANK["Running"] ?? 0;
      if (job.status === "") return null;
      return JOB_STATUS_RANK[job.status] ?? 99;
    },
    cell: (job) => {
      // Whether progress_percent describes THIS run. It is written by the same
      // pass that writes status, so it is only trustworthy when status agrees
      // a run is in flight.
      const measured = job.status === "Running";
      if (!isRunning(job)) {
        return <Badge variant="secondary">{job.status || "-"}</Badge>;
      }
      return (
        <div className="flex items-center gap-2">
          <Badge variant="default">Running</Badge>
          {/* The bar renders ONLY when Veeam itself reports the job as
              running, because progress_percent is refreshed by the same
              inventory pass that sets status. For a run derived from a live
              session that number is the PREVIOUS run's — normally 100 — so
              drawing it would show a backup that started seconds ago as
              finished. A badge with no bar says "running, progress not
              measured yet", which is the truth. */}
          {measured && (
            <div
              data-testid="job-progress"
              className="h-1.5 w-16 shrink-0 overflow-hidden rounded-full bg-muted"
            >
              <div
                className="h-full rounded-full bg-primary transition-all"
                style={{
                  width: `${String(Math.min(Math.max(job.progress_percent, 0), 100))}%`,
                }}
              />
            </div>
          )}
        </div>
      );
    },
  },
  {
    key: "result",
    label: "Last Result",
    width: 170,
    sortValue: (job) =>
      job.last_result === ""
        ? null
        : (JOB_RESULT_RANK[resultLabel(job.last_result)] ?? 99),
    cell: (job) =>
      job.last_result === "" ? (
        <span className="text-muted-foreground">-</span>
      ) : (
        <Badge variant={resultVariant(job.last_result)}>
          {resultLabel(job.last_result)}
        </Badge>
      ),
  },
  {
    key: "lastRun",
    label: "Last Run",
    width: 180,
    sortValue: (job) => toEpoch(job.last_run),
    cell: (job) => (
      <span className="text-sm">{formatDateTime(job.last_run, "Never")}</span>
    ),
  },
  {
    key: "nextRun",
    label: "Next Run",
    width: 180,
    sortValue: (job) => toEpoch(job.next_run),
    cell: (job) => (
      <span className="text-sm">{formatDateTime(job.next_run, "Never")}</span>
    ),
  },
  {
    key: "repository",
    label: "Repository",
    width: 180,
    sortValue: (job) => job.repository_name || null,
    cell: (job) => (
      <span className="text-sm">{job.repository_name || "-"}</span>
    ),
  },
  {
    key: "guests",
    label: "Guests",
    width: 90,
    align: "right",
    sortValue: (job) => job.objects_count,
    cell: (job) => job.objects_count,
  },
  {
    key: "actions",
    label: "Actions",
    width: 230,
    align: "right",
    fixed: true,
    // The action components render their mutation errors and notices inline
    // beside the buttons; truncating this cell would clip the only feedback a
    // failed start/stop ever gives.
    wrap: true,
    cell: (job, ctx) => <VeeamJobActions serverId={ctx.serverId} job={job} />,
  },
];

export function VeeamJobTable({ jobs, serverId }: VeeamJobTableProps) {
  const {
    layout,
    rows: sortedJobs,
    toggle: toggleSort,
    directionFor,
  } = useDataTable("veeam-jobs", COLUMNS, jobs, byId);

  const { expanded, toggle: toggleExpand } = useExpandedRows(serverId);

  if (jobs.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No Proxmox backup jobs found on this server.
      </p>
    );
  }

  const cellCtx: JobCtx = { serverId, expanded };

  return (
    <div className="rounded-md border">
      <div className="flex justify-end px-2 pt-2">
        <ResetColumnsButton layout={layout} />
      </div>
      <DataTableFrame layout={layout}>
        <TableHeader>
          <DataTableHeadRow
            layout={layout}
            directionFor={directionFor}
            onSort={toggleSort}
          />
        </TableHeader>
        <TableBody>
          {sortedJobs.map((job) => {
            const isExpanded = expanded.has(job.id);

            return (
              <Fragment key={job.id}>
                <TableRow
                  className="cursor-pointer"
                  onClick={() => {
                    toggleExpand(job.id);
                  }}
                >
                  <DataTableCells row={job} layout={layout} ctx={cellCtx} />
                </TableRow>

                {isExpanded && (
                  <ExpandedDetailRow colSpan={layout.columns.length}>
                    {job.description !== "" && (
                      <p className="text-sm text-muted-foreground">
                        {job.description}
                      </p>
                    )}
                    <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-4">
                      <DetailField label="Bottleneck">
                        {bottleneckLabel(job.bottleneck)}
                      </DetailField>
                      <DetailField label="Duration" variant="mono">
                        {job.duration || "—"}
                      </DetailField>
                      <DetailField label="Processing rate" variant="mono">
                        {processingRateLabel(job.processing_rate)}
                      </DetailField>
                      <DetailField label="Schedule">
                        {job.next_run_policy || "—"}
                      </DetailField>
                      <DetailField label="Processed" variant="mono">
                        {formatBytes(job.processed_size)}
                      </DetailField>
                      <DetailField label="Read" variant="mono">
                        {formatBytes(job.read_size)}
                      </DetailField>
                      <DetailField label="Transferred" variant="mono">
                        {formatBytes(job.transferred_size)}
                      </DetailField>
                      <DetailField label="Job type">{job.job_type}</DetailField>
                    </dl>
                    {/* Which guests the job's latest run processed, and
                        which failed — the question a job-level "Failed" raises
                        and cannot answer.

                        The live run when there is one, else the last one Veeam
                        reported. running_session_id is preferred because
                        last_session_id is refreshed by the inventory pass and
                        is stale for a job started since it. */}
                    <VeeamTaskTable
                      serverId={serverId}
                      sessionVeeamId={
                        job.running_session_id || job.last_session_id
                      }
                      enabled={isExpanded}
                      running={job.running_session_id !== ""}
                    />

                    {job.last_result === "Failed" && (
                      <p className="text-xs text-muted-foreground">
                        Veeam records a job stopped through its API the same way
                        it records a genuine failure — same result, no
                        cancellation flag, empty log — so Nexara cannot tell the
                        two apart. Stops made from Nexara are the exception:
                        those are recorded, and the failed-job alert skips them.
                      </p>
                    )}
                  </ExpandedDetailRow>
                )}
              </Fragment>
            );
          })}
        </TableBody>
      </DataTableFrame>
    </div>
  );
}
