import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronUp, ChevronDown } from "lucide-react";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  useRecentActivity,
  type AuditLogEntry,
} from "@/features/audit/api/audit-queries";
import { auditClusterLabel } from "@/features/audit/lib/audit-cluster";
import { useTaskStatus, useTaskLog } from "@/features/tasks/api/tasks-queries";
import { DataTableHeadCells } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { TaskLogSection } from "@/components/TaskLogSection";
import { Button } from "@/components/ui/button";
import { useDataTable } from "@/hooks/useDataTable";
import type { ColumnLayout } from "@/hooks/useColumnLayout";
import { formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  DEFAULT_ACTIVITY_SORT,
  activityLabel,
  activityRowKey,
  decorateActivity,
  filterActivity,
  isErrorActivity,
  pinRunningFirst,
  type ActivityFilter,
  type ActivityRowData,
  type ActivitySortKey,
  type LiveTaskStatus,
} from "./activity-columns";
import {
  ACTIVITY_COLUMN_DEFS,
  type ActivityCellCtx,
} from "./activity-column-defs";

// A removed cluster's entry keeps its id, but nothing can be asked of that
// cluster any more, so it is treated as having none.
function getClusterIdFromEntry(entry: AuditLogEntry): string {
  return entry.cluster_removed ? "" : (entry.cluster_id ?? "");
}

/**
 * Polls Proxmox for a running task's live status to drive the progress bar.
 * Mounted only for entries the server reports as running, and useTaskStatus
 * stops polling once the task is stopped — so the working set is tiny and
 * self-emptying. No entry-age gate: status correctness comes from the server.
 *
 * Takes the cluster and UPID rather than the entry: decorateActivity has
 * already parsed the details the caller selected these rows by, so re-parsing
 * them here would be the second parse of the same JSON per row per render.
 */
function ActiveTaskPoller({
  clusterId,
  upid,
  onStatus,
}: {
  clusterId: string;
  upid: string;
  onStatus: (
    upid: string,
    status: string,
    exitStatus: string,
    progress?: number,
  ) => void;
}) {
  const { data: task, isRefetchError } = useTaskStatus(clusterId, upid);

  const prevRef = useRef<string | null>(null);
  useEffect(() => {
    if (task) {
      // A failed refetch keeps the last good answer. "Stopped" stays true, but
      // "running" no longer confirms anything: the node may be gone. A blank
      // status leaves the row on the server's word, and unpinned.
      const status =
        isRefetchError && task.status !== "stopped" ? "" : task.status;
      const key = `${status}:${task.exit_status}:${String(task.progress ?? "")}`;
      if (prevRef.current !== key) {
        prevRef.current = key;
        onStatus(upid, status, task.exit_status, task.progress);
      }
    }
  }, [task, isRefetchError, upid, onStatus]);

  return null;
}

type ActivityLayout = ColumnLayout<
  ActivityRowData,
  ActivitySortKey,
  ActivityCellCtx
>;

function ActivityRow({
  row,
  expanded,
  onToggle,
  onFocus,
  layout,
}: {
  row: ActivityRowData;
  expanded: boolean;
  onToggle: () => void;
  onFocus: () => void;
  /** The parent's layout — the SAME instance the header row uses. */
  layout: ActivityLayout;
}) {
  const { t } = useTranslation("common");
  const { entry, details, status } = row;
  const upid = details.upid;
  const hasUpid = !!upid;
  const clusterId = getClusterIdFromEntry(entry);

  const isFailed = status === "failed";

  const { data: logLines, isLoading: logLoading } = useTaskLog(
    clusterId,
    upid ?? null,
    expanded && hasUpid,
  );

  return (
    <>
      <tr
        className="cursor-pointer border-b transition-colors hover:bg-muted/30"
        onClick={onToggle}
        onDoubleClick={(e) => {
          e.stopPropagation();
          onFocus();
        }}
      >
        <DataTableCells row={row} layout={layout} ctx={{ expanded }} />
      </tr>
      {expanded && (
        <tr className="border-b bg-muted/10">
          <td colSpan={layout.columns.length} className="px-4 py-2">
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">Action</span>
              <span>{entry.action}</span>
              <span className="text-muted-foreground">Resource</span>
              <span>
                {entry.resource_type} / {entry.resource_id}
              </span>
              <span className="text-muted-foreground">Cluster</span>
              <span>{auditClusterLabel(entry, "—")}</span>
              <span className="text-muted-foreground">User</span>
              <span>{row.actorLabel || "—"}</span>
              <span className="text-muted-foreground">Time</span>
              <span>{formatDateTime(entry.created_at)}</span>
              {isFailed && row.exitStatusText !== "" && (
                <>
                  <span className="text-muted-foreground">Exit Status</span>
                  <span className="text-red-500">{row.exitStatusText}</span>
                </>
              )}
              {upid && (
                <>
                  <span className="text-muted-foreground">UPID</span>
                  <span className="break-all font-mono text-[10px]">
                    {upid}
                  </span>
                </>
              )}
              {details.node && (
                <>
                  <span className="text-muted-foreground">Node</span>
                  <span>{details.node}</span>
                </>
              )}
              {Object.keys(details).filter(
                (k) => !["upid", "node", "vmid"].includes(k),
              ).length > 0 && (
                <>
                  <span className="text-muted-foreground">{t("details")}</span>
                  <span className="break-all font-mono text-[10px]">
                    {JSON.stringify(
                      Object.fromEntries(
                        Object.entries(details).filter(
                          ([k]) => !["upid", "node", "vmid"].includes(k),
                        ),
                      ),
                    )}
                  </span>
                </>
              )}
            </div>

            {/* Task Log Output (for UPID-bearing entries) */}
            {hasUpid && (
              <TaskLogSection lines={logLines} isLoading={logLoading} />
            )}
          </td>
        </tr>
      )}
    </>
  );
}

/**
 * All, errors or the rest. The error count lives here, and reads red whenever
 * it is not zero, selected or not.
 */
function ActivityFilterControl({
  filter,
  errorCount,
  onSelect,
}: {
  filter: ActivityFilter;
  /** Null until the feed has answered: "0 errors" would be a false all-clear. */
  errorCount: number | null;
  onSelect: (filter: ActivityFilter) => void;
}) {
  const { t } = useTranslation("common");
  const segments: { value: ActivityFilter; label: string }[] = [
    { value: "all", label: t("all") },
    {
      value: "errors",
      label:
        errorCount === null
          ? t("activityErrors")
          : t("activityErrorCount", { count: errorCount }),
    },
    { value: "other", label: t("others") },
  ];

  return (
    <div
      role="group"
      aria-label={t("filterActivity")}
      className="flex items-center gap-px rounded-md border p-px"
      onClick={(e) => {
        // The whole header bar toggles the panel; choosing a filter must not
        // also collapse it.
        e.stopPropagation();
      }}
    >
      {segments.map(({ value, label }) => {
        const selected = filter === value;
        const alarming = value === "errors" && (errorCount ?? 0) > 0;
        return (
          <Button
            key={value}
            type="button"
            variant="ghost"
            aria-pressed={selected}
            className={cn(
              "h-5 rounded-sm px-1.5 py-0 text-xs font-normal",
              alarming
                ? "text-red-600 hover:bg-red-500/10 hover:text-red-600 dark:text-red-400 dark:hover:text-red-400"
                : "text-muted-foreground",
              selected && alarming && "bg-red-500/20 font-medium",
              selected && !alarming && "bg-muted font-medium text-foreground",
            )}
            onClick={() => {
              onSelect(value);
            }}
          >
            {label}
          </Button>
        );
      })}
    </div>
  );
}

export function TaskLogPanel() {
  const { t } = useTranslation("common");
  const panelOpen = useTaskLogStore((s) => s.panelOpen);
  const panelHeight = useTaskLogStore((s) => s.panelHeight);
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setPanelHeight = useTaskLogStore((s) => s.setPanelHeight);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  const { data: entries } = useRecentActivity();

  const [expandedId, setExpandedId] = useState<string | null>(null);

  // Not persisted, unlike the panel's height: an "errors only" left on from last
  // week would hide the running tasks from someone who has forgotten it.
  const [filter, setFilter] = useState<ActivityFilter>("all");

  const selectFilter = (next: ActivityFilter) => {
    // On a collapsed panel the click asks to see that list. On an open one,
    // the selected segment toggles back to everything.
    if (!panelOpen) {
      setPanelOpen(true);
      setFilter(next);
      return;
    }
    setFilter(filter === next ? "all" : next);
  };

  // Track live task statuses from pollers
  const [taskStatuses, setTaskStatuses] = useState<
    Record<string, LiveTaskStatus>
  >({});

  const handleTaskStatus = useCallback(
    (upid: string, status: string, exitStatus: string, progress?: number) => {
      setTaskStatuses((prev) => {
        const existing = prev[upid];
        if (
          existing?.status === status &&
          existing.exitStatus === exitStatus &&
          existing.progress === progress
        ) {
          return prev;
        }
        const entry =
          progress != null
            ? { status, exitStatus, progress }
            : { status, exitStatus };
        return { ...prev, [upid]: entry };
      });
    },
    [],
  );

  // Resolve every cell's value once, then sort on those same values.
  //
  // Sorting is client-side here, unlike the Tasks page. useRecentActivity
  // fetches a fixed window (the 50 most recent entries, and the long tasks
  // ListRecentAuditLogEnriched keeps with them) rather than paging, so
  // re-ordering it cannot hide rows that a next page would have held — there is
  // no next page. It ranks WITHIN that window and nothing further back; a
  // failure older than that is not on screen to be sorted to the top, which is
  // why the Events page, not this drawer, is where a full history is read: Tasks
  // for failed tasks, Audit Log for errors with no task behind them.
  const rows = useMemo(
    () => (entries ?? []).map((e) => decorateActivity(e, taskStatuses)),
    [entries, taskStatuses],
  );

  // Counted off the decorated rows, so each count says what its rows show, and
  // the error one is exactly what its filter selects.
  const runningCount = rows.filter((r) => r.status === "running").length;
  const errorCount = entries ? rows.filter(isErrorActivity).length : null;

  const filteredRows = useMemo(
    () => filterActivity(rows, filter),
    [rows, filter],
  );

  // Live-poll only tasks the server reports as running — for the progress bar
  // and to flip to done between reconcile ticks. No entry-age gate. Read off
  // the decorated rows so the details JSON is parsed once per entry, not again
  // per poller.
  const runningPolls = rows.flatMap((r) =>
    r.entry.task_status === "running" && r.details.upid
      ? [
          {
            id: r.entry.id,
            clusterId: getClusterIdFromEntry(r.entry),
            upid: r.details.upid,
          },
        ]
      : [],
  );

  const {
    layout,
    rows: sortedRows,
    sort,
    toggle,
    directionFor,
  } = useDataTable(
    "activity",
    ACTIVITY_COLUMN_DEFS,
    filteredRows,
    activityRowKey,
    DEFAULT_ACTIVITY_SORT,
  );
  const visibleRows = useMemo(
    () => pinRunningFirst(sortedRows, sort),
    [sortedRows, sort],
  );

  const dragRef = useRef<{ startY: number; startHeight: number } | null>(null);

  const handlePointerDown = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault();
      dragRef.current = { startY: e.clientY, startHeight: panelHeight };
      const el = e.currentTarget as HTMLElement;
      el.setPointerCapture(e.pointerId);
    },
    [panelHeight],
  );

  const handlePointerMove = useCallback(
    (e: React.PointerEvent) => {
      if (!dragRef.current) return;
      const delta = dragRef.current.startY - e.clientY;
      setPanelHeight(dragRef.current.startHeight + delta);
    },
    [setPanelHeight],
  );

  const handlePointerUp = useCallback(() => {
    dragRef.current = null;
  }, []);

  return (
    <div className="flex flex-col border-t bg-card">
      {/* Invisible progress pollers for running tasks */}
      {runningPolls.map((poll) => (
        <ActiveTaskPoller
          key={poll.id}
          clusterId={poll.clusterId}
          upid={poll.upid}
          onStatus={handleTaskStatus}
        />
      ))}

      {/* Resize handle — only visible when panel is open */}
      {panelOpen && (
        <div
          className="h-1 cursor-row-resize bg-border hover:bg-primary/30"
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={handlePointerUp}
        />
      )}

      {/* Header bar. On a phone it wraps rather than clip Reset columns. */}
      <div
        className="flex min-h-7 cursor-pointer flex-wrap items-center gap-x-2 border-b px-3 text-xs select-none sm:h-7 sm:flex-nowrap"
        onClick={() => {
          setPanelOpen(!panelOpen);
        }}
      >
        <span className="font-medium">{t("activity")}</span>
        {runningCount > 0 && (
          <span className="rounded-full bg-blue-500/20 px-1.5 py-0.5 text-blue-600 dark:text-blue-400">
            {runningCount} {t("running").toLowerCase()}
          </span>
        )}
        {/* Always there: hidden at zero, it read as a feature gone missing. */}
        <ActivityFilterControl
          filter={filter}
          errorCount={errorCount}
          onSelect={selectFilter}
        />
        <div className="flex-1" />
        {panelOpen && (
          <span
            onClick={(e) => {
              // The whole header bar toggles the panel; resetting must not
              // also collapse it.
              e.stopPropagation();
            }}
          >
            <ResetColumnsButton layout={layout} />
          </span>
        )}
        {panelOpen ? (
          <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
        ) : (
          <ChevronUp className="h-3.5 w-3.5 text-muted-foreground" />
        )}
      </div>

      {/* Activity list */}
      {panelOpen && (
        <div className="overflow-auto" style={{ height: panelHeight }}>
          {visibleRows.length === 0 && (
            <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
              {rows.length === 0 ? t("noActivity") : t("noMatchingActivity")}
            </div>
          )}
          {visibleRows.length > 0 && (
            <table
              className="table-fixed text-xs"
              style={{ width: layout.totalWidth }}
            >
              <thead>
                <tr className="border-b">
                  <DataTableHeadCells
                    layout={layout}
                    directionFor={directionFor}
                    onSort={toggle}
                  />
                </tr>
              </thead>
              <tbody>
                {visibleRows.map((row) => (
                  <ActivityRow
                    key={row.entry.id}
                    row={row}
                    expanded={expandedId === row.entry.id}
                    onToggle={() => {
                      setExpandedId(
                        expandedId === row.entry.id ? null : row.entry.id,
                      );
                    }}
                    layout={layout}
                    onFocus={() => {
                      const clusterId = getClusterIdFromEntry(row.entry);
                      if (row.details.upid && clusterId) {
                        setFocusedTask({
                          clusterId,
                          upid: row.details.upid,
                          description: activityLabel(row),
                        });
                      }
                    }}
                  />
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}
