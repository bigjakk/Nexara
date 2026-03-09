import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  ChevronUp,
  ChevronDown,
  ChevronRight,
  Loader2,
  CheckCircle2,
  XCircle,
  Activity,
} from "lucide-react";
import { useTaskLogStore } from "@/stores/task-log-store";
import {
  useRecentActivity,
  type AuditLogEntry,
} from "@/features/audit/api/audit-queries";
import {
  useTaskStatus,
  useTaskLog,
} from "@/features/vms/api/vm-queries";

function formatRelativeTime(iso: string): string {
  const ago = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (ago < 60) return `${String(ago)}s ago`;
  if (ago < 3600) return `${String(Math.floor(ago / 60))}m ago`;
  if (ago < 86400) return `${String(Math.floor(ago / 3600))}h ago`;
  return `${String(Math.floor(ago / 86400))}d ago`;
}

function formatTimestamp(iso: string): string {
  return new Date(iso).toLocaleString();
}

function formatAction(action: string): string {
  return action
    .replace(/_/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

type Severity = "info" | "warning" | "error";

function deriveSeverity(action: string, details: string): Severity {
  if (details && details !== "{}" && details !== "null") {
    try {
      const d = JSON.parse(details) as Record<string, unknown>;
      if (typeof d["error"] === "string" && d["error"] !== "") return "error";
      if (d["status"] === "failed" || d["status"] === "error") return "error";
    } catch {
      // ignore
    }
  }
  const a = action.toLowerCase();
  if (a.includes("error") || a.includes("failed") || a.includes("fail")) return "error";
  if (
    a.includes("delete") ||
    a.includes("destroy") ||
    a.includes("disable") ||
    a.includes("revoke") ||
    a.includes("reset") ||
    a.includes("stop") ||
    a.includes("shutdown") ||
    a.includes("suspend") ||
    a.includes("cancel")
  )
    return "warning";
  return "info";
}

const SEVERITY_STYLES: Record<Severity, string> = {
  info: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  warning: "bg-yellow-500/15 text-yellow-600 dark:text-yellow-400",
  error: "bg-red-500/15 text-red-600 dark:text-red-400",
};

const SEVERITY_LABELS: Record<Severity, string> = {
  info: "info",
  warning: "warn",
  error: "error",
};

interface ParsedDetails {
  upid?: string;
  node?: string;
  vmid?: number;
  [key: string]: unknown;
}

function parseDetails(detailsStr: string): ParsedDetails {
  try {
    const parsed: unknown = JSON.parse(detailsStr);
    if (parsed && typeof parsed === "object") {
      return parsed as ParsedDetails;
    }
  } catch {
    // ignore
  }
  return {};
}

function getClusterIdFromEntry(entry: AuditLogEntry): string {
  return entry.cluster_id ?? "";
}

function isRecentEntry(entry: AuditLogEntry): boolean {
  const tenMinAgo = Date.now() - 10 * 60 * 1000;
  return new Date(entry.created_at).getTime() > tenMinAgo;
}

/**
 * Polls Proxmox for a UPID's live status (running/stopped).
 * Only active for recent entries with UPIDs.
 */
function ActiveTaskPoller({
  entry,
  onStatus,
}: {
  entry: AuditLogEntry;
  onStatus: (upid: string, status: string, exitStatus: string) => void;
}) {
  const details = parseDetails(entry.details);
  const clusterId = getClusterIdFromEntry(entry);
  const upid = details.upid ?? null;

  const { data: task } = useTaskStatus(
    clusterId,
    upid && isRecentEntry(entry) ? upid : null,
  );

  const prevRef = useRef<string | null>(null);
  useEffect(() => {
    if (task && upid) {
      const key = `${task.status}:${task.exit_status}`;
      if (prevRef.current !== key) {
        prevRef.current = key;
        onStatus(upid, task.status, task.exit_status);
      }
    }
  }, [task, upid, onStatus]);

  return null;
}

function ActivityRow({
  entry,
  expanded,
  onToggle,
  onFocus,
  taskStatus,
}: {
  entry: AuditLogEntry;
  expanded: boolean;
  onToggle: () => void;
  onFocus: () => void;
  taskStatus: { status: string; exitStatus: string } | undefined;
}) {
  const { t } = useTranslation("common");
  const details = parseDetails(entry.details);
  const hasUpid = !!details.upid;
  const clusterId = getClusterIdFromEntry(entry);

  const isRunning = hasUpid && taskStatus?.status === "running";
  const isStopped = hasUpid && taskStatus?.status === "stopped";
  const isOk =
    isStopped &&
    (taskStatus.exitStatus === "" ||
      taskStatus.exitStatus === "OK" ||
      taskStatus.exitStatus.startsWith("WARNINGS"));
  const isFailed = isStopped && !isOk;

  // Derive severity — task failure overrides to error
  let severity = deriveSeverity(entry.action, entry.details);
  if (isFailed) severity = "error";

  const resourceLabel =
    entry.resource_name && entry.resource_vmid
      ? `${entry.resource_name} (${String(entry.resource_vmid)})`
      : entry.resource_name || entry.resource_id;

  const { data: logLines, isLoading: logLoading } = useTaskLog(
    clusterId,
    details.upid ?? null,
    expanded && hasUpid,
  );

  return (
    <>
      <tr
        className="cursor-pointer border-b hover:bg-muted/20"
        onClick={onToggle}
        onDoubleClick={(e) => {
          e.stopPropagation();
          onFocus();
        }}
      >
        <td className="px-2 py-1">
          <div className="flex items-center gap-1">
            <ChevronRight
              className={`h-3 w-3 text-muted-foreground transition-transform ${expanded ? "rotate-90" : ""}`}
            />
            {isRunning && (
              <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500" />
            )}
            {isOk && (
              <CheckCircle2 className="h-3.5 w-3.5 text-green-500" />
            )}
            {isFailed && (
              <XCircle className="h-3.5 w-3.5 text-red-500" />
            )}
            {!hasUpid && (
              <Activity className="h-3.5 w-3.5 text-muted-foreground" />
            )}
            {hasUpid && !taskStatus && (
              <Activity className="h-3.5 w-3.5 text-muted-foreground" />
            )}
          </div>
        </td>
        <td className="px-2 py-1">
          <span className={`inline-block rounded-full px-1.5 py-0.5 text-[10px] font-medium leading-none ${SEVERITY_STYLES[severity]}`}>
            {SEVERITY_LABELS[severity]}
          </span>
        </td>
        <td className="px-2 py-1">
          <div className="flex items-center gap-2">
            <span>{formatAction(entry.action)}</span>
            {resourceLabel && (
              <span className="text-muted-foreground">
                — {resourceLabel}
              </span>
            )}
            {isRunning && (
              <span className="text-xs text-blue-500">{t("running").toLowerCase()}</span>
            )}
          </div>
        </td>
        <td className="px-2 py-1 text-muted-foreground">
          {entry.cluster_name || "—"}
        </td>
        <td className="px-2 py-1 text-right text-muted-foreground">
          {formatRelativeTime(entry.created_at)}
        </td>
      </tr>
      {expanded && (
        <tr className="border-b bg-muted/10">
          <td colSpan={5} className="px-4 py-2">
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">Action</span>
              <span>{entry.action}</span>
              <span className="text-muted-foreground">Resource</span>
              <span>
                {entry.resource_type} / {entry.resource_id}
              </span>
              <span className="text-muted-foreground">User</span>
              <span>
                {entry.user_display_name || entry.user_email}
              </span>
              <span className="text-muted-foreground">Time</span>
              <span>{formatTimestamp(entry.created_at)}</span>
              {isFailed && (
                <>
                  <span className="text-muted-foreground">Exit Status</span>
                  <span className="text-red-500">
                    {taskStatus.exitStatus}
                  </span>
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
    Record<string, { status: string; exitStatus: string }>
  >({});

  const handleTaskStatus = useCallback(
    (upid: string, status: string, exitStatus: string) => {
      setTaskStatuses((prev) => {
        const existing = prev[upid];
        if (
          existing?.status === status &&
          existing.exitStatus === exitStatus
        ) {
          return prev;
        }
        return { ...prev, [upid]: { status, exitStatus } };
      });
    },
    [],
  );

  // Count running/failed from polled statuses
  const runningCount = Object.values(taskStatuses).filter(
    (s) => s.status === "running",
  ).length;
  const failedCount = Object.values(taskStatuses).filter(
    (s) =>
      s.status === "stopped" &&
      s.exitStatus !== "" &&
      s.exitStatus !== "OK" &&
      !s.exitStatus.startsWith("WARNINGS"),
  ).length;

  // Recent entries that have UPIDs and are within last 10 minutes
  const recentWithUpids =
    entries?.filter((e) => {
      const d = parseDetails(e.details);
      return d.upid && isRecentEntry(e);
    }) ?? [];

  const dragRef = useRef<{ startY: number; startHeight: number } | null>(
    null,
  );

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
    <div className="flex flex-col border-t bg-background">
      {/* Invisible pollers for recent UPID-bearing entries */}
      {recentWithUpids.map((e) => (
        <ActiveTaskPoller
          key={e.id}
          entry={e}
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
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b bg-muted/30 text-left">
                  <th className="w-12 px-2 py-1" />
                  <th className="w-14 px-2 py-1 font-medium">Level</th>
                  <th className="px-2 py-1 font-medium">Action</th>
                  <th className="px-2 py-1 font-medium">Cluster</th>
                  <th className="w-24 px-2 py-1 text-right font-medium">
                    Time
                  </th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry) => {
                  const details = parseDetails(entry.details);
                  const upid = details.upid;
                  return (
                    <ActivityRow
                      key={entry.id}
                      entry={entry}
                      expanded={expandedId === entry.id}
                      onToggle={() => {
                        setExpandedId(
                          expandedId === entry.id ? null : entry.id,
                        );
                      }}
                      onFocus={() => {
                        if (upid && entry.cluster_id) {
                          setFocusedTask({
                            clusterId: entry.cluster_id,
                            upid,
                            description: `${formatAction(entry.action)} — ${entry.resource_name || entry.resource_id}`,
                          });
                        }
                      }}
                      taskStatus={
                        upid ? taskStatuses[upid] : undefined
                      }
                    />
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}
