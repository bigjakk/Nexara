import { useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Pencil,
  Trash2,
  Play,
  Loader2,
  ChevronRight,
  ChevronDown,
} from "lucide-react";
import type { BackupJob } from "../types/backup";
import { useDeleteBackupJob, useRunBackupJob } from "../api/backup-queries";
import { describeSchedule } from "../lib/schedule";
import { BackupJobDialog } from "./BackupJobDialog";

function formatNextRun(ts?: number): string {
  if (!ts) return "-";
  return new Date(ts * 1000).toLocaleString();
}

/** Mirrors the dialog's guest-selection modes, in the same precedence. */
function formatGuests(job: BackupJob): string {
  if (job.exclude) return `All except ${job.exclude}`;
  if (job.all) return "All";
  if (job.pool) return `Pool: ${job.pool}`;
  if (job.vmid) return job.vmid;
  return "All";
}

interface BackupJobTableProps {
  jobs: BackupJob[];
  clusterId: string;
}

export function BackupJobTable({ jobs, clusterId }: BackupJobTableProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [editJob, setEditJob] = useState<BackupJob | null>(null);
  const [runningJobId, setRunningJobId] = useState<string | null>(null);
  const deleteMutation = useDeleteBackupJob();
  const runMutation = useRunBackupJob();

  if (jobs.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No backup job schedules found.
      </p>
    );
  }

  const toggleExpand = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  return (
    <>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>ID</TableHead>
              <TableHead>Schedule</TableHead>
              <TableHead>Storage</TableHead>
              <TableHead>Mode</TableHead>
              <TableHead>Enabled</TableHead>
              <TableHead>Next Run</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobs.map((job) => {
              const isExpanded = expanded.has(job.id);
              const isEnabled = job.enabled !== 0;
              const scheduleText = describeSchedule(job.schedule);

              return (
                <>
                  <TableRow
                    key={job.id}
                    className="cursor-pointer"
                    onClick={() => {
                      toggleExpand(job.id);
                    }}
                  >
                    <TableCell className="px-2">
                      {isExpanded ? (
                        <ChevronDown className="h-4 w-4" />
                      ) : (
                        <ChevronRight className="h-4 w-4" />
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-sm">
                      {job.id}
                    </TableCell>
                    <TableCell className="text-sm">
                      {job.schedule ? (
                        <>
                          <div>{scheduleText ?? job.schedule}</div>
                          {scheduleText && (
                            <div className="font-mono text-xs text-muted-foreground">
                              {job.schedule}
                            </div>
                          )}
                        </>
                      ) : (
                        "-"
                      )}
                    </TableCell>
                    <TableCell className="text-sm">
                      {job.storage ?? "-"}
                    </TableCell>
                    <TableCell className="text-sm">{job.mode ?? "-"}</TableCell>
                    <TableCell>
                      {isEnabled ? (
                        <Badge variant="default" className="bg-emerald-600">
                          Yes
                        </Badge>
                      ) : (
                        <Badge variant="secondary">No</Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-xs">
                      {formatNextRun(job["next-run"])}
                    </TableCell>
                    <TableCell>
                      <div
                        className="flex items-center gap-1"
                        onClick={(e) => {
                          e.stopPropagation();
                        }}
                      >
                        <Button
                          variant="ghost"
                          size="sm"
                          title="Run Now"
                          onClick={() => {
                            setRunningJobId(job.id);
                            runMutation.mutate(
                              { clusterId, jobId: job.id },
                              {
                                onSettled: () => {
                                  setRunningJobId(null);
                                },
                              },
                            );
                          }}
                          disabled={runningJobId === job.id}
                        >
                          {runningJobId === job.id ? (
                            <Loader2 className="h-4 w-4 animate-spin" />
                          ) : (
                            <Play className="h-4 w-4" />
                          )}
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          title="Edit"
                          onClick={() => {
                            setEditJob(job);
                          }}
                        >
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          title="Delete"
                          onClick={() => {
                            deleteMutation.mutate({
                              clusterId,
                              jobId: job.id,
                            });
                          }}
                          disabled={deleteMutation.isPending}
                        >
                          <Trash2 className="h-4 w-4 text-destructive" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                  {isExpanded && (
                    <TableRow key={`${job.id}-detail`}>
                      <TableCell colSpan={8} className="bg-muted/30 px-8 py-4">
                        <div className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm md:grid-cols-4">
                          <div>
                            <span className="text-muted-foreground">Type:</span>{" "}
                            {job.type}
                          </div>
                          <div>
                            <span className="text-muted-foreground">Node:</span>{" "}
                            {job.node ?? "All"}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              Guests:
                            </span>{" "}
                            {formatGuests(job)}
                          </div>
                          <div>
                            <span className="text-muted-foreground">
                              Compress:
                            </span>{" "}
                            {job.compress ?? "-"}
                          </div>
                          <div>
                            <span className="text-muted-foreground">Mail:</span>{" "}
                            {job.mailto ?? "-"}
                          </div>
                          <div className="col-span-2">
                            <span className="text-muted-foreground">
                              Comment:
                            </span>{" "}
                            {job.comment ?? "-"}
                          </div>
                        </div>
                      </TableCell>
                    </TableRow>
                  )}
                </>
              );
            })}
          </TableBody>
        </Table>
      </div>
      {editJob && (
        <BackupJobDialog
          clusterId={clusterId}
          job={editJob}
          open={!!editJob}
          onOpenChange={(open) => {
            if (!open) setEditJob(null);
          }}
        />
      )}
    </>
  );
}
