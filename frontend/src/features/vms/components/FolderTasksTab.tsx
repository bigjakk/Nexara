import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  MAX_TASK_VMIDS_FILTER,
  useTasks,
  type TaskRecord,
} from "@/features/tasks/api/tasks-queries";
import {
  TaskRow,
  TaskTableHeader,
} from "@/features/tasks/components/TasksPanel";
import { selectClass, statusFilters } from "@/features/tasks/lib/task-filters";
import { useTaskSort } from "@/features/tasks/lib/task-columns";
import { TASK_COLUMNS_WITH_VM } from "@/features/tasks/lib/task-column-defs";
import { useColumnLayout } from "@/hooks/useColumnLayout";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { upidVmid } from "@/lib/upid";

const PAGE_SIZE = 50;

export interface FolderVMLink {
  name: string;
  path: string;
}

interface FolderTasksTabProps {
  clusterId: string;
  clusterName: string;
  /** Proxmox VMIDs of the folder's VMs — passed to the server-side vmids
   * filter (task_history.vmid, parsed from the UPID at insert). */
  vmids: Set<number>;
  vmLinkByVmid: Map<number, FolderVMLink>;
}

/** The server stores vmid on each row; the UPID parse is only a fallback for
 * rows that predate migration 000076 in an already-open session. */
function taskVmid(task: TaskRecord): number | null {
  return task.vmid ?? upidVmid(task.upid);
}

export function FolderTasksTab({
  clusterId,
  clusterName,
  vmids,
  vmLinkByVmid,
}: FolderTasksTabProps) {
  const [statusFilter, setStatusFilter] = useState("");
  const [page, setPage] = useState(0);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const { sort, toggle, directionFor } = useTaskSort(() => {
    setPage(0);
  });
  const layout = useColumnLayout("folder-tasks", TASK_COLUMNS_WITH_VM);

  const vmidList = useMemo(() => [...vmids].sort((a, b) => a - b), [vmids]);

  // An empty folder must not fetch: absent vmids means "no filter" server-side
  // and would return the whole cluster's history. A folder over the server's
  // vmids cap must not fetch either — the request would just 400.
  const hasVMs = vmidList.length > 0;
  const tooManyVMs = vmidList.length > MAX_TASK_VMIDS_FILTER;
  const { data, isLoading, error } = useTasks({
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    clusterId,
    status: statusFilter || undefined,
    sort: sort.key,
    order: sort.direction,
    vmids: vmidList,
    enabled: hasVMs && !tooManyVMs,
  });

  const total = hasVMs ? (data?.total ?? 0) : 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const items = hasVMs ? (data?.items ?? []) : [];

  // Snap back when a refetch (WS invalidation / 60s poll) shrinks the result
  // below the current page — otherwise a stale page index strands an empty
  // table. Safe against transient zeros: placeholderData in useTasks keeps
  // the previous `total` while the next page loads.
  useEffect(() => {
    if (page > totalPages - 1) setPage(totalPages - 1);
  }, [page, totalPages]);

  if (tooManyVMs) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        This folder spans {vmidList.length} VMs — more than the{" "}
        {MAX_TASK_VMIDS_FILTER} the task filter supports. Use the{" "}
        <Link to="/events" className="underline">
          Events page
        </Link>{" "}
        for full task history.
      </p>
    );
  }
  if (hasVMs && isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    );
  }
  if (hasVMs && error) {
    return <p className="text-destructive">{error.message}</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <select
          className={selectClass}
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value);
            setPage(0);
          }}
        >
          {statusFilters.map((s) => (
            <option key={s.value || "all"} value={s.value}>
              {s.label}
            </option>
          ))}
        </select>
        <span className="flex-1" />
        <ResetColumnsButton layout={layout} />
      </div>

      <div className="overflow-x-auto rounded-md border">
        <table
          className="table-fixed text-sm"
          style={{ width: layout.totalWidth }}
        >
          <TaskTableHeader
            layout={layout}
            directionFor={directionFor}
            onSort={toggle}
          />
          <tbody>
            {items.map((task) => {
              const vmid = taskVmid(task);
              const link = vmid !== null ? vmLinkByVmid.get(vmid) : undefined;
              return (
                <TaskRow
                  key={task.id}
                  task={task}
                  clusterName={clusterName}
                  layout={layout}
                  vmName={
                    link ? (
                      <Link
                        to={link.path}
                        className="hover:underline"
                        onClick={(e) => {
                          e.stopPropagation();
                        }}
                      >
                        {link.name}
                      </Link>
                    ) : (
                      <span className="text-muted-foreground">
                        {vmid !== null ? `#${String(vmid)}` : "—"}
                      </span>
                    )
                  }
                  expanded={expandedId === task.id}
                  onToggle={() => {
                    setExpandedId(expandedId === task.id ? null : task.id);
                  }}
                />
              );
            })}
            {items.length === 0 && (
              <tr>
                <td
                  colSpan={layout.columns.length}
                  className="px-4 py-8 text-center text-muted-foreground"
                >
                  No tasks for VMs in this folder.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          {String(total)} task{total === 1 ? "" : "s"}
        </p>
        {totalPages > 1 && (
          <div className="flex items-center gap-2">
            <Button
              aria-label="Previous page"
              variant="outline"
              size="sm"
              disabled={page === 0}
              onClick={() => {
                setPage((p) => Math.max(0, p - 1));
              }}
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <span className="text-sm">
              Page {page + 1} of {totalPages}
            </span>
            <Button
              aria-label="Next page"
              variant="outline"
              size="sm"
              disabled={page + 1 >= totalPages}
              onClick={() => {
                setPage((p) => p + 1);
              }}
            >
              <ChevronRight className="h-4 w-4" />
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
