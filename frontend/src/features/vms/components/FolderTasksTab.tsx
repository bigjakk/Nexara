import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useTasks } from "@/features/tasks/api/tasks-queries";
import { TaskRow } from "@/features/tasks/components/TasksPanel";
import { selectClass, statusFilters } from "@/features/tasks/lib/task-filters";
import { upidVmid } from "@/lib/upid";

/** How many recent cluster tasks to scan for folder membership — must not
 * exceed the server's limit clamp (internal/api/handlers/tasks.go caps limit
 * at 200, silently). Older tasks are reachable from the Events page, which
 * paginates the full history server-side. */
const FETCH_WINDOW = 200;
const PAGE_SIZE = 50;

export interface FolderVMLink {
  name: string;
  path: string;
}

interface FolderTasksTabProps {
  clusterId: string;
  clusterName: string;
  /** Proxmox VMIDs of the folder's VMs — tasks are matched by the UPID id field. */
  vmids: Set<number>;
  vmLinkByVmid: Map<number, FolderVMLink>;
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

  const { data, isLoading, error } = useTasks({
    limit: FETCH_WINDOW,
    offset: 0,
    clusterId,
  });

  const filtered = useMemo(
    () =>
      (data?.items ?? []).filter((task) => {
        const vmid = upidVmid(task.upid);
        if (vmid === null || !vmids.has(vmid)) return false;
        return statusFilter === "" || task.status === statusFilter;
      }),
    [data, vmids, statusFilter],
  );

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  // Clamp rather than trust state: a refetch (WS invalidation / 60s poll)
  // can shrink the filtered set below the current page, which would
  // otherwise strand a stale page index past the end of the list.
  const safePage = Math.min(page, totalPages - 1);
  const pageItems = filtered.slice(
    safePage * PAGE_SIZE,
    (safePage + 1) * PAGE_SIZE,
  );
  const scanned = data?.items.length ?? 0;
  const truncated = data ? data.total > data.items.length : false;

  if (isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    );
  }
  if (error) {
    return <p className="text-destructive">{error.message}</p>;
  }

  return (
    <div className="space-y-4">
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

      <div className="overflow-x-auto rounded-md border">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="px-4 py-2 text-left font-medium">Started</th>
              <th className="px-4 py-2 text-left font-medium">Cluster</th>
              <th className="px-4 py-2 text-left font-medium">Type</th>
              <th className="px-4 py-2 text-left font-medium">Description</th>
              <th className="px-4 py-2 text-left font-medium">VM</th>
              <th className="px-4 py-2 text-left font-medium">Node</th>
              <th className="px-4 py-2 text-left font-medium">Status</th>
            </tr>
          </thead>
          <tbody>
            {pageItems.map((task) => {
              const vmid = upidVmid(task.upid);
              const link = vmid !== null ? vmLinkByVmid.get(vmid) : undefined;
              return (
                <TaskRow
                  key={task.id}
                  task={task}
                  clusterName={clusterName}
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
            {pageItems.length === 0 && (
              <tr>
                <td
                  colSpan={7}
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
          {String(filtered.length)} task{filtered.length === 1 ? "" : "s"}
          {truncated &&
            ` — scanned the ${String(scanned)} most recent cluster tasks`}
        </p>
        {totalPages > 1 && (
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={safePage === 0}
              onClick={() => {
                setPage(Math.max(0, safePage - 1));
              }}
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>
            <span className="text-sm">
              Page {safePage + 1} of {totalPages}
            </span>
            <Button
              variant="outline"
              size="sm"
              disabled={safePage + 1 >= totalPages}
              onClick={() => {
                setPage(safePage + 1);
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
