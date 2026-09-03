import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronUp, ChevronDown, Loader2 } from "lucide-react";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  useRecentActivity,
  type AuditLogEntry,
} from "@/features/audit/api/audit-queries";
import { useTaskStatus, useTaskLog } from "@/features/vms/api/vm-queries";
import { DataTableHeadCells } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { useTableSort } from "@/hooks/useTableSort";
import { useColumnLayout, type ColumnLayout } from "@/hooks/useColumnLayout";
import { parseDetails } from "./task-status";
import {
  ACTIVITY_ACCESSORS,
  DEFAULT_ACTIVITY_SORT,
  activityRowKey,
  decorateActivity,
  formatTimestamp,
  type ActivityRowData,
  type ActivitySortKey,
  type LiveTaskStatus,
} from "./activity-columns";
import {
  ACTIVITY_COLUMN_DEFS,
  type ActivityCellCtx,
} from "./activity-column-defs";

function getClusterIdFromEntry(entry: AuditLogEntry): string {
  return entry.cluster_id ?? "";
}

/**
 * Polls Proxmox for a running task's live status to drive the progress bar.
 * Mounted only for entries the server reports as running, and useTaskStatus
 * stops polling once the task is stopped — so the working set is tiny and
 * self-emptying. No entry-age gate: status correctness comes from the server.
 */
function ActiveTaskPoller({
  entry,
  onStatus,
}: {
  entry: AuditLogEntry;
  onStatus: (
    upid: string,
    status: string,
    exitStatus: string,
    progress?: number,
  ) => void;
}) {
  const details = parseDetails(entry.details);
  const clusterId = getClusterIdFromEntry(entry);
  const upid = details.upid ?? null;

  const { data: task } = useTaskStatus(clusterId, upid);

  const prevRef = useRef<string | null>(null);
  useEffect(() => {
    if (task && upid) {
      const key = `${task.status}:${task.exit_status}:${String(task.progress ?? "")}`;
      if (prevRef.current !== key) {
        prevRef.current = key;
        onStatus(upid, task.status, task.exit_status, task.progress);
      }
    }
  }, [task, upid, onStatus]);

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
  const { entry, details, upid, status } = row;
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
              <span>{entry.cluster_name || "—"}</span>
              <span className="text-muted-foreground">User</span>
              <span>{entry.user_display_name || entry.user_email}</span>
              <span className="text-muted-foreground">Time</span>
              <span>{formatTimestamp(entry.created_at)}</span>
              {isFailed && row.exitStatusText !== "" && (
                <>
                  <span className="text-muted-foreground">Exit Status</span>
                  <span className="text-red-500">{row.exitStatusText}</span>
                </>
              )}
              {details.upid && (
                <>
                  <span className="text-muted-foreground">UPID</span>
                  <span className="break-all font-mono text-[10px]">
                    {details.upid}
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
              <div className="mt-2 border-t pt-2">
                <span className="text-xs font-medium text-muted-foreground">
                  {t("log")}
                </span>
                {logLoading && (
                  <div className="mt-1 flex items-center gap-1 text-xs text-muted-foreground">
                    <Loader2 className="h-3 w-3 animate-spin" />
                    {t("loadingLog")}
                  </div>
                )}
                {logLines && logLines.length > 0 && (
                  <pre className="mt-1 max-h-40 overflow-auto rounded bg-muted/50 p-2 font-mono text-[11px] leading-relaxed">
                    {logLines.map((line) => line.t).join("\n")}
                  </pre>
                )}
                {logLines && logLines.length === 0 && (
                  <div className="mt-1 text-xs text-muted-foreground">
                    {t("noLogOutput")}
                  </div>
                )}
              </div>
            )}
          </td>
        </tr>
      )}
    </>
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

  // Header counts come from the server-authoritative task_history status.
  const runningCount =
    entries?.filter((e) => e.task_status === "running").length ?? 0;
  const failedCount =
    entries?.filter((e) => e.task_status === "failed").length ?? 0;

  // Live-poll only tasks the server reports as running — for the progress bar
  // and to flip to done between reconcile ticks. No entry-age gate.
  const runningWithUpids =
    entries?.filter(
      (e) => e.task_status === "running" && !!parseDetails(e.details).upid,
    ) ?? [];

  // Resolve every cell's value once, then sort on those same values.
  //
  // Sorting is client-side here, unlike the Tasks page. useRecentActivity
  // fetches a fixed window (the server's LIMIT 50, newest first) rather than
  // paging, so re-ordering it cannot hide rows that a next page would have
  // held — there is no next page. It ranks WITHIN the 50 most recent entries
  // and nothing further back; a failure older than that is not on screen to
  // be sorted to the top, which is why the Events → Tasks table, and not this
  // drawer, is where a full failure history is read.
  const rows = useMemo(
    () => (entries ?? []).map((e) => decorateActivity(e, taskStatuses)),
    [entries, taskStatuses],
  );

  const {
    rows: sortedRows,
    toggle,
    directionFor,
  } = useTableSort(
    rows,
    ACTIVITY_ACCESSORS,
    activityRowKey,
    DEFAULT_ACTIVITY_SORT,
  );
  const layout = useColumnLayout("activity", ACTIVITY_COLUMN_DEFS);

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
      {runningWithUpids.map((e) => (
        <ActiveTaskPoller key={e.id} entry={e} onStatus={handleTaskStatus} />
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

      {/* Header bar */}
      <div
        className="flex h-7 cursor-pointer items-center gap-2 border-b px-3 text-xs select-none"
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
        {failedCount > 0 && (
          <span className="rounded-full bg-red-500/20 px-1.5 py-0.5 text-red-600 dark:text-red-400">
            {failedCount} {t("failed").toLowerCase()}
          </span>
        )}
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
          {(!entries || entries.length === 0) && (
            <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
              {t("noActivity")}
            </div>
          )}
          {entries && entries.length > 0 && (
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
                {sortedRows.map((row) => (
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
                      if (row.upid && row.entry.cluster_id) {
                        setFocusedTask({
                          clusterId: row.entry.cluster_id,
                          upid: row.upid,
                          description: `${row.actionLabel} — ${row.resourceLabel}`,
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
