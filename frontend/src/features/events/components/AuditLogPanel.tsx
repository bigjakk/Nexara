import { useState, useCallback, useMemo } from "react";
import { ChevronLeft, ChevronRight, ChevronDown, Download } from "lucide-react";
import { DetailField } from "@/components/DetailField";
import { DefGrid, Panel } from "@/components/page/Panel";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { apiFetch } from "@/lib/api-client";
import type { ApiPath } from "@/lib/api-path";
import {
  deriveActor,
  deriveSeverity,
  formatAction,
  userLabel,
  type Severity,
} from "@/components/layout/activity-columns";
import { parseDetails } from "@/components/layout/task-status";
import { haRequester, linkedTaskRole } from "@/components/layout/linked-task";
import { LinkedTaskBadge } from "@/components/LinkedTaskBadge";
import { usePermissions } from "@/hooks/usePermissions";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useEvents,
  useAuditActions,
  useAuditUsers,
  buildExportUrl,
  type AuditLogEntry,
} from "../api/events-queries";
import { SyslogConfigCard } from "./SyslogConfigCard";
import { detailPairs } from "../lib/audit-details";
import { resourceTypeLabel, resourceTypes } from "../lib/resource-types";
import { auditClusterLabel } from "@/features/audit/lib/audit-cluster";

const PAGE_SIZE = 25;

/** A filter in the row above the table: compact, and named by its aria-label. */
const filterClass =
  "h-8 min-w-0 max-w-[11.5rem] rounded-md border border-input bg-transparent px-2 text-[13px] transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

/** The severity dropdown's value: a severity, or no filter at all. */
type SeverityFilter = Severity | "all";

/** This table's severity of `entry`, from the same rule the Activity panel uses. */
function entrySeverity(entry: AuditLogEntry): Severity {
  return deriveSeverity(entry.action, parseDetails(entry.details));
}

const SEVERITY_WORDS: Record<Severity, string> = {
  info: "Info",
  warning: "Warning",
  error: "Error",
};

/** A dot and a word. Info is nearly every row, so it stays quiet. */
function SeverityMark({ severity }: { severity: Severity }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-[7px] whitespace-nowrap",
        severity === "info" && "text-muted-foreground",
        severity === "warning" && "text-amber-700 dark:text-amber-400",
        severity === "error" && "text-red-700 dark:text-red-400",
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "h-[7px] w-[7px] shrink-0 rounded-full",
          severity === "info" && "bg-muted-foreground/40",
          severity === "warning" && "bg-amber-500",
          severity === "error" && "bg-red-500",
        )}
      />
      {SEVERITY_WORDS[severity]}
    </span>
  );
}

function formatDetailsSummary(entry: AuditLogEntry): string | null {
  if (!entry.details || entry.details === "{}" || entry.details === "null") {
    return null;
  }
  try {
    const d = JSON.parse(entry.details) as Record<string, unknown>;
    const parts: string[] = [];
    if (typeof d["vm_type"] === "string") parts.push(d["vm_type"]);
    if (
      typeof d["source_node"] === "string" &&
      typeof d["target_node"] === "string"
    ) {
      parts.push(`${d["source_node"]} → ${d["target_node"]}`);
    }
    if (typeof d["migration_type"] === "string")
      parts.push(d["migration_type"]);
    if (d["online"] === true) parts.push("live");
    if (typeof d["error"] === "string") parts.push(`Error: ${d["error"]}`);
    if (typeof d["status"] === "string" && d["status"] !== "completed")
      parts.push(d["status"]);
    return parts.length > 0 ? parts.join(" · ") : null;
  } catch {
    return null;
  }
}

/** Who did it: the Nexara account, or the Proxmox account for an ingested task. */
function SourceCell({ entry }: { entry: AuditLogEntry }) {
  if (entry.source === "proxmox") {
    const details = parseDetails(entry.details);
    const proxmoxUser =
      typeof details["proxmox_user"] === "string"
        ? details["proxmox_user"]
        : "";
    return (
      <span>
        {proxmoxUser !== "" && <>{proxmoxUser} </>}
        <span className="text-[12.5px] text-muted-foreground">
          {proxmoxUser !== "" ? "in Proxmox" : "Proxmox"}
        </span>
      </span>
    );
  }
  // userLabel, not deriveActor: the Proxmox case is the branch above, so what
  // is left is the Nexara account, and this must name it the same way the
  // Activity drawer's User column does — a row cannot read "DRS Scheduler" in
  // one table and "System" in the other when the drawer sits over this page.
  return (
    <span>
      {userLabel(entry.user_id, entry.user_display_name, entry.user_email) ||
        "—"}
    </span>
  );
}

function ResourceFallback({ entry }: { entry: AuditLogEntry }) {
  try {
    const d = JSON.parse(entry.details) as Record<string, unknown>;
    // Proxmox-sourced entries store name + VMID in details
    const resName =
      typeof d["resource_name"] === "string" ? d["resource_name"] : null;
    const resId =
      typeof d["resource_id"] === "string" ? d["resource_id"] : null;
    if (resName) {
      return (
        <span>
          {resName}
          {resId && <span className="text-muted-foreground"> ({resId})</span>}
        </span>
      );
    }
    // Nexara-initiated entries with VMID in details
    const vmid = typeof d["vmid"] === "number" ? d["vmid"] : null;
    const vmType = typeof d["vm_type"] === "string" ? d["vm_type"] : null;
    if (vmid != null) {
      return (
        <span>
          {vmType ?? "VM"} {String(vmid)}
        </span>
      );
    }
    // Proxmox task with VMID but no name resolved
    if (resId) {
      return <span className="text-muted-foreground">VMID {resId}</span>;
    }
  } catch {
    // ignore
  }
  return (
    <span className="font-mono text-[12.5px] text-muted-foreground">
      {entry.resource_id.slice(0, 8)}
    </span>
  );
}

/** How many columns the table has, for a row that spans them all. */
const COLUMN_COUNT = 7;

function EventRow({
  entry,
  requester,
  expanded,
  onToggle,
}: {
  entry: AuditLogEntry;
  /** Who asked the HA manager for this entry's task, when it is one. */
  requester: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const summary = formatDetailsSummary(entry);
  const details = expanded ? detailPairs(entry.details) : null;
  const severity = entrySeverity(entry);

  return (
    <>
      <TableRow className="cursor-pointer" onClick={onToggle}>
        <TableCell className="whitespace-nowrap text-muted-foreground">
          <div className="flex items-center gap-1.5">
            <ChevronDown
              className={cn(
                "h-3.5 w-3.5 shrink-0 transition-transform",
                !expanded && "-rotate-90",
              )}
            />
            {new Date(entry.created_at).toLocaleString()}
          </div>
        </TableCell>
        <TableCell>
          <SeverityMark severity={severity} />
        </TableCell>
        <TableCell className="whitespace-nowrap font-medium">
          <span className="inline-flex items-center gap-2">
            <LinkedTaskBadge
              role={linkedTaskRole(parseDetails(entry.details))}
              requester={requester}
            />
            {formatAction(entry.action)}
          </span>
        </TableCell>
        <TableCell>
          <span className="mr-2 text-[12.5px] text-muted-foreground">
            {resourceTypeLabel(entry.resource_type)}
          </span>
          {entry.resource_name ? (
            <span>
              {entry.resource_name}
              {entry.resource_vmid > 0 && (
                <span className="text-muted-foreground">
                  {" "}
                  ({String(entry.resource_vmid)})
                </span>
              )}
            </span>
          ) : (
            <ResourceFallback entry={entry} />
          )}
        </TableCell>
        <TableCell className="hidden text-muted-foreground md:table-cell">
          {auditClusterLabel(entry, "System")}
        </TableCell>
        <TableCell className="hidden text-[12.5px] text-muted-foreground lg:table-cell">
          {summary ?? ""}
        </TableCell>
        <TableCell>
          <SourceCell entry={entry} />
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow className="hover:bg-transparent">
          <TableCell
            colSpan={COLUMN_COUNT}
            className="bg-foreground/[0.03] py-4"
          >
            <DefGrid className="grid-cols-[repeat(auto-fit,minmax(min(12rem,100%),1fr))]">
              <DetailField label="Timestamp">
                {new Date(entry.created_at).toLocaleString()}
              </DetailField>
              <DetailField label="Severity">
                <SeverityMark severity={severity} />
              </DetailField>
              <DetailField label="Action">
                {formatAction(entry.action)}
              </DetailField>
              <DetailField label="Cluster">
                {auditClusterLabel(entry, "System")}
              </DetailField>
              <DetailField label="Resource type">
                {resourceTypeLabel(entry.resource_type)}
              </DetailField>
              {entry.resource_name && (
                <DetailField label="Resource name">
                  {entry.resource_name}
                  {entry.resource_vmid > 0 &&
                    ` (VMID ${String(entry.resource_vmid)})`}
                </DetailField>
              )}
              <DetailField label="Source">
                <SourceCell entry={entry} />
              </DetailField>
              <DetailField label="User">
                {/* The Nexara account that wrote the entry — the Source field
                    already carries the PVE account for an ingested task, so
                    naming that one here would put "root@pam" beside its own
                    system@ email. */}
                {userLabel(
                  entry.user_id,
                  entry.user_display_name,
                  entry.user_email,
                ) || "—"}
                {entry.user_display_name && entry.user_email && (
                  <span className="ml-1 text-muted-foreground">
                    ({entry.user_email})
                  </span>
                )}
              </DetailField>
              <DetailField label="Resource ID" variant="mono">
                <span className="break-all text-xs">{entry.resource_id}</span>
              </DetailField>
              <DetailField label="User ID" variant="mono">
                <span className="break-all text-xs">{entry.user_id}</span>
              </DetailField>
            </DefGrid>

            {details && details.length > 0 && (
              <div className="mt-4 border-t pt-3">
                <h3 className="text-[12.5px] font-medium text-muted-foreground">
                  Details
                </h3>
                <div className="mt-1.5 grid grid-cols-[auto_1fr] gap-x-5 gap-y-1 text-[12.5px]">
                  {details.map(([label, value]) => (
                    <div key={label} className="contents">
                      <span className="text-muted-foreground">{label}</span>
                      <span
                        className={cn(
                          "break-all",
                          label === "Error" && "text-red-700 dark:text-red-400",
                        )}
                      >
                        {value}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </TableCell>
        </TableRow>
      )}
    </>
  );
}

async function triggerDownload(url: ApiPath, filename: string) {
  const res = await apiFetch(url, { credentials: "same-origin" });
  const blob = await res.blob();
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = filename;
  link.click();
  URL.revokeObjectURL(link.href);
}

export function AuditLogPanel() {
  const [page, setPage] = useState(0);
  const [clusterFilter, setClusterFilter] = useState("");
  const [resourceFilter, setResourceFilter] = useState("");
  const [userFilter, setUserFilter] = useState("");
  const [actionFilter, setActionFilter] = useState("");
  const [sourceFilter, setSourceFilter] = useState("");
  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>("all");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const { canManage } = usePermissions();
  const { data: clusters } = useClusters();
  const { data: actions } = useAuditActions();
  const { data: users } = useAuditUsers();

  // Re-sorted on the label, not left in the server's order. ListDistinctAuditUsers
  // orders by users.display_name, so the seeded system actor arrives filed under
  // "DRS Scheduler" while the option — and every row it filters — reads "System",
  // landing it between Dave and Erin. Same rule as the drawer's User column: a
  // list has to be ordered by what it shows.
  const userOptions = useMemo(
    () =>
      (users ?? [])
        .map((u) => ({
          id: u.id,
          label: userLabel(u.id, u.display_name, u.email),
        }))
        .sort((a, b) => a.label.localeCompare(b.label)),
    [users],
  );

  const startTime = startDate ? new Date(startDate).toISOString() : undefined;
  const endTime = endDate
    ? new Date(endDate + "T23:59:59").toISOString()
    : undefined;

  const { data, isLoading, error } = useEvents({
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    clusterId: clusterFilter || undefined,
    resourceType: resourceFilter || undefined,
    userId: userFilter || undefined,
    action: actionFilter || undefined,
    source: sourceFilter || undefined,
    startTime,
    endTime,
  });

  // Client-side severity filter (derived from action/details, not stored in DB)
  const filteredItems =
    severityFilter === "all"
      ? data?.items
      : data?.items.filter((e) => entrySeverity(e) === severityFilter);

  // Who made each task on the page: an HA manager's task names its request's.
  // From the whole page, not what the severity filter leaves of it.
  const actorByUpid = useMemo(() => {
    const actors = new Map<string, string>();
    for (const entry of data?.items ?? []) {
      const details = parseDetails(entry.details);
      const actor = deriveActor(entry, details);
      if (details.upid && actor) actors.set(details.upid, actor);
    }
    return actors;
  }, [data?.items]);

  const totalPages = data ? Math.ceil(data.total / PAGE_SIZE) : 0;

  const handleExport = useCallback(
    (format: "json" | "csv" | "syslog") => {
      const filters = {
        clusterId: clusterFilter || undefined,
        resourceType: resourceFilter || undefined,
        userId: userFilter || undefined,
        action: actionFilter || undefined,
        startTime,
        endTime,
      };
      const url = buildExportUrl(format, filters);
      const ext = format === "syslog" ? "log" : format;
      void triggerDownload(url, `audit-log.${ext}`);
    },
    [
      clusterFilter,
      resourceFilter,
      userFilter,
      actionFilter,
      startTime,
      endTime,
    ],
  );

  const resetFilters = useCallback(() => {
    setClusterFilter("");
    setResourceFilter("");
    setUserFilter("");
    setActionFilter("");
    setSourceFilter("");
    setSeverityFilter("all");
    setStartDate("");
    setEndDate("");
    setPage(0);
  }, []);

  // The filters the server applies, which narrow the total it reports. The
  // severity filter is applied here, to the page already fetched, and leaves
  // that total alone.
  const hasServerFilters =
    clusterFilter !== "" ||
    resourceFilter !== "" ||
    userFilter !== "" ||
    actionFilter !== "" ||
    sourceFilter !== "" ||
    startDate !== "" ||
    endDate !== "";
  const hasFilters = hasServerFilters || severityFilter !== "all";

  return (
    <div className="space-y-4">
      <Panel
        flush
        title="Audit log"
        aside={
          data
            ? `${data.total.toLocaleString()} ${data.total === 1 ? "event" : "events"}${hasServerFilters ? " match" : ""}`
            : undefined
        }
        actions={
          <>
            <Segmented
              aria-label="Severity"
              value={severityFilter}
              onValueChange={(value) => {
                setSeverityFilter(value);
                setPage(0);
              }}
              options={[
                { value: "all", label: "All" },
                { value: "info", label: "Info" },
                { value: "warning", label: "Warning" },
                { value: "error", label: "Error" },
              ]}
            />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="sm" className="gap-1.5">
                  <Download className="h-3.5 w-3.5" />
                  Export
                  <ChevronDown className="h-3.5 w-3.5 opacity-60" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  onSelect={() => {
                    handleExport("csv");
                  }}
                >
                  CSV
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => {
                    handleExport("json");
                  }}
                >
                  JSON
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => {
                    handleExport("syslog");
                  }}
                >
                  Syslog (RFC 5424)
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </>
        }
      >
        {/* Filters: each control is named by its own first option. */}
        <div className="flex flex-wrap items-center gap-2 px-[18px] pb-3">
          <select
            aria-label="Cluster"
            className={filterClass}
            value={clusterFilter}
            onChange={(e) => {
              setClusterFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value="">All clusters</option>
            {clusters?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>

          <select
            aria-label="Resource type"
            className={filterClass}
            value={resourceFilter}
            onChange={(e) => {
              setResourceFilter(e.target.value);
              setPage(0);
            }}
          >
            {resourceTypes.map((rt) => (
              <option key={rt.value || "all"} value={rt.value}>
                {rt.value === "" ? "All types" : rt.label}
              </option>
            ))}
          </select>

          <select
            aria-label="User"
            className={filterClass}
            value={userFilter}
            onChange={(e) => {
              setUserFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value="">All users</option>
            {userOptions.map((u) => (
              <option key={u.id} value={u.id}>
                {u.label}
              </option>
            ))}
          </select>

          <select
            aria-label="Action"
            className={filterClass}
            value={actionFilter}
            onChange={(e) => {
              setActionFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value="">All actions</option>
            {actions?.map((a) => (
              <option key={a} value={a}>
                {formatAction(a)}
              </option>
            ))}
          </select>

          <select
            aria-label="Source"
            className={filterClass}
            value={sourceFilter}
            onChange={(e) => {
              setSourceFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value="">All sources</option>
            <option value="nexara">Nexara</option>
            <option value="proxmox">Proxmox</option>
          </select>

          <label className="flex items-center gap-1.5 text-[13px] text-muted-foreground">
            From
            <input
              type="date"
              className={cn(filterClass, "text-foreground")}
              value={startDate}
              onChange={(e) => {
                setStartDate(e.target.value);
                setPage(0);
              }}
            />
          </label>
          <label className="flex items-center gap-1.5 text-[13px] text-muted-foreground">
            to
            <input
              type="date"
              className={cn(filterClass, "text-foreground")}
              value={endDate}
              onChange={(e) => {
                setEndDate(e.target.value);
                setPage(0);
              }}
            />
          </label>

          {hasFilters && (
            <Button
              variant="ghost"
              size="sm"
              onClick={resetFilters}
              className="text-muted-foreground"
            >
              Clear filters
            </Button>
          )}
        </div>

        {isLoading ? (
          <div className="space-y-2 px-[18px] pb-1">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : error ? (
          <p className="px-[18px] pb-1 text-sm text-destructive">
            {error instanceof Error ? error.message : "Failed to load events"}
          </p>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Time</TableHead>
                  <TableHead>Severity</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead>Resource</TableHead>
                  <TableHead className="hidden md:table-cell">
                    Cluster
                  </TableHead>
                  <TableHead className="hidden lg:table-cell">
                    Details
                  </TableHead>
                  <TableHead>User</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredItems?.map((entry) => (
                  <EventRow
                    key={entry.id}
                    entry={entry}
                    requester={haRequester(
                      parseDetails(entry.details),
                      actorByUpid,
                    )}
                    expanded={expandedId === entry.id}
                    onToggle={() => {
                      setExpandedId(expandedId === entry.id ? null : entry.id);
                    }}
                  />
                ))}
                {(!filteredItems || filteredItems.length === 0) && (
                  <TableRow className="hover:bg-transparent">
                    <TableCell
                      colSpan={COLUMN_COUNT}
                      className="py-10 text-center text-muted-foreground"
                    >
                      No events found.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>

            <div className="flex items-center justify-end gap-1 border-t px-[18px] pt-3">
              <span className="mr-1 text-[12.5px] text-muted-foreground">
                Page {page + 1} of {Math.max(1, totalPages)}
              </span>
              <Button
                aria-label="Previous page"
                variant="ghost"
                size="icon"
                className="h-7 w-7"
                disabled={page === 0}
                onClick={() => {
                  setPage((p) => Math.max(0, p - 1));
                }}
              >
                <ChevronLeft className="h-4 w-4" />
              </Button>
              <Button
                aria-label="Next page"
                variant="ghost"
                size="icon"
                className="h-7 w-7"
                disabled={page + 1 >= totalPages}
                onClick={() => {
                  setPage((p) => p + 1);
                }}
              >
                <ChevronRight className="h-4 w-4" />
              </Button>
            </div>
          </>
        )}
      </Panel>

      {/* Syslog forwarding config. Its routes all require a global manage:audit
          (registry_audit.go), and this tab is open to anyone with view:audit —
          every Viewer — who would get only a notice whose Retry can 403 again. */}
      {canManage("audit") && <SyslogConfigCard />}
    </div>
  );
}
