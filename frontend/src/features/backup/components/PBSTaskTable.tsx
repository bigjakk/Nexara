import { useState, useMemo } from "react";
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
import { Input } from "@/components/ui/input";
import {
  ChevronRight,
  ChevronDown,
  ChevronLeft,
  Search,
} from "lucide-react";
import type { PBSTask } from "../types/backup";
import { usePBSTaskLog } from "../api/backup-queries";

const PAGE_SIZE = 25;

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

function TaskLogPanel({ pbsId, upid }: { pbsId: string; upid: string }) {
  const { data: entries, isLoading } = usePBSTaskLog(pbsId, upid);

  if (isLoading) {
    return (
      <p className="py-2 text-xs text-muted-foreground">Loading log...</p>
    );
  }

  if (!entries || entries.length === 0) {
    return <p className="py-2 text-xs text-muted-foreground">No log data.</p>;
  }

  return (
    <pre className="max-h-64 overflow-auto rounded bg-muted p-3 font-mono text-xs leading-relaxed">
      {entries.map((e) => e.t).join("\n")}
    </pre>
  );
}

interface PBSTaskTableProps {
  tasks: PBSTask[];
  pbsId: string;
}

export function PBSTaskTable({ tasks, pbsId }: PBSTaskTableProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [search, setSearch] = useState("");
  const [typeFilter, setTypeFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [page, setPage] = useState(0);

  // Extract unique task types for the filter dropdown
  const taskTypes = useMemo(() => {
    const types = new Set(tasks.map((t) => t.worker_type));
    return [...types].sort();
  }, [tasks]);

  // Filter tasks
  const filtered = useMemo(() => {
    let result = tasks;
    if (typeFilter !== "all") {
      result = result.filter((t) => t.worker_type === typeFilter);
    }
    if (statusFilter === "running") {
      result = result.filter((t) => !t.endtime);
    } else if (statusFilter === "ok") {
      result = result.filter(
        (t) => t.endtime && (t.status === "OK" || t.status === ""),
      );
    } else if (statusFilter === "error") {
      result = result.filter(
        (t) => t.endtime && t.status !== "OK" && t.status !== "",
      );
    }
    if (search) {
      const q = search.toLowerCase();
      result = result.filter(
        (t) =>
          t.worker_type.toLowerCase().includes(q) ||
          t.user.toLowerCase().includes(q) ||
          t.upid.toLowerCase().includes(q),
      );
    }
    return result;
  }, [tasks, typeFilter, statusFilter, search]);

  // Pagination
  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages - 1);
  const paged = filtered.slice(safePage * PAGE_SIZE, (safePage + 1) * PAGE_SIZE);

  // Reset page when filters change
  useMemo(() => { setPage(0); }, [typeFilter, statusFilter, search]);

  if (tasks.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No recent tasks.
      </p>
    );
  }

  const toggleExpand = (upid: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(upid)) {
        next.delete(upid);
      } else {
        next.add(upid);
      }
      return next;
    });
  };

  return (
    <div className="space-y-3">
      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="Search tasks..."
            value={search}
            onChange={(e) => { setSearch(e.target.value); }}
            className="pl-8 h-9"
          />
        </div>
        <select
          className="rounded-md border bg-background px-3 py-2 text-sm h-9"
          value={typeFilter}
          onChange={(e) => { setTypeFilter(e.target.value); }}
        >
          <option value="all">All Types</option>
          {taskTypes.map((t) => (
            <option key={t} value={t}>{t}</option>
          ))}
        </select>
        <select
          className="rounded-md border bg-background px-3 py-2 text-sm h-9"
          value={statusFilter}
          onChange={(e) => { setStatusFilter(e.target.value); }}
        >
          <option value="all">All Statuses</option>
          <option value="running">Running</option>
          <option value="ok">OK</option>
          <option value="error">Error</option>
        </select>
        <span className="text-xs text-muted-foreground">
          {filtered.length} task{filtered.length !== 1 ? "s" : ""}
        </span>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8" />
              <TableHead>Type</TableHead>
              <TableHead>User</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Started</TableHead>
              <TableHead>Duration</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {paged.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="py-8 text-center text-sm text-muted-foreground">
                  No matching tasks.
                </TableCell>
              </TableRow>
            )}
            {paged.map((task) => {
              const isRunning = !task.endtime;
              const isOk = task.status === "OK" || task.status === "";
              const isFailed = !isRunning && !isOk;
              const isExpanded = expanded.has(task.upid);

              return (
                <>
                  <TableRow
                    key={task.upid}
                    className="cursor-pointer"
                    onClick={() => {
                      toggleExpand(task.upid);
                    }}
                  >
                    <TableCell className="px-2">
                      {isExpanded ? (
                        <ChevronDown className="h-4 w-4" />
                      ) : (
                        <ChevronRight className="h-4 w-4" />
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {task.worker_type}
                    </TableCell>
                    <TableCell className="text-sm">{task.user}</TableCell>
                    <TableCell>
                      {isRunning && (
                        <Badge variant="default" className="bg-blue-600">
                          Running
                        </Badge>
                      )}
                      {!isRunning && isOk && (
                        <Badge variant="default" className="bg-green-600">
                          OK
                        </Badge>
                      )}
                      {isFailed && (
                        <Badge variant="destructive">
                          {task.status ?? "Error"}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-xs">
                      {formatTime(task.starttime)}
                    </TableCell>
                    <TableCell className="text-xs">
                      {formatDuration(task.starttime, task.endtime)}
                    </TableCell>
                  </TableRow>
                  {isExpanded && (
                    <TableRow key={`${task.upid}-log`}>
                      <TableCell colSpan={6} className="bg-muted/30 px-6 py-3">
                        <div className="space-y-2">
                          <div className="grid grid-cols-2 gap-2 text-xs md:grid-cols-4">
                            <div>
                              <span className="text-muted-foreground">
                                UPID:
                              </span>{" "}
                              <span className="font-mono break-all">
                                {task.upid}
                              </span>
                            </div>
                            <div>
                              <span className="text-muted-foreground">
                                PID:
                              </span>{" "}
                              {task.pid}
                            </div>
                            <div>
                              <span className="text-muted-foreground">
                                Started:
                              </span>{" "}
                              {formatTime(task.starttime)}
                            </div>
                            <div>
                              <span className="text-muted-foreground">
                                Ended:
                              </span>{" "}
                              {task.endtime ? formatTime(task.endtime) : "-"}
                            </div>
                          </div>
                          <TaskLogPanel pbsId={pbsId} upid={task.upid} />
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

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-xs text-muted-foreground">
            Page {safePage + 1} of {totalPages}
          </span>
          <div className="flex gap-1">
            <Button
              variant="outline"
              size="sm"
              disabled={safePage === 0}
              onClick={() => { setPage(safePage - 1); }}
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={safePage >= totalPages - 1}
              onClick={() => { setPage(safePage + 1); }}
            >
              <ChevronRight className="h-4 w-4" />
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
