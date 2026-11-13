import { Skeleton } from "@/components/ui/skeleton";
import { Panel } from "@/components/page/Panel";
import { useBackupJobs } from "../api/backup-queries";
import { BackupJobDialog } from "./BackupJobDialog";
import { BackupJobTable } from "./BackupJobTable";

interface ClusterBackupTabProps {
  clusterId: string;
  /** The caller may add schedules (manage:backup). */
  canManage: boolean;
}

/**
 * A cluster's backup schedules, where Proxmox has Datacenter > Backup. They are
 * Proxmox's own vzdump jobs and back up to any store that holds backups, so
 * they need no registered PBS server, whose page lists them too.
 */
export function ClusterBackupTab({
  clusterId,
  canManage,
}: ClusterBackupTabProps) {
  const jobsQuery = useBackupJobs(clusterId);
  const jobs = jobsQuery.data ?? [];

  return (
    <Panel
      flush
      title="Backup schedules"
      aside={
        jobsQuery.isLoading
          ? "Loading..."
          : jobs.length > 0
            ? `${String(jobs.length)} ${jobs.length === 1 ? "job" : "jobs"}`
            : undefined
      }
      actions={
        canManage ? <BackupJobDialog clusterId={clusterId} /> : undefined
      }
    >
      {jobsQuery.isLoading ? (
        <div className="space-y-2 px-[18px] pb-1">
          {Array.from({ length: 2 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : jobsQuery.isError && jobsQuery.data === undefined ? (
        <p className="px-[18px] pb-1 text-sm text-destructive">
          Failed to load this cluster&apos;s backup schedules.
        </p>
      ) : (
        <BackupJobTable jobs={jobs} clusterId={clusterId} />
      )}
    </Panel>
  );
}
