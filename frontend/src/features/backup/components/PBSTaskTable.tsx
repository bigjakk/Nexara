import { Fragment, useState, useMemo } from "react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ChevronRight, ChevronDown, ChevronLeft, Search } from "lucide-react";
import { DetailField } from "@/components/DetailField";
import { ExpandedDetailRow } from "@/components/ExpandedDetailRow";
import { DefGrid, Panel } from "@/components/page/Panel";
import type { PBSTask } from "../types/backup";
import { usePBSTaskLog } from "../api/backup-queries";
import {
  pbsTaskOutcome,
  pbsWorkerId,
  type PBSTaskOutcome,
} from "../lib/pbs-live-tasks";
import { StateText } from "./StateText";

const PAGE_SIZE = 25;

/** The outcome each choice of the status filter lists; "all" lists them all. */
const STATUS_FILTER_OUTCOME: Record<string, PBSTaskOutcome | undefined> = {
  running: "running",
  ok: "ok",
  warning: "warning",
  error: "failed",
};

function formatTime(unix: number): string {
  if (!unix) return "-";
  return new Date(unix * 1000).toLocaleString();
}

function formatDuration(start: number, end?: number): string {
  if (!end) return "running";
  const secs = end - start;
  if (secs < 60) return `${String(secs)}s`;
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return `${String(m)}m ${String(s)}s`;
}

function TaskLogPanel({ pbsId, upid }: { pbsId: string; upid: string }) {
  const { data: entries, isLoading } = usePBSTaskLog(pbsId, upid);

  if (isLoading) {
    return <p className="py-2 text-xs text-muted-foreground">Loading log...</p>;
  }

  if (!entries || entries.length === 0) {
    return <p className="py-2 text-xs text-muted-foreground">No log data.</p>;
  }

  return (
    <pre className="max-h-64 overflow-auto rounded-[7px] bg-foreground/[0.05] p-3 font-mono text-xs leading-relaxed">
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
    // By the outcome the Status cell draws, so a filter never lists a row
    // under a word its own cell does not wear: a finish with warnings is
    // amber, and is not an error.
    const wanted = STATUS_FILTER_OUTCOME[statusFilter];
    if (wanted !== undefined) {
      result = result.filter((t) => pbsTaskOutcome(t) === wanted);
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

  // A page number only means something against the list it was chosen for:
  // page 6 of ten pages of tasks is not page 6 of the three that survive a
  // search. Filtering therefore sends the operator back to the first page,
  // rather than dropping them at an arbitrary offset into the new results.
  //
  // The trigger is the filters, deliberately NOT `filtered` itself: that is
  // rebuilt whenever `tasks` is refetched, so keying off it would move the
  // operator on a background timer they cannot see — a worse version of the
  // bug this is fixing, not a fix for it.
  //
  // Adjusted during render rather than from an effect. React discards this
  // render and re-invokes the component before reconciling children, so the
  // stale offset never reaches the DOM; an effect would commit it and need a
  // second pass to correct it. Same shape as useExpandedRows — though there a
  // stale value is inert and the render phase only saves a commit, whereas
  // here it would slice the wrong rows.
  const [pageFor, setPageFor] = useState({ typeFilter, statusFilter, search });
  if (
    pageFor.typeFilter !== typeFilter ||
    pageFor.statusFilter !== statusFilter ||
    pageFor.search !== search
  ) {
    setPageFor({ typeFilter, statusFilter, search });
    setPage(0);
  }

  // Pagination. safePage covers the other way the page can fall out of range:
  // a poll returning fewer tasks shrinks totalPages with no filter change. The
  // pager buttons below step from safePage, not from page, so a clamped page
  // still advances by one rather than appearing stuck.
  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages - 1);
  const paged = filtered.slice(
    safePage * PAGE_SIZE,
    (safePage + 1) * PAGE_SIZE,
  );

  if (tasks.length === 0) {
    return (
      <Panel>
        <p className="py-6 text-center text-sm text-muted-foreground">
          No recent tasks.
        </p>
      </Panel>
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

  const selectClass =
    "h-8 rounded-md border bg-transparent px-2 text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-ring";

  return (
    <Panel
      flush
      title="Recent tasks"
      aside={`${String(filtered.length)} task${filtered.length !== 1 ? "s" : ""}`}
      actions={
        <>
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-2 h-4 w-4 text-muted-foreground" />
            <Input
              placeholder="Search tasks..."
              aria-label="Search tasks"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
              }}
              className="h-8 w-48 pl-8"
            />
          </div>
          <select
            aria-label="Task type"
            className={selectClass}
            value={typeFilter}
            onChange={(e) => {
              setTypeFilter(e.target.value);
            }}
          >
            <option value="all">All types</option>
            {taskTypes.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
          <select
            aria-label="Task status"
            className={selectClass}
            value={statusFilter}
            onChange={(e) => {
              setStatusFilter(e.target.value);
            }}
          >
            <option value="all">All statuses</option>
            <option value="running">Running</option>
            <option value="ok">OK</option>
            <option value="warning">Warnings</option>
            <option value="error">Error</option>
          </select>
        </>
      }
    >
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-8" />
            <TableHead>Task</TableHead>
            <TableHead>User</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Started</TableHead>
            <TableHead>Duration</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {paged.length === 0 && (
            <TableRow>
              <TableCell
                colSpan={6}
                className="py-8 text-center text-sm text-muted-foreground"
              >
                No matching tasks.
              </TableCell>
            </TableRow>
          )}
          {paged.map((task) => {
            const outcome = pbsTaskOutcome(task);
            const isExpanded = expanded.has(task.upid);
            const worker = pbsWorkerId(task.upid);

            return (
              <Fragment key={task.upid}>
                <TableRow
                  className="cursor-pointer"
                  onClick={() => {
                    toggleExpand(task.upid);
                  }}
                >
                  <TableCell className="pr-0 text-muted-foreground">
                    {isExpanded ? (
                      <ChevronDown className="h-4 w-4" />
                    ) : (
                      <ChevronRight className="h-4 w-4" />
                    )}
                  </TableCell>
                  <TableCell className="font-mono text-[12.5px]">
                    <span className="font-medium">{task.worker_type}</span>
                    {worker !== "" && (
                      <span className="ml-2 text-muted-foreground">
                        {worker}
                      </span>
                    )}
                  </TableCell>
                  <TableCell>{task.user}</TableCell>
                  <TableCell className="max-w-[20rem]">
                    {outcome === "running" && (
                      <StateText tone="running">Running</StateText>
                    )}
                    {outcome === "ok" && <StateText tone="ok">OK</StateText>}
                    {outcome === "warning" && (
                      <StateText tone="warn" title={task.status}>
                        {task.status}
                      </StateText>
                    )}
                    {outcome === "failed" && (
                      <StateText tone="bad" title={task.status}>
                        {task.status}
                      </StateText>
                    )}
                  </TableCell>
                  <TableCell className="whitespace-nowrap">
                    {formatTime(task.starttime)}
                  </TableCell>
                  <TableCell className="whitespace-nowrap tabular-nums">
                    {formatDuration(task.starttime, task.endtime)}
                  </TableCell>
                </TableRow>
                {isExpanded && (
                  <ExpandedDetailRow colSpan={6}>
                    <DefGrid>
                      <DetailField label="Process ID" variant="mono">
                        {task.pid}
                      </DetailField>
                      <DetailField label="Started">
                        {formatTime(task.starttime)}
                      </DetailField>
                      <DetailField label="Ended">
                        {task.endtime ? formatTime(task.endtime) : "-"}
                      </DetailField>
                    </DefGrid>
                    <dl className="text-sm">
                      <dt className="text-xs text-muted-foreground">UPID</dt>
                      <dd className="break-all font-mono text-xs">
                        {task.upid}
                      </dd>
                    </dl>
                    <TaskLogPanel pbsId={pbsId} upid={task.upid} />
                  </ExpandedDetailRow>
                )}
              </Fragment>
            );
          })}
        </TableBody>
      </Table>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-between border-t px-[18px] pt-3">
          <span className="text-[12.5px] text-muted-foreground">
            Page {safePage + 1} of {totalPages}
          </span>
          <div className="flex gap-1">
            <Button
              aria-label="Previous page"
              variant="outline"
              size="sm"
              disabled={safePage === 0}
              onClick={() => {
                setPage(safePage - 1);
              }}
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <Button
              aria-label="Next page"
              variant="outline"
              size="sm"
              disabled={safePage >= totalPages - 1}
              onClick={() => {
                setPage(safePage + 1);
              }}
            >
              <ChevronRight className="h-4 w-4" />
            </Button>
          </div>
        </div>
      )}
    </Panel>
  );
}
