import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronUp } from "lucide-react";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  useRecentActivity,
  type AuditLogEntry,
} from "@/features/audit/api/audit-queries";
import { auditClusterLabel } from "@/features/audit/lib/audit-cluster";
import { useTaskStatus, useTaskLog } from "@/features/tasks/api/tasks-queries";
import { DataTableHeadCells } from "@/components/DataTableHeadCells";
import { DataTableCells } from "@/components/DataTableCells";
import { DetailField } from "@/components/DetailField";
import { ResetColumnsButton } from "@/components/ResetColumnsButton";
import { TaskLogSection } from "@/components/TaskLogSection";
import { TaskProgressCell } from "@/components/TaskProgressCell";
import { Segmented, type SegmentedOption } from "@/components/ui/segmented";
import { useDataTable } from "@/hooks/useDataTable";
import type { ColumnLayout } from "@/hooks/useColumnLayout";
import { cn } from "@/lib/utils";
import {
  DEFAULT_ACTIVITY_SORT,
  activityLabel,
  activityRowKey,
  decorateActivities,
  filterActivity,
  isErrorActivity,
  pinRunningFirst,
  summarizeActivity,
  type ActivityFilter,
  type ActivityRowData,
  type ActivitySortKey,
  type ActivitySummary,
  type LiveTaskStatus,
} from "./activity-columns";
import {
  ACTIVITY_COLUMN_DEFS,
  type ActivityCellCtx,
} from "./activity-column-defs";
import { DateTime, useDateText } from "@/components/DateTime";

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

  // What is not the task's id, its node or its guest: those have fields of
  // their own. Only worked out for the one row that is open.
  const otherDetails = expanded
    ? Object.fromEntries(
        Object.entries(details).filter(
          ([k]) => !["upid", "node", "vmid"].includes(k),
        ),
      )
    : {};

  return (
    <>
      <tr
        className={cn(
          "cursor-pointer border-b border-border/70 transition-colors hover:bg-foreground/[0.04]",
          // The row's own words take its state: a running task blue, a
          // failed one red, everything finished plain.
          status === "running" && "text-info",
          isFailed && "text-red-600 dark:text-red-400",
          expanded && "border-transparent bg-foreground/[0.04]",
        )}
        onClick={onToggle}
        onDoubleClick={(e) => {
          e.stopPropagation();
          onFocus();
        }}
      >
        <DataTableCells row={row} layout={layout} ctx={{ expanded }} />
        <td aria-hidden="true" className="p-0" />
      </tr>
      {expanded && (
        <tr>
          {/* One more than the columns: the filler cell (see the table). */}
          <td colSpan={layout.columns.length + 1} className="!p-0">
            {/* Recessed: this is the row opened up, not another row. Held to
                the band's width and pinned to its left edge, so a table
                wider than a phone does not carry the details off-screen. */}
            <div className="sticky left-0 max-w-[100cqw] border-b bg-foreground/[0.04] px-[18px] pb-3.5 pt-2 text-[12.5px]">
              <dl className="grid grid-cols-[repeat(auto-fit,minmax(min(9.5rem,100%),1fr))] gap-x-6 gap-y-2.5 [overflow-wrap:anywhere]">
                <DetailField label="Action">{entry.action}</DetailField>
                <DetailField label="Resource">
                  {entry.resource_type} / {entry.resource_id}
                </DetailField>
                <DetailField label="Cluster">
                  {auditClusterLabel(entry, "—")}
                </DetailField>
                {details.node && (
                  <DetailField label="Node">{details.node}</DetailField>
                )}
                <DetailField label="User">{row.actorLabel || "—"}</DetailField>
                <DetailField label="Time">
                  <DateTime value={entry.created_at} />
                </DetailField>
                {isFailed && row.exitStatusText !== "" && (
                  <div className="col-span-full">
                    <dt className="text-xs text-muted-foreground">
                      Exit status
                    </dt>
                    <dd className="text-red-600 dark:text-red-400">
                      {row.exitStatusText}
                    </dd>
                  </div>
                )}
                {upid && (
                  <div className="col-span-full">
                    <dt className="text-xs text-muted-foreground">UPID</dt>
                    <dd className="break-all font-mono text-[11.5px]">
                      {upid}
                    </dd>
                  </div>
                )}
                {Object.keys(otherDetails).length > 0 && (
                  <div className="col-span-full">
                    <dt className="text-xs text-muted-foreground">
                      {t("details")}
                    </dt>
                    <dd className="break-all font-mono text-[11.5px]">
                      {JSON.stringify(otherDetails)}
                    </dd>
                  </div>
                )}
              </dl>

              {/* Task Log Output (for UPID-bearing entries) */}
              {hasUpid && (
                <TaskLogSection lines={logLines} isLoading={logLoading} />
              )}
            </div>
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
  const alarming = (errorCount ?? 0) > 0;
  const options: SegmentedOption<ActivityFilter>[] = [
    { value: "all", label: t("all") },
    {
      value: "errors",
      label: (
        <span
          className={alarming ? "text-red-600 dark:text-red-400" : undefined}
        >
          {errorCount === null
            ? t("activityErrors")
            : t("activityErrorCount", { count: errorCount })}
        </span>
      ),
    },
    { value: "other", label: t("others") },
  ];

  return (
    <span
      className="flex shrink-0"
      onClick={(e) => {
        // The whole title row toggles the panel; choosing a filter must not
        // also collapse it.
        e.stopPropagation();
      }}
    >
      <Segmented
        aria-label={t("filterActivity")}
        value={filter}
        options={options}
        onValueChange={onSelect}
        onReselect={onSelect}
      />
    </span>
  );
}

/**
 * The collapsed panel's one line: the task running now with its progress, or
 * failing that the last thing that happened. Kept to the row's left on
 * purpose: a minimised console sits over its right end.
 */
function ActivitySummaryLine({ summary }: { summary: ActivitySummary }) {
  const { t } = useTranslation("common");
  const { featured, latest } = summary;
  const when = useDateText(latest?.entry.created_at, { auto: "relative" });
  if (featured) {
    return (
      <span className="hidden min-w-0 items-center gap-2.5 text-[12.5px] sm:flex">
        <span className="truncate">{activityLabel(featured)}</span>
        <span className="shrink-0">
          <TaskProgressCell display="running" value={featured.progress} />
        </span>
      </span>
    );
  }
  if (!latest) return null;
  return (
    <span className="hidden min-w-0 truncate text-[12.5px] text-muted-foreground sm:inline">
      {t("activityLast", {
        label: activityLabel(latest),
        when,
      })}
    </span>
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
    () => decorateActivities(entries ?? [], taskStatuses),
    [entries, taskStatuses],
  );

  // Counted off the decorated rows, so each count says what its rows show, and
  // the error one is exactly what its filter selects.
  // Off the unfiltered rows: the strip answers "what is happening", whatever
  // filter the open list was left on.
  const summary = useMemo(() => summarizeActivity(rows), [rows]);
  const runningCount = summary.runningCount;
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
    <section
      aria-label={t("activity")}
      className="relative flex shrink-0 flex-col border-t bg-card"
    >
      {/* Invisible progress pollers for running tasks */}
      {runningPolls.map((poll) => (
        <ActiveTaskPoller
          key={poll.id}
          clusterId={poll.clusterId}
          upid={poll.upid}
          onStatus={handleTaskStatus}
        />
      ))}

      {/* Resize handle: only while the panel is open. It sits over the
          band's top hairline, which lights up under the pointer. */}
      {panelOpen && (
        <div
          className="absolute inset-x-0 -top-[3px] z-10 h-[5px] cursor-row-resize transition-colors hover:bg-primary/40 active:bg-primary/60"
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={handlePointerUp}
        />
      )}

      {/* Title row. The filter sits left of the summary on purpose: a
          minimised console covers the row's right end. On a phone it wraps
          rather than clip Reset columns; from sm up it is one line of a fixed
          height, so the collapsed summary truncates and the strip never
          changes height as activity comes and goes. */}
      <div
        className="flex min-h-9 cursor-pointer select-none flex-wrap items-center gap-x-3 gap-y-1 py-[3px] pl-[18px] pr-2 sm:h-9 sm:flex-nowrap"
        onClick={() => {
          setPanelOpen(!panelOpen);
        }}
      >
        <h2 className="text-[13px] font-semibold">{t("activity")}</h2>
        {runningCount > 0 && (
          <span className="text-[12.5px] font-medium tabular-nums text-info">
            {runningCount} {t("running").toLowerCase()}
          </span>
        )}
        {/* Always there: hidden at zero, it read as a feature gone missing. */}
        <ActivityFilterControl
          filter={filter}
          errorCount={errorCount}
          onSelect={selectFilter}
        />
        {/* Only while collapsed: the open list already shows these rows. */}
        {!panelOpen && <ActivitySummaryLine summary={summary} />}
        <div className="flex-1" />
        {panelOpen && (
          <span
            className="[&_button]:h-7 [&_button]:px-2 [&_button]:text-[12.5px] [&_button]:text-muted-foreground"
            onClick={(e) => {
              // The whole title row toggles the panel; resetting must not
              // also collapse it.
              e.stopPropagation();
            }}
          >
            <ResetColumnsButton layout={layout} />
          </span>
        )}
        <button
          type="button"
          aria-expanded={panelOpen}
          aria-label={panelOpen ? t("collapseActivity") : t("expandActivity")}
          title={panelOpen ? t("collapseActivity") : t("expandActivity")}
          onClick={(e) => {
            e.stopPropagation();
            setPanelOpen(!panelOpen);
          }}
          className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-muted-foreground outline-hidden transition-colors hover:bg-foreground/[0.07] hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        >
          {panelOpen ? (
            <ChevronDown aria-hidden="true" className="h-4 w-4" />
          ) : (
            <ChevronUp aria-hidden="true" className="h-4 w-4" />
          )}
        </button>
      </div>

      {/* Activity list */}
      {panelOpen && (
        <div
          className="@container overflow-auto border-t"
          style={{ height: panelHeight }}
        >
          {visibleRows.length === 0 && (
            <div className="flex h-full items-center justify-center text-[12.5px] text-muted-foreground">
              {rows.length === 0 ? t("noActivity") : t("noMatchingActivity")}
            </div>
          )}
          {visibleRows.length > 0 && (
            <table
              // Dense one-line rows; the headings stay put while the list
              // scrolls under them. The columns keep the widths they were
              // given, and an empty last one takes whatever the band has
              // left, so the hairlines and the hover run to its edge.
              className="min-w-full table-fixed text-[12.5px] [&_td]:py-[5px] [&_th]:sticky [&_th]:top-0 [&_th]:z-[1] [&_th]:h-7 [&_th]:bg-card [&_th]:shadow-[inset_0_-1px_0_hsl(var(--border))]"
              style={{ width: layout.totalWidth }}
            >
              <thead>
                <tr>
                  <DataTableHeadCells
                    layout={layout}
                    directionFor={directionFor}
                    onSort={toggle}
                  />
                  <th aria-hidden="true" className="p-0" />
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
    </section>
  );
}
