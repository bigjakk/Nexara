import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { RollingUpdateJob } from "@/types/api";
import { Play, XCircle, Eye } from "lucide-react";

const statusVariant: Record<
  RollingUpdateJob["status"],
  "default" | "secondary" | "destructive" | "outline"
> = {
  pending: "outline",
  running: "default",
  paused: "secondary",
  completed: "outline",
  failed: "destructive",
  cancelled: "outline",
};

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
      <p className="py-8 text-center text-sm text-muted-foreground">
        No rolling update jobs yet
      </p>
    );
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Status</TableHead>
          <TableHead>Parallelism</TableHead>
          <TableHead>Reboot</TableHead>
          <TableHead>Created</TableHead>
          <TableHead>Completed</TableHead>
          <TableHead className="text-right">Actions</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {jobs.map((job) => (
          <TableRow key={job.id}>
            <TableCell>
              <Badge variant={statusVariant[job.status]}>{job.status}</Badge>
              {job.failure_reason && (
                <span className="ml-2 text-xs text-destructive">
                  {job.failure_reason}
                </span>
              )}
            </TableCell>
            <TableCell>{job.parallelism}</TableCell>
            <TableCell>{job.reboot_after_update ? "Yes" : "No"}</TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {new Date(job.created_at).toLocaleString()}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {job.completed_at
                ? new Date(job.completed_at).toLocaleString()
                : "-"}
            </TableCell>
            <TableCell className="text-right">
              <div className="flex items-center justify-end gap-1">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => { onSelect(job); }}
                >
                  <Eye className="mr-1 h-3 w-3" />
                  View
                </Button>
                {canManage && job.status === "pending" && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => { onStart(job.id); }}
                  >
                    <Play className="mr-1 h-3 w-3" />
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
                      onClick={() => { onCancel(job.id); }}
                    >
                      <XCircle className="mr-1 h-3 w-3" />
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
