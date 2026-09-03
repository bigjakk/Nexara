import { Fragment, useState } from "react";
import {
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { ChevronDown, ChevronRight } from "lucide-react";
import { formatBytes } from "@/lib/format";
import { byId } from "@/hooks/useTableSort";
import type { ColumnDef } from "@/hooks/useColumnLayout";
import { useDataTable } from "@/hooks/useDataTable";
import { DataTableFrame } from "@/components/DataTableFrame";
import { DataTableHeadRow } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
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

function resultVariant(
  result: string,
): "default" | "secondary" | "destructive" | "outline" {
  switch (result) {
    case "Success":
      return "default";
    case "Warning":
      return "outline";
    case "Failed":
      return "destructive";
    default:
      return "secondary";
  }
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

function formatTime(value: string | null): string {
  if (value == null || value === "") return "Never";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
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
  {
    // The expand affordance. Unlabelled and unsortable, and pinned so it
    // cannot be dragged into the middle of the data.
    key: "expand",
    label: "",
    width: 40,
    fixed: true,
    cell: (job, ctx) =>
      ctx.expanded.has(job.id) ? (
        <ChevronDown className="h-4 w-4" />
      ) : (
        <ChevronRight className="h-4 w-4" />
      ),
  },
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
    cell: (job) => <span className="text-sm">{formatTime(job.last_run)}</span>,
  },
  {
    key: "nextRun",
    label: "Next Run",
    width: 180,
    sortValue: (job) => toEpoch(job.next_run),
    cell: (job) => <span className="text-sm">{formatTime(job.next_run)}</span>,
  },
  {
    key: "repository",
    label: "Repository",
    width: 180,
    sortValue: (job) => job.repository_name || null,
    cell: (job) => <span className="text-sm">{job.repository_name || "-"}</span>,
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
    cell: (job, ctx) => (
      <VeeamJobActions serverId={ctx.serverId} job={job} />
    ),
  },
];

export function VeeamJobTable({ jobs, serverId }: VeeamJobTableProps) {
  const {
    layout,
    rows: sortedJobs,
    toggle: toggleSort,
    directionFor,
  } = useDataTable("veeam-jobs", COLUMNS, jobs, byId);

  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  // Row ids are server-scoped, so switching servers must not carry a stale
  // expansion set forward — it only grows, and rows silently re-expand on
  // return.
  //
  // This has to stay ABOVE the empty-state return below. A server with no rows
  // still renders this component, and if it returned before updating
  // expandedFor, switching A → (empty) B → A would compare A against A, skip
  // the reset, and re-expand A's old rows.
  const [expandedFor, setExpandedFor] = useState(serverId);
  if (expandedFor !== serverId) {
    setExpandedFor(serverId);
    setExpanded(new Set());
  }

  if (jobs.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No Proxmox backup jobs found on this server.
      </p>
    );
  }

  function toggle(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
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
                    toggle(job.id);
                  }}
                >
                  <DataTableCells row={job} layout={layout} ctx={cellCtx} />
                </TableRow>

                {isExpanded && (
                  <TableRow>
                    <TableCell
                      colSpan={layout.columns.length}
                      className="bg-muted/30"
                    >
                      <div className="space-y-3 px-2 py-3">
                        {job.description !== "" && (
                          <p className="text-sm text-muted-foreground">
                            {job.description}
                          </p>
                        )}
                        <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-4">
                          <div>
                            <dt className="text-xs text-muted-foreground">
                              Bottleneck
                            </dt>
                            <dd>
                              {job.bottleneck === "" ||
                              job.bottleneck === "NotDefined"
                                ? "—"
                                : job.bottleneck}
                            </dd>
                          </div>
                          <div>
                            <dt className="text-xs text-muted-foreground">
                              Duration
                            </dt>
                            <dd className="font-mono">{job.duration || "—"}</dd>
                          </div>
                          <div>
                            <dt className="text-xs text-muted-foreground">
                              Processing rate
                            </dt>
                            <dd className="font-mono">
                              {job.processing_rate === "" ||
                              job.processing_rate === "N/A"
                                ? "—"
                                : job.processing_rate}
                            </dd>
                          </div>
                          <div>
                            <dt className="text-xs text-muted-foreground">
                              Schedule
                            </dt>
                            <dd>{job.next_run_policy || "—"}</dd>
                          </div>
                          <div>
                            <dt className="text-xs text-muted-foreground">
                              Processed
                            </dt>
                            <dd className="font-mono">
                              {formatBytes(job.processed_size)}
                            </dd>
                          </div>
                          <div>
                            <dt className="text-xs text-muted-foreground">
                              Read
                            </dt>
                            <dd className="font-mono">
                              {formatBytes(job.read_size)}
                            </dd>
                          </div>
                          <div>
                            <dt className="text-xs text-muted-foreground">
                              Transferred
                            </dt>
                            <dd className="font-mono">
                              {formatBytes(job.transferred_size)}
                            </dd>
                          </div>
                          <div>
                            <dt className="text-xs text-muted-foreground">
                              Job type
                            </dt>
                            <dd>{job.job_type}</dd>
                          </div>
                        </dl>
                        {/* Which guests the job's latest run processed, and
                              which failed — the question a job-level "Failed"
                              raises and cannot answer.

                              The live run when there is one, else the last one
                              Veeam reported. running_session_id is preferred
                              because last_session_id is refreshed by the
                              inventory pass and is stale for a job started
                              since it. */}
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
                            Veeam records a job stopped through its API the same
                            way it records a genuine failure — same result, no
                            cancellation flag, empty log — so Nexara cannot tell
                            the two apart. Stops made from Nexara are the
                            exception: those are recorded, and the failed-job
                            alert skips them.
                          </p>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                )}
              </Fragment>
            );
          })}
        </TableBody>
      </DataTableFrame>
    </div>
  );
}
