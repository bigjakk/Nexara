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
import { Play } from "lucide-react";
import { useRunSyncJob } from "../api/backup-queries";
import type { PBSSyncJob } from "../types/backup";

function formatUnixTime(ts: number): string {
  if (ts === 0) return "-";
  return new Date(ts * 1000).toLocaleString();
}

function stateVariant(state: string): "default" | "secondary" | "destructive" {
  if (state === "ok") return "default";
  if (state === "error") return "destructive";
  return "secondary";
}

interface SyncJobTableProps {
  jobs: PBSSyncJob[];
  pbsId: string;
}

export function SyncJobTable({ jobs, pbsId }: SyncJobTableProps) {
  const runMutation = useRunSyncJob();

  if (jobs.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No sync jobs configured.
      </p>
    );
  }

  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Job ID</TableHead>
            <TableHead>Store</TableHead>
            <TableHead>Remote</TableHead>
            <TableHead>Remote Store</TableHead>
            <TableHead>Schedule</TableHead>
            <TableHead>Last Run</TableHead>
            <TableHead>Next Run</TableHead>
            <TableHead className="w-20">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {jobs.map((job) => (
            <TableRow key={job.job_id}>
              <TableCell className="font-mono text-sm">{job.job_id}</TableCell>
              <TableCell>{job.store}</TableCell>
              <TableCell>{job.remote || "-"}</TableCell>
              <TableCell>{job.remote_store || "-"}</TableCell>
              <TableCell className="text-sm">{job.schedule || "-"}</TableCell>
              <TableCell>
                {job.last_run_state ? (
                  <Badge variant={stateVariant(job.last_run_state)}>
                    {job.last_run_state}
                  </Badge>
                ) : (
                  <span className="text-muted-foreground">-</span>
                )}
              </TableCell>
              <TableCell className="text-sm">
                {formatUnixTime(job.next_run)}
              </TableCell>
              <TableCell>
                <Button
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
    </div>
  );
}
