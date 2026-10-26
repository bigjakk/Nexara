import { useEffect, useState } from "react";
import { Activity, ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { DataTableHeadCells } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { TaskLogSection } from "@/components/TaskLogSection";
import { displayProgress } from "@/components/layout/task-status";
import { useColumnLayout, type ColumnLayout } from "@/hooks/useColumnLayout";
import { formatDateTime } from "@/lib/format";
import { useTaskLogStore } from "@/stores/task-log-store";
import { selectClass } from "@/features/tasks/lib/task-filters";
import { detailPairs } from "@/features/events/lib/audit-details";
import { useTaskLog, useTaskStatus } from "../api/vm-queries";
import {
  GUEST_HISTORY_PAGE_SIZE,
  useGuestHistory,
  type GuestHistoryCategory,
  type GuestHistoryEntry,
  type GuestHistorySource,
  type GuestHistoryWindow,
} from "../api/guest-history-queries";
import {
  CATEGORY_OPTIONS,
  SOURCE_LABEL,
  SOURCE_OPTIONS,
  WINDOW_OPTIONS,
  historyActor,
  historyDisplayStatus,
  historyEventLabel,
  historySummary,
  upidWorkerType,
  type GuestHistoryCellCtx,
  type GuestHistoryColumnKey,
} from "../lib/guest-history";
import { GUEST_HISTORY_COLUMNS } from "../lib/guest-history-column-defs";
import { LiveBlockJobRows } from "./LiveBlockJobRows";

type HistoryLayout = ColumnLayout<
  GuestHistoryEntry,
  GuestHistoryColumnKey,
  GuestHistoryCellCtx
>;

/** The timeline is newest first and sorts no other way. */
const noSort = () => null;
const ignoreSort = () => undefined;

function HistoryRow({
  entry,
  clusterId,
  vmid,
  layout,
  expanded,
  onToggle,
}: {
  entry: GuestHistoryEntry;
  clusterId: string;
  vmid: number;
  layout: HistoryLayout;
  expanded: boolean;
  onToggle: () => void;
}) {
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);
  const hasTask = entry.upid !== "";

  // Live-poll ONLY a task the server still reports as running, as the task
  // tables do; useTaskStatus stops itself once the task has stopped.
  const { data: live } = useTaskStatus(
    clusterId,
    entry.outcome === "running" && hasTask ? entry.upid : null,
  );
  const display = historyDisplayStatus(entry, live);
  const progress =
    display === null
      ? null
      : displayProgress(display, entry.progress ?? null, live?.progress);
  const label = historyEventLabel(entry, vmid);
  const summary = historySummary(entry, vmid);
  const actor = historyActor(entry);

  const {
    data: logLines,
    isLoading: logLoading,
    isError: logError,
  } = useTaskLog(clusterId, hasTask ? entry.upid : null, expanded && hasTask);
  const details = expanded ? detailPairs(entry.details) : null;
  const exitText = entry.exit_status || live?.exit_status || "";

  return (
    <>
      <tr
        className="cursor-pointer border-b hover:bg-muted/20"
        onClick={onToggle}
      >
        <DataTableCells
          row={entry}
          layout={layout}
          ctx={{ label, summary, actor, display, progress, expanded }}
        />
      </tr>
      {expanded && (
        <tr className="border-b bg-muted/10">
          <td colSpan={layout.columns.length} className="px-4 py-3">
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">Event</span>
              <span className="font-medium">{label}</span>

              {summary !== "" && (
                <>
                  <span className="text-muted-foreground">Summary</span>
                  <span className="break-words">{summary}</span>
                </>
              )}

              <span className="text-muted-foreground">
                {hasTask
                  ? "Started"
                  : entry.kind === "status"
                    ? "Seen"
                    : "When"}
              </span>
              <span>{formatDateTime(entry.occurred_at)}</span>

              {entry.last_seen_at && (
                <>
                  <span className="text-muted-foreground">
                    Last seen {entry.from_status}
                  </span>
                  <span>{formatDateTime(entry.last_seen_at)}</span>
                </>
              )}

              {entry.finished_at && (
                <>
                  <span className="text-muted-foreground">Finished</span>
                  <span>{formatDateTime(entry.finished_at)}</span>
                </>
              )}

              <span className="text-muted-foreground">Source</span>
              <span>
                {SOURCE_LABEL[entry.source]}
                {actor !== "" && ` · ${actor}`}
              </span>

              {entry.node !== "" && (
                <>
                  <span className="text-muted-foreground">Node</span>
                  <span>{entry.node}</span>
                </>
              )}

              {hasTask && (
                <>
                  <span className="text-muted-foreground">Task type</span>
                  <span className="font-mono">
                    {upidWorkerType(entry.upid) || entry.task_type || "—"}
                  </span>
                </>
              )}

              {display === "failed" && exitText !== "" && (
                <>
                  <span className="text-muted-foreground">Exit status</span>
                  <span className="break-all text-red-500">{exitText}</span>
                </>
              )}

              {hasTask && (
                <>
                  <span className="text-muted-foreground">UPID</span>
                  <span className="break-all font-mono text-[10px]">
                    {entry.upid}
                  </span>
                </>
              )}
            </div>

            {entry.kind === "status" && (
              <p className="mt-2 text-xs text-muted-foreground">
                Nexara saw this change, and no task of this guest that could
                make it ran in between — in Nexara or in Proxmox.
              </p>
            )}

            {details && (
              <div className="mt-2 border-t pt-2">
                <span className="text-xs font-medium text-muted-foreground">
                  Details
                </span>
                <div className="mt-1 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
                  {details.map(([key, value]) => (
                    <div key={key} className="contents">
                      <span className="text-muted-foreground">{key}</span>
                      <span className="break-all">{value}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {hasTask && (
              <>
                <div className="mt-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={(e) => {
                      e.stopPropagation();
                      setFocusedTask({
                        clusterId,
                        upid: entry.upid,
                        description: label,
                      });
                    }}
                  >
                    <Activity className="mr-1 h-3 w-3" />
                    Live view
                  </Button>
                </div>
                <TaskLogSection
                  lines={logLines}
                  isLoading={logLoading}
                  isError={logError}
                />
              </>
            )}
          </td>
        </tr>
      )}
    </>
  );
}

/**
 * A guest's History tab: everything that happened to it, newest first — its
 * tasks in Nexara and in Proxmox with how each ended, its config and HA
 * changes, and the status changes no task explains. Rows expand to the task's
 * Proxmox log.
 *
 * Keyed on the Proxmox VMID within the cluster, as the server is: the history
 * outlives the guest's inventory row, and the other cluster's VM of the same
 * number is a different guest.
 *
 * A running VM's block jobs sit above the first page as live rows — the
 * Overview once showed them in a card of their own, which was empty almost
 * always.
 */
export function GuestHistoryTab({
  clusterId,
  vmId,
  vmid,
  showBlockJobs,
}: {
  clusterId: string;
  /** The guest's inventory row, which the block-jobs read is keyed on. */
  vmId: string;
  vmid: number;
  /** A running QEMU VM: the only guest with block jobs to read. */
  showBlockJobs: boolean;
}) {
  const [page, setPage] = useState(0);
  const [category, setCategory] = useState<GuestHistoryCategory | "">("");
  const [source, setSource] = useState<GuestHistorySource | "">("");
  const [timeWindow, setTimeWindow] = useState<GuestHistoryWindow>("");
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const layout = useColumnLayout("guest-history", GUEST_HISTORY_COLUMNS);

  const { data, isLoading, error } = useGuestHistory({
    clusterId,
    vmid,
    page,
    category,
    source,
    timeWindow,
    enabled: true,
  });
  const items = data?.items ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / GUEST_HISTORY_PAGE_SIZE));

  // Snap back when a refetch shrinks the result below the current page, but
  // not while the fetch is failing — see TaskHistoryTable for why.
  useEffect(() => {
    if (!error && page > totalPages - 1) setPage(totalPages - 1);
  }, [error, page, totalPages]);

  function filterSelect<T extends string>(
    label: string,
    value: T,
    options: readonly { value: T; label: string }[],
    onChange: (v: T) => void,
  ) {
    return (
      <select
        aria-label={label}
        className={selectClass}
        value={value}
        onChange={(e) => {
          onChange(e.target.value as T);
          setPage(0);
        }}
      >
        {options.map((o) => (
          <option key={o.value || "all"} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        {filterSelect("Type", category, CATEGORY_OPTIONS, setCategory)}
        {filterSelect("Source", source, SOURCE_OPTIONS, setSource)}
        {filterSelect("Time", timeWindow, WINDOW_OPTIONS, setTimeWindow)}
        <span className="flex-1" />
        <ResetColumnsButton layout={layout} />
      </div>

      {category === "backup" && (
        <p className="text-xs text-muted-foreground">
          A backup job that covers several guests is not listed here: its
          Proxmox task names no guest. The Backups tab has this guest&apos;s
          backups.
        </p>
      )}

      {isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : (
        <>
          <div className="overflow-x-auto rounded-md border">
            <table
              className="table-fixed text-sm"
              style={{ width: layout.totalWidth }}
            >
              <thead>
                <tr className="border-b bg-muted/50">
                  <DataTableHeadCells
                    layout={layout}
                    directionFor={noSort}
                    onSort={ignoreSort}
                  />
                </tr>
              </thead>
              <tbody>
                {showBlockJobs && page === 0 && (
                  <LiveBlockJobRows
                    clusterId={clusterId}
                    vmId={vmId}
                    colSpan={layout.columns.length}
                  />
                )}
                {/* A failed read keeps the live rows above it: they come from
                    QEMU, not from the history. */}
                {error && (
                  <tr>
                    <td
                      colSpan={layout.columns.length}
                      className="px-4 py-8 text-center text-destructive"
                    >
                      {error.message}
                    </td>
                  </tr>
                )}
                {!error &&
                  items.map((entry) => (
                    <HistoryRow
                      key={entry.id}
                      entry={entry}
                      clusterId={clusterId}
                      vmid={vmid}
                      layout={layout}
                      expanded={expandedId === entry.id}
                      onToggle={() => {
                        setExpandedId(
                          expandedId === entry.id ? null : entry.id,
                        );
                      }}
                    />
                  ))}
                {!error && items.length === 0 && (
                  <tr>
                    <td
                      colSpan={layout.columns.length}
                      className="px-4 py-8 text-center text-muted-foreground"
                    >
                      {category || source || timeWindow
                        ? "Nothing matches these filters."
                        : "Nothing has been recorded for this guest yet."}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-sm text-muted-foreground">
              {error ? "" : `${String(total)} entr${total === 1 ? "y" : "ies"}`}
            </p>
            {totalPages > 1 && (
              <div className="flex items-center gap-2">
                <Button
                  aria-label="Previous page"
                  variant="outline"
                  size="sm"
                  disabled={page === 0}
                  onClick={() => {
                    setPage(Math.max(0, page - 1));
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
                    setPage(page + 1);
                  }}
                >
                  <ChevronRight className="h-4 w-4" />
                </Button>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
