import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { RollingUpdateJob } from "@/types/api";
import { Loader2 } from "lucide-react";

const STATUS_LABEL: Record<RollingUpdateJob["status"], string> = {
  pending: "Pending",
  running: "Running",
  paused: "Paused",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};

/** A job's state: a dot and a word. Finished is plain, and only trouble is coloured. */
function JobStatus({ status }: { status: RollingUpdateJob["status"] }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-[7px] whitespace-nowrap",
        (status === "pending" || status === "cancelled") &&
          "text-muted-foreground",
        status === "paused" && "text-amber-700 dark:text-amber-400",
        status === "failed" && "text-red-700 dark:text-red-400",
      )}
    >
      {status === "running" ? (
        <Loader2 className="h-3.5 w-3.5 animate-spin text-data" />
      ) : (
        <span
          aria-hidden="true"
          className={cn(
            "h-2 w-2 shrink-0 rounded-full",
            status === "paused" && "bg-amber-500",
            status === "failed" && "bg-red-500",
            status === "completed" && "bg-muted-foreground/45",
            (status === "pending" || status === "cancelled") &&
              "ring-[1.5px] ring-inset ring-muted-foreground/60",
          )}
        />
      )}
      {STATUS_LABEL[status]}
    </span>
  );
}

interface RollingUpdateJobsTableProps {
  jobs: RollingUpdateJob[];
  canManage: boolean;
  onSelect: (job: RollingUpdateJob) => void;
  onStart: (jobId: string) => void;
  onCancel: (jobId: string) => void;
}

export function RollingUpdateJobsTable({
  jobs,
  canManage,
  onSelect,
  onStart,
  onCancel,
}: RollingUpdateJobsTableProps) {
  if (jobs.length === 0) {
    return (
      <p className="px-[18px] pb-1 text-sm text-muted-foreground">
        No rolling update jobs yet
      </p>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead>Status</TableHead>
          <TableHead className="whitespace-nowrap">Nodes at a time</TableHead>
          <TableHead>Reboot</TableHead>
          <TableHead>Created</TableHead>
          <TableHead>Completed</TableHead>
          <TableHead className="text-right">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {jobs.map((job) => (
          <TableRow key={job.id}>
            {/* A floor on the width: on a phone the table scrolls sideways
                rather than squeeze a failure's reason into a ribbon. */}
            <TableCell
              className={job.failure_reason ? "min-w-[16rem] py-2" : "py-2"}
            >
              <JobStatus status={job.status} />
              {job.failure_reason && (
                <div className="mt-0.5 max-w-[30rem] text-[12.5px] text-red-700 [overflow-wrap:anywhere] dark:text-red-400">
                  {job.failure_reason}
                </div>
              )}
            </TableCell>
            <TableCell className="py-2 tabular-nums">
              {job.parallelism}
            </TableCell>
            <TableCell className="py-2">
              {job.reboot_after_update ? "Yes" : "No"}
            </TableCell>
            <TableCell className="whitespace-nowrap py-2 text-muted-foreground">
              {new Date(job.created_at).toLocaleString()}
            </TableCell>
            <TableCell className="whitespace-nowrap py-2 text-muted-foreground">
              {job.completed_at
                ? new Date(job.completed_at).toLocaleString()
                : "—"}
            </TableCell>
            <TableCell className="py-1.5 text-right">
              <div className="flex items-center justify-end gap-1">
                {/* View first: Start begins a rolling update without asking,
                    so it does not sit where a habitual click lands. */}
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-primary hover:text-primary"
                  onClick={() => {
                    onSelect(job);
                  }}
                >
                  View
                </Button>
                {canManage && job.status === "pending" && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      onStart(job.id);
                    }}
                  >
                    Start
                  </Button>
                )}
                {canManage &&
                  (job.status === "pending" ||
                    job.status === "running" ||
                    job.status === "paused") && (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-muted-foreground"
                      onClick={() => {
                        onCancel(job.id);
                      }}
                    >
                      Cancel
                    </Button>
                  )}
              </div>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
