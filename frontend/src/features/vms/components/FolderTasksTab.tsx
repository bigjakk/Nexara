import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  MAX_TASK_VMIDS_FILTER,
  useTasks,
  type TaskRecord,
} from "@/features/tasks/api/tasks-queries";
import {
  TaskHistoryTable,
  TaskStatusFilter,
} from "@/features/tasks/components/TasksPanel";
import { useTaskSort } from "@/features/tasks/lib/task-columns";
import { PAGE_SIZE } from "@/features/tasks/lib/task-filters";
import { TASK_COLUMNS_WITH_VM } from "@/features/tasks/lib/task-column-defs";
import { useColumnLayout } from "@/hooks/useColumnLayout";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { upidVmid } from "@/lib/upid";

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

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <TaskStatusFilter
          value={statusFilter}
          onChange={(v) => {
            setStatusFilter(v);
            setPage(0);
          }}
        />
        <span className="flex-1" />
        <ResetColumnsButton layout={layout} />
      </div>

      <TaskHistoryTable
        layout={layout}
        directionFor={directionFor}
        onSort={toggle}
        items={hasVMs ? (data?.items ?? []) : []}
        total={hasVMs ? (data?.total ?? 0) : 0}
        page={page}
        onPageChange={setPage}
        isLoading={hasVMs && isLoading}
        error={hasVMs ? error : null}
        clusterName={() => clusterName}
        vmName={(task) => {
          const vmid = taskVmid(task);
          const link = vmid !== null ? vmLinkByVmid.get(vmid) : undefined;
          if (!link) {
            return (
              <span className="text-muted-foreground">
                {vmid !== null ? `#${String(vmid)}` : "—"}
              </span>
            );
          }
          return (
            <Link
              to={link.path}
              className="hover:underline"
              onClick={(e) => {
                e.stopPropagation();
              }}
            >
              {link.name}
            </Link>
          );
        }}
        emptyMessage="No tasks for VMs in this folder."
      />
    </div>
  );
}
