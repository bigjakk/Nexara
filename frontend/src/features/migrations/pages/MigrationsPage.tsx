import { ArrowLeftRight } from "lucide-react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/EmptyState";
import { UsageBar } from "@/components/UsageBar";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import {
  useMigrationGuestNames,
  useMigrationJobs,
} from "../api/migration-queries";
import { MigrateWizard } from "../components/MigrateWizard";
import { MigrationStatusText } from "../components/MigrationStatusText";
import { ACTIVE_MIGRATION_STATUSES, jobSummary } from "../lib/job-summary";
import type { MigrationJob } from "../types/migration";
import { DateTime } from "@/components/DateTime";

const TYPE_LABEL: Record<MigrationJob["migration_type"], string> = {
  "intra-cluster": "Within the cluster",
  "cross-cluster": "Between clusters",
};

/** Only a job that is moving has a bar; a finished one needs no picture. */
function MigrationProgress({ job }: { job: MigrationJob }) {
  if (job.status !== "migrating") {
    return <span className="text-muted-foreground/60">—</span>;
  }
  const percent = Math.round(job.progress * 100);
  return (
    <div className="flex items-center gap-2">
      {/* A sliver even at 0%, so a job that just started is not an empty track. */}
      <UsageBar
        percent={Math.max(percent, 5)}
        progress
        className="w-20 shrink-0"
      />
      <span className="text-[12.5px] font-medium tabular-nums text-info">
        {String(percent)}%
      </span>
    </div>
  );
}

export function MigrationsPage() {
  const { data: jobs, isLoading } = useMigrationJobs();
  const guestNames = useMigrationGuestNames(jobs);
  const summary =
    jobSummary(
      "migration",
      (jobs ?? []).map((j) => j.status),
      ACTIVE_MIGRATION_STATUSES,
    ) ??
    "Move a guest to another node or another cluster, after a pre-flight check.";

  return (
    <div className="space-y-5">
      <PageHeader
        title="Migrations"
        summary={summary}
        actions={<MigrateWizard />}
      />

      {isLoading ? (
        <Panel title="Jobs" aside="Loading...">
          <div className="space-y-2">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-8 w-full" />
            ))}
          </div>
        </Panel>
      ) : !jobs || jobs.length === 0 ? (
        <EmptyState
          icon={ArrowLeftRight}
          title="No migration jobs yet"
          description="A job moves one guest and records its pre-flight checks, its progress and its outcome. New migration starts one."
        />
      ) : (
        <Panel flush title="Jobs" aside={String(jobs.length)}>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Guest</TableHead>
                <TableHead>From</TableHead>
                <TableHead>To</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Progress</TableHead>
                <TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobs.map((job) => (
                <TableRow key={job.id}>
                  <TableCell className="whitespace-nowrap py-2">
                    {guestNames.get(
                      `${job.source_cluster_id}:${String(job.vmid)}`,
                    ) !== undefined && (
                      <span className="mr-1.5 font-medium">
                        {guestNames.get(
                          `${job.source_cluster_id}:${String(job.vmid)}`,
                        )}
                      </span>
                    )}
                    <span className="tabular-nums">{job.vmid}</span>{" "}
                    <span className="text-[12.5px] text-muted-foreground">
                      {job.vm_type === "lxc" ? "CT" : "VM"}
                    </span>
                  </TableCell>
                  <TableCell className="py-2">{job.source_node}</TableCell>
                  <TableCell className="py-2">
                    {job.target_node || (
                      <span className="text-muted-foreground/60">—</span>
                    )}
                  </TableCell>
                  <TableCell className="py-2 text-muted-foreground">
                    {TYPE_LABEL[job.migration_type]}
                  </TableCell>
                  <TableCell className="py-2">
                    <MigrationStatusText status={job.status} />
                  </TableCell>
                  <TableCell className="py-2">
                    <MigrationProgress job={job} />
                  </TableCell>
                  <TableCell className="whitespace-nowrap py-2 text-[12.5px] text-muted-foreground">
                    <DateTime value={job.created_at} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      )}
    </div>
  );
}
