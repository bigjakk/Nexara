import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
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
import { useTaskLog, useTaskStatus } from "@/features/tasks/api/tasks-queries";
import { HISTORY_PAGE_SIZE, useHistory } from "../api/history-queries";
import {
  SOURCE_OPTIONS,
  WINDOW_OPTIONS,
  historyActor,
  historyDisplayStatus,
  historyEventLabel,
  historyExclusions,
  historyGuestVmid,
  historyNamespace,
  historyNote,
  historySourceLabel,
  historySummary,
  upidWorkerType,
  type HistoryCellCtx,
  type HistoryColumnKey,
} from "../lib/history";
import {
  HISTORY_COLUMNS,
  HISTORY_COLUMNS_WITH_GUEST,
} from "../lib/history-column-defs";
import type {
  HistoryEntry,
  HistoryGuestLink,
  HistoryKind,
  HistorySource,
  HistoryTarget,
  HistoryWindow,
} from "../types/history";

type HistoryLayout = ColumnLayout<
  HistoryEntry,
  HistoryColumnKey,
  HistoryCellCtx
>;

/** The timeline is newest first and sorts no other way. */
const noSort = () => null;
const ignoreSort = () => undefined;

function HistoryRow({
  kind,
  t,
  entry,
  clusterId,
  layout,
  guestLink,
  expanded,
  onToggle,
}: {
  kind: HistoryKind;
  t: TFunction;
  entry: HistoryEntry;
  clusterId: string;
  layout: HistoryLayout;
  guestLink: ((vmid: number) => HistoryGuestLink | undefined) | undefined;
  expanded: boolean;
  onToggle: () => void;
}) {
  const { t: shared } = useTranslation("history");
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
  const label = historyEventLabel(kind, entry, t);
  const summary = historySummary(kind, entry, t);
  const actor = historyActor(entry);
  const note = expanded ? historyNote(kind, entry, t) : undefined;
  const guestVmid = historyGuestVmid(entry);
  const guest =
    guestVmid > 0 ? { vmid: guestVmid, link: guestLink?.(guestVmid) } : null;

  // A backup entry's task is the whole job: read from where the guest's part
  // begins, and show it to where it ends.
  const isBackup = entry.kind === "backup";
  const {
    data: jobLogLines,
    isLoading: logLoading,
    isError: logError,
  } = useTaskLog(
    clusterId,
    hasTask ? entry.upid : null,
    expanded && hasTask,
    isBackup ? Math.max(0, (entry.log_start ?? 1) - 1) : 0,
  );
  const logEnd = entry.log_end ?? 0;
  const logLines =
    isBackup && logEnd > 0
      ? jobLogLines?.filter((line) => line.n <= logEnd)
      : jobLogLines;
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
          ctx={{
            label,
            summary,
            actor,
            display,
            progress,
            expanded,
            icon: kind.icons[entry.category],
            guest,
          }}
        />
      </tr>
      {expanded && (
        <tr className="border-b bg-muted/10">
          <td colSpan={layout.columns.length} className="px-4 py-3">
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">
                {shared("row.event")}
              </span>
              <span className="font-medium">{label}</span>

              {summary !== "" && (
                <>
                  <span className="text-muted-foreground">
                    {shared("row.summary")}
                  </span>
                  <span className="break-words">{summary}</span>
                </>
              )}

              <span className="text-muted-foreground">
                {shared(
                  hasTask
                    ? "row.started"
                    : entry.kind === "status" ||
                        entry.kind === "observed" ||
                        entry.kind === "move"
                      ? "row.seen"
                      : "row.when",
                )}
              </span>
              <span>{formatDateTime(entry.occurred_at)}</span>

              {entry.last_seen_at && (
                <>
                  <span className="text-muted-foreground">
                    {entry.kind === "observed"
                      ? shared("observed.lastSeen")
                      : entry.kind === "run" || (entry.run_count ?? 0) > 0
                        ? shared("row.first")
                        : entry.kind === "move"
                          ? shared("row.lastSeenBeforeMove")
                          : shared("row.lastSeen", {
                              status: entry.from_status,
                            })}
                  </span>
                  <span>{formatDateTime(entry.last_seen_at)}</span>
                </>
              )}

              {entry.finished_at && (
                <>
                  <span className="text-muted-foreground">
                    {shared("row.finished")}
                  </span>
                  <span>{formatDateTime(entry.finished_at)}</span>
                </>
              )}

              <span className="text-muted-foreground">
                {shared("row.source")}
              </span>
              <span>
                {historySourceLabel(entry, shared)}
                {actor !== "" && ` · ${actor}`}
              </span>

              {entry.node !== "" && (
                <>
                  <span className="text-muted-foreground">
                    {shared("row.node")}
                  </span>
                  <span>{entry.node}</span>
                </>
              )}

              {hasTask && (
                <>
                  <span className="text-muted-foreground">
                    {shared("row.taskType")}
                  </span>
                  <span className="font-mono">
                    {upidWorkerType(entry.upid) || entry.task_type || "—"}
                  </span>
                </>
              )}

              {display === "failed" && exitText !== "" && (
                <>
                  <span className="text-muted-foreground">
                    {shared("row.exitStatus")}
                  </span>
                  <span className="break-all text-red-500">{exitText}</span>
                </>
              )}

              {hasTask && (
                <>
                  <span className="text-muted-foreground">
                    {shared("row.upid")}
                  </span>
                  <span className="break-all font-mono text-[10px]">
                    {entry.upid}
                  </span>
                </>
              )}
            </div>

            {note !== undefined && (
              <p className="mt-2 text-xs text-muted-foreground">{note}</p>
            )}

            {details && (
              <div className="mt-2 border-t pt-2">
                <span className="text-xs font-medium text-muted-foreground">
                  {shared("row.details")}
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
                    {shared("row.liveView")}
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
 * An object's History: everything that happened to it, newest first — its
 * tasks in Nexara and in Proxmox with how each ended, its changes with no
 * task, and, for a guest, the status changes no task explains. Rows expand to
 * the task's Proxmox log. What the entries are, and what they are called, is
 * the kind's (HistoryKind); which object, and where its route is, the
 * target's.
 */
export function HistoryTimeline({
  kind,
  target,
  enabled = true,
  rowsAbove,
  guestLink,
}: {
  kind: HistoryKind;
  target: HistoryTarget;
  enabled?: boolean;
  /** Live rows above the first page — a running VM's block jobs — spanning
   *  the table's colSpan columns. A failed read keeps them. */
  rowsAbove?: ((colSpan: number) => ReactNode) | undefined;
  /** A History of many guests (a folder's): adds a Guest column, each guest
   *  linked where this returns its page. */
  guestLink?: ((vmid: number) => HistoryGuestLink | undefined) | undefined;
}) {
  const { t } = useTranslation(historyNamespace(kind));
  const { t: shared } = useTranslation("history");
  const [page, setPage] = useState(0);
  const [category, setCategory] = useState("");
  const [source, setSource] = useState<HistorySource | "">("");
  const [timeWindow, setTimeWindow] = useState<HistoryWindow>(
    kind.defaultWindow ?? "",
  );
  const [showRoutine, setShowRoutine] = useState(false);
  const [includeGuests, setIncludeGuests] = useState(false);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const layout = useColumnLayout(
    kind.layoutKey,
    guestLink ? HISTORY_COLUMNS_WITH_GUEST : HISTORY_COLUMNS,
  );

  const excludeCategories = historyExclusions(kind, showRoutine, category);
  const { data, isLoading, error } = useHistory({
    target,
    page,
    category,
    excludeCategories,
    source,
    timeWindow,
    // The server reads a kind's default window when none is named.
    allTime: timeWindow === "" && kind.defaultWindow !== undefined,
    includeGuests: kind.guestToggle === true && includeGuests,
    enabled,
  });
  const items = data?.items ?? [];
  const total = data?.total ?? 0;
  // A capped read's total is one past the cap: more than it, of which the
  // cap's worth can be paged. A narrowed read has the larger cap.
  const narrowed =
    category !== "" || source !== "" || excludeCategories.length > 0;
  const cap = narrowed ? kind.filteredRowCap : kind.rowCap;
  const capped = cap !== undefined && total === cap + 1;
  const reachable = capped ? cap : total;
  const totalPages = Math.max(1, Math.ceil(reachable / HISTORY_PAGE_SIZE));

  // Snap back when a refetch shrinks the result below the current page, but
  // not while the fetch is failing — see TaskHistoryTable for why.
  useEffect(() => {
    if (!error && page > totalPages - 1) setPage(totalPages - 1);
  }, [error, page, totalPages]);

  const categoryOptions = [
    { value: "", label: shared("filters.allTypes") },
    ...kind.categories.map((c) => ({
      value: c,
      label: t(`categories.${c}`),
    })),
  ];
  const categoryNote =
    category === "" ? "" : t(`categoryNotes.${category}`, { defaultValue: "" });

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

  function filterCheckbox(
    label: string,
    checked: boolean,
    onChange: (v: boolean) => void,
  ) {
    return (
      <label className="flex items-center gap-1.5 text-sm text-muted-foreground">
        <input
          type="checkbox"
          checked={checked}
          onChange={(e) => {
            onChange(e.target.checked);
            setPage(0);
          }}
        />
        {label}
      </label>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        {filterSelect(
          shared("filters.type"),
          category,
          categoryOptions,
          setCategory,
        )}
        {filterSelect(
          shared("filters.source"),
          source,
          SOURCE_OPTIONS.map((value) => ({
            value,
            label: shared(`sourceOptions.${value || "all"}`),
          })),
          setSource,
        )}
        {filterSelect(
          shared("filters.time"),
          timeWindow,
          WINDOW_OPTIONS.map((value) => ({
            value,
            label: shared(`windows.${value || "all"}`),
          })),
          setTimeWindow,
        )}
        {(kind.routine ?? []).length > 0 &&
          filterCheckbox(
            shared("filters.showRoutine"),
            showRoutine,
            setShowRoutine,
          )}
        {kind.guestToggle === true &&
          filterCheckbox(
            shared("filters.includeGuests"),
            includeGuests,
            setIncludeGuests,
          )}
        <span className="flex-1" />
        <ResetColumnsButton layout={layout} />
      </div>

      {data?.searched_from !== undefined && cap !== undefined && (
        <p className="text-xs text-muted-foreground">
          {shared("searchedFrom", {
            when: formatDateTime(data.searched_from),
            cap: cap.toLocaleString(),
          })}
        </p>
      )}

      {categoryNote !== "" && (
        <p className="text-xs text-muted-foreground">{categoryNote}</p>
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
                {page === 0 && rowsAbove?.(layout.columns.length)}
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
                      kind={kind}
                      t={t}
                      entry={entry}
                      clusterId={entry.cluster_id ?? target.clusterId ?? ""}
                      layout={layout}
                      guestLink={guestLink}
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
                      {category || source
                        ? shared("noMatches")
                        : timeWindow
                          ? shared("emptyWindow")
                          : t("empty", { defaultValue: shared("empty") })}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-sm text-muted-foreground">
              {error
                ? ""
                : capped
                  ? shared("entriesMore", {
                      count: cap,
                      cap: cap.toLocaleString(),
                    })
                  : shared("entries", { count: total })}
            </p>
            {totalPages > 1 && (
              <div className="flex items-center gap-2">
                <Button
                  aria-label={shared("previousPage")}
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
                  {shared("page", { page: page + 1, pages: totalPages })}
                </span>
                <Button
                  aria-label={shared("nextPage")}
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
