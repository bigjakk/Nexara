import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { Play } from "lucide-react";
import { useRunSyncJob } from "../api/backup-queries";
import type { PBSSyncJob } from "../types/backup";
import { RunState } from "./StateText";

function formatUnixTime(ts: number): string {
  if (ts === 0) return "-";
  return new Date(ts * 1000).toLocaleString();
}

interface SyncJobTableProps {
  jobs: PBSSyncJob[];
  pbsId: string;
}

export function SyncJobTable({ jobs, pbsId }: SyncJobTableProps) {
  const runMutation = useRunSyncJob();

  if (jobs.length === 0) {
    return (
      <Panel>
        <p className="py-6 text-center text-sm text-muted-foreground">
          No sync jobs configured.
        </p>
      </Panel>
    );
  }

  return (
    <Panel flush>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Job</TableHead>
            <TableHead>Datastore</TableHead>
            <TableHead>Remote</TableHead>
            <TableHead>Remote datastore</TableHead>
            <TableHead>Schedule</TableHead>
            <TableHead>Last run</TableHead>
            <TableHead>Next run</TableHead>
            <TableHead className="w-12">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {jobs.map((job) => (
            <TableRow key={job.job_id}>
              <TableCell className="font-mono font-medium">
                {job.job_id}
              </TableCell>
              <TableCell>{job.store}</TableCell>
              <TableCell>{job.remote || "-"}</TableCell>
              <TableCell>{job.remote_store || "-"}</TableCell>
              <TableCell>{job.schedule || "-"}</TableCell>
              <TableCell>
                <RunState state={job.last_run_state} />
              </TableCell>
              <TableCell className="whitespace-nowrap">
                {formatUnixTime(job.next_run)}
              </TableCell>
              <TableCell>
                <Button
                  aria-label={`Run sync job ${job.job_id}`}
                  title="Run now"
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    runMutation.mutate({ pbsId, jobId: job.job_id });
                  }}
                  disabled={runMutation.isPending}
                >
                  <Play className="h-4 w-4" />
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  );
}
