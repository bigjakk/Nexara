import { Fragment, useState } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { DefGrid } from "@/components/page/Panel";
import { StateText } from "./StateText";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Pencil,
  Trash2,
  Play,
  Loader2,
  ChevronRight,
  ChevronDown,
} from "lucide-react";
import type { BackupJob } from "../types/backup";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { usePermissions } from "@/hooks/usePermissions";
import { useDeleteBackupJob, useRunBackupJob } from "../api/backup-queries";
import { describeBackupJobRunConfirm } from "../lib/backup-job-run";
import { describeSchedule } from "../lib/schedule";
import { BackupJobDialog } from "./BackupJobDialog";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { backupHistories } from "@/features/history/lib/backup-histories";
import { DateTime } from "@/components/DateTime";

function formatNextRun(ts?: number) {
  return <DateTime value={ts === undefined ? null : ts * 1000} fallback="-" />;
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

/**
 * A cluster's backup job schedules, as rows for the panel that holds them:
 * the panel's title row carries the cluster choice and the Add button.
 */
export function BackupJobTable({ jobs, clusterId }: BackupJobTableProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [editJob, setEditJob] = useState<BackupJob | null>(null);
  const [runningJobId, setRunningJobId] = useState<string | null>(null);
  // Run now confirms first, as the Proxmox GUI does: a run may prune older
  // backups as a scheduled one does, and a stop-mode job shuts guests down.
  const [confirmRun, setConfirmRun] = useState<BackupJob | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<BackupJob | null>(null);
  const deleteMutation = useDeleteBackupJob();
  const runMutation = useRunBackupJob();
  // Each action shows only to a caller its route admits: run and edit take
  // manage:backup, delete takes delete:backup (registry_backup.go).
  const { hasPermission } = usePermissions();
  const canManage = hasPermission("manage", "backup");
  const canDelete = hasPermission("delete", "backup");

  const runJob = (job: BackupJob) => {
    setConfirmRun(null);
    setRunningJobId(job.id);
    runMutation.mutate(
      { clusterId, jobId: job.id },
      {
        onSettled: () => {
          setRunningJobId(null);
        },
      },
    );
  };

  if (jobs.length === 0) {
    return (
      <p className="px-[18px] py-6 text-center text-sm text-muted-foreground">
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
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-8" />
            <TableHead>Job</TableHead>
            <TableHead>Schedule</TableHead>
            <TableHead>Storage</TableHead>
            <TableHead>Mode</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Next run</TableHead>
            <TableHead className="w-32">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {jobs.map((job) => {
            const isExpanded = expanded.has(job.id);
            const isEnabled = job.enabled !== 0;
            const scheduleText = describeSchedule(job.schedule);

            return (
              <Fragment key={job.id}>
                <TableRow
                  className="cursor-pointer"
                  onClick={() => {
                    toggleExpand(job.id);
                  }}
                >
                  <TableCell className="pr-0 text-muted-foreground">
                    {isExpanded ? (
                      <ChevronDown className="h-4 w-4" />
                    ) : (
                      <ChevronRight className="h-4 w-4" />
                    )}
                  </TableCell>
                  <TableCell className="font-mono font-medium">
                    {job.id}
                  </TableCell>
                  <TableCell>
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
                  <TableCell>{job.storage ?? "-"}</TableCell>
                  <TableCell>{job.mode ?? "-"}</TableCell>
                  <TableCell>
                    {isEnabled ? (
                      <StateText tone="ok">Enabled</StateText>
                    ) : (
                      <StateText tone="idle">Disabled</StateText>
                    )}
                  </TableCell>
                  <TableCell className="whitespace-nowrap">
                    {/* Proxmox reports a next-run for a disabled job too,
                        which does not run. */}
                    {isEnabled ? formatNextRun(job["next-run"]) : "-"}
                  </TableCell>
                  <TableCell>
                    <div
                      className="flex items-center justify-end gap-0.5"
                      onClick={(e) => {
                        e.stopPropagation();
                      }}
                    >
                      <ClusterHistoryButton
                        history={backupHistories.backupJob(clusterId, job.id)}
                        title={job.id}
                      />
                      {canManage && (
                        <>
                          <Button
                            variant="ghost"
                            size="sm"
                            title="Run Now"
                            onClick={() => {
                              setConfirmRun(job);
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
                        </>
                      )}
                      {canDelete && (
                        <Button
                          variant="ghost"
                          size="sm"
                          title="Delete"
                          onClick={() => {
                            setConfirmDelete(job);
                          }}
                          disabled={deleteMutation.isPending}
                        >
                          <Trash2 className="h-4 w-4 text-destructive" />
                        </Button>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
                {isExpanded && (
                  <ExpandedDetailRow colSpan={8}>
                    <DefGrid>
                      <DetailField label="Type">{job.type}</DetailField>
                      <DetailField label="Node">
                        {job.node ?? "All"}
                      </DetailField>
                      <DetailField label="Guests">
                        {formatGuests(job)}
                      </DetailField>
                      <DetailField label="Compression">
                        {job.compress ?? "-"}
                      </DetailField>
                      <DetailField label="Mail to">
                        {job.mailto ?? "-"}
                      </DetailField>
                      <DetailField label="Comment">
                        {job.comment ?? "-"}
                      </DetailField>
                    </DefGrid>
                  </ExpandedDetailRow>
                )}
              </Fragment>
            );
          })}
        </TableBody>
      </Table>
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
      <AlertDialog
        open={confirmRun !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmRun(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Run backup job {confirmRun?.id} now?
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirmRun && describeBackupJobRunConfirm(confirmRun)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (confirmRun) runJob(confirmRun);
              }}
            >
              Run now
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {/*
        pve-manager PVE/API2/Backup.pm "delete_job" removes the job from
        vzdump.cron or jobs.cfg and, for the latter, unlinks its schedule
        state (PVE/Jobs.pm remove_job). It touches no backup archive and
        signals no running vzdump.
      */}
      <ConfirmDeleteDialog
        target={confirmDelete}
        onClose={() => {
          setConfirmDelete(null);
        }}
        onConfirm={(job) => {
          deleteMutation.mutate({ clusterId, jobId: job.id });
        }}
        title={(job) => `Delete backup job ${job.id}?`}
        description={(job) =>
          `Proxmox deletes the job as soon as you confirm, and it no longer runs on its schedule. The backups it already made stay on ${job.storage ?? "their storage"}, and a backup it is running now is not stopped. To undo, create the job again.`
        }
      />
    </>
  );
}
