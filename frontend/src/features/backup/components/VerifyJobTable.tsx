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
import { useRunVerifyJob } from "../api/backup-queries";
import type { PBSVerifyJob } from "../types/backup";
import { RunState } from "./StateText";

interface VerifyJobTableProps {
  jobs: PBSVerifyJob[];
  pbsId: string;
}

export function VerifyJobTable({ jobs, pbsId }: VerifyJobTableProps) {
  const runMutation = useRunVerifyJob();

  if (jobs.length === 0) {
    return (
      <Panel>
        <p className="py-6 text-center text-sm text-muted-foreground">
          No verify jobs configured.
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
            <TableHead>Schedule</TableHead>
            <TableHead>Last run</TableHead>
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
              <TableCell>{job.schedule || "-"}</TableCell>
              <TableCell>
                <RunState state={job.last_run_state} />
              </TableCell>
              <TableCell>
                <Button
                  aria-label={`Run verify job ${job.job_id}`}
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
