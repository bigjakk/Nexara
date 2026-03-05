import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import type { PBSTask } from "../types/backup";

function formatTime(unix: number): string {
  if (!unix) return "-";
  return new Date(unix * 1000).toLocaleString();
}

function formatDuration(start: number, end?: number): string {
  if (!end) return "running";
  const secs = end - start;
  if (secs < 60) return `${secs}s`;
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return `${m}m ${s}s`;
}

interface PBSTaskTableProps {
  tasks: PBSTask[];
}

export function PBSTaskTable({ tasks }: PBSTaskTableProps) {
  if (tasks.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No recent tasks.
      </p>
    );
  }

  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Type</TableHead>
            <TableHead>User</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Started</TableHead>
            <TableHead>Duration</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {tasks.map((task) => {
            const isRunning = !task.endtime;
            const isOk = task.status === "OK" || task.status === "";
            const isFailed = !isRunning && !isOk;

            return (
              <TableRow key={task.upid}>
                <TableCell className="font-mono text-xs">
                  {task.worker_type}
                </TableCell>
                <TableCell className="text-sm">
                  {task.user}
                </TableCell>
                <TableCell>
                  {isRunning && (
                    <Badge variant="default" className="bg-blue-600">Running</Badge>
                  )}
                  {!isRunning && isOk && (
                    <Badge variant="default" className="bg-green-600">OK</Badge>
                  )}
                  {isFailed && (
                    <Badge variant="destructive">{task.status ?? "Error"}</Badge>
                  )}
                </TableCell>
                <TableCell className="text-xs">
                  {formatTime(task.starttime)}
                </TableCell>
                <TableCell className="text-xs">
                  {formatDuration(task.starttime, task.endtime)}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
