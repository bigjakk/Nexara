import { useState, useCallback } from "react";
import {
  ChevronLeft,
  ChevronRight,
  ChevronDown,
  Download,
  Activity,
  Monitor,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useEvents,
  useAuditActions,
  useAuditUsers,
  buildExportUrl,
  type AuditLogEntry,
} from "../api/events-queries";
import { SyslogConfigCard } from "../components/SyslogConfigCard";

const PAGE_SIZE = 25;

const selectClass =
  "flex h-9 w-[180px] rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const inputClass =
  "flex h-9 w-[180px] rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const resourceTypes = [
  { value: "", label: "All Types" },
  { value: "vm", label: "VM" },
  { value: "container", label: "Container" },
  { value: "migration", label: "Migration" },
  { value: "cluster", label: "Cluster" },
  { value: "storage", label: "Storage" },
  { value: "ceph_pool", label: "Ceph Pool" },
  { value: "auth", label: "Auth" },
  { value: "task", label: "Task" },
  { value: "drs", label: "DRS" },
  { value: "firewall", label: "Firewall" },
  { value: "sdn", label: "SDN" },
  { value: "schedule", label: "Schedule" },
  { value: "pbs", label: "PBS" },
  { value: "backup", label: "Backup" },
  { value: "network", label: "Network" },
  { value: "role", label: "Role" },
  { value: "rolling_update", label: "Rolling Update" },
  { value: "cve_scan", label: "CVE Scan" },
  { value: "proxmox_task", label: "Proxmox Task" },
  { value: "node", label: "Node" },
] as const;

type Severity = "info" | "warning" | "error" | "all";

function deriveSeverity(action: string, details: string): "info" | "warning" | "error" {
  // Check for error indicators in details
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

function severityBadge(severity: "info" | "warning" | "error") {
  const colors = {
    info: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
    warning: "bg-yellow-500/10 text-yellow-600 dark:text-yellow-400",
    error: "bg-red-500/10 text-red-600 dark:text-red-400",
  };
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${colors[severity]}`}
    >
      {severity}
    </span>
  );
}

function formatAction(action: string): string {
  return action
    .replace(/_/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

function resourceTypeLabel(type: string): string {
  const found = resourceTypes.find((rt) => rt.value === type);
  if (found) return found.label;
  return type.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

function formatDetailsSummary(entry: AuditLogEntry): string | null {
  if (!entry.details || entry.details === "{}" || entry.details === "null") {
    return null;
  }
  try {
    const d = JSON.parse(entry.details) as Record<string, unknown>;
    const parts: string[] = [];
    if (typeof d["vm_type"] === "string") parts.push(d["vm_type"]);
    if (typeof d["source_node"] === "string" && typeof d["target_node"] === "string") {
      parts.push(`${d["source_node"]} → ${d["target_node"]}`);
    }
    if (typeof d["migration_type"] === "string") parts.push(d["migration_type"]);
    if (d["online"] === true) parts.push("live");
    if (typeof d["error"] === "string") parts.push(`Error: ${d["error"]}`);
    if (typeof d["status"] === "string" && d["status"] !== "completed") parts.push(d["status"]);
    return parts.length > 0 ? parts.join(" · ") : null;
  } catch {
    return null;
  }
}

function detailKeyLabel(key: string): string {
  const labels: Record<string, string> = {
    vm_type: "Type",
    vmid: "VMID",
    source_node: "Source Node",
    target_node: "Target Node",
    migration_type: "Migration Type",
    online: "Live Migration",
    error: "Error",
    status: "Status",
    bwlimit_kib: "BW Limit (KiB)",
    delete_source: "Delete Source",
    target_vmid: "Target VMID",
    storage: "Storage",
    pool: "Pool",
    name: "Name",
    size: "Size",
  };
  return labels[key] ?? key.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

function detailValue(val: unknown): string {
  if (typeof val === "boolean") return val ? "Yes" : "No";
  if (typeof val === "number") return String(val);
  if (typeof val === "string") return val;
  if (val === null || val === undefined) return "-";
  return JSON.stringify(val);
}

function parseDetails(entry: AuditLogEntry): Array<[string, string]> | null {
  if (!entry.details || entry.details === "{}" || entry.details === "null") return null;
  try {
    const d = JSON.parse(entry.details) as Record<string, unknown>;
    const pairs: Array<[string, string]> = [];
    for (const [k, v] of Object.entries(d)) {
      pairs.push([detailKeyLabel(k), detailValue(v)]);
    }
    return pairs.length > 0 ? pairs : null;
  } catch {
    return null;
  }
}

function sourceBadge(entry: AuditLogEntry) {
  if (entry.source === "proxmox") {
    let proxmoxUser = "";
    try {
      const d = JSON.parse(entry.details) as Record<string, unknown>;
      if (typeof d["proxmox_user"] === "string") proxmoxUser = d["proxmox_user"];
    } catch {
      // ignore
    }
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-orange-500/10 px-2 py-0.5 text-xs font-medium text-orange-600 dark:text-orange-400">
        <Monitor className="h-3 w-3" />
        PVE
        {proxmoxUser && <span className="text-muted-foreground">({proxmoxUser})</span>}
      </span>
    );
  }
  return (
    <span className="text-sm">
      {entry.user_display_name || entry.user_email}
    </span>
  );
}

function ResourceFallback({ entry }: { entry: AuditLogEntry }) {
  try {
    const d = JSON.parse(entry.details) as Record<string, unknown>;
    // Proxmox-sourced entries store name + VMID in details
    const resName = typeof d["resource_name"] === "string" ? d["resource_name"] : null;
    const resId = typeof d["resource_id"] === "string" ? d["resource_id"] : null;
    if (resName) {
      return (
        <span className="ml-2 text-xs">
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
        <span className="ml-2 text-xs">
          {vmType ?? "VM"} {String(vmid)}
        </span>
      );
    }
    // Proxmox task with VMID but no name resolved
    if (resId) {
      return (
        <span className="ml-2 text-xs text-muted-foreground">
          VMID {resId}
        </span>
      );
    }
  } catch {
    // ignore
  }
  return (
    <span className="ml-2 text-xs text-muted-foreground">
      {entry.resource_id.slice(0, 8)}
    </span>
  );
}

function EventRow({
  entry,
  expanded,
  onToggle,
}: {
  entry: AuditLogEntry;
  expanded: boolean;
  onToggle: () => void;
}) {
  const summary = formatDetailsSummary(entry);
  const details = expanded ? parseDetails(entry) : null;
  const severity = deriveSeverity(entry.action, entry.details);

  return (
    <>
      <tr
        className="cursor-pointer border-b hover:bg-muted/20"
        onClick={onToggle}
      >
        <td className="px-4 py-2 text-muted-foreground whitespace-nowrap">
          <div className="flex items-center gap-1.5">
            <ChevronDown
              className={`h-3 w-3 text-muted-foreground transition-transform ${expanded ? "" : "-rotate-90"}`}
            />
            {new Date(entry.created_at).toLocaleString()}
          </div>
        </td>
        <td className="px-4 py-2">{severityBadge(severity)}</td>
        <td className="px-4 py-2">{entry.cluster_name || "System"}</td>
        <td className="px-4 py-2">
          <span className="inline-flex items-center rounded-full bg-muted px-2 py-0.5 text-xs font-medium">
            {resourceTypeLabel(entry.resource_type)}
          </span>
          {entry.resource_name ? (
            <span className="ml-2 text-xs">
              {entry.resource_name}
              {entry.resource_vmid > 0 && (
                <span className="text-muted-foreground"> ({String(entry.resource_vmid)})</span>
              )}
            </span>
          ) : (
            <ResourceFallback entry={entry} />
          )}
        </td>
        <td className="px-4 py-2 font-medium">
          {formatAction(entry.action)}
        </td>
        <td className="px-4 py-2 text-xs text-muted-foreground">
          {summary ?? ""}
        </td>
        <td className="px-4 py-2">
          {sourceBadge(entry)}
        </td>
      </tr>
      {expanded && (
        <tr className="border-b bg-muted/10">
          <td colSpan={7} className="px-4 py-3">
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">Timestamp</span>
              <span>{new Date(entry.created_at).toLocaleString()}</span>

              <span className="text-muted-foreground">Severity</span>
              <span>{severityBadge(severity)}</span>

              <span className="text-muted-foreground">Cluster</span>
              <span>{entry.cluster_name || "System"}</span>

              <span className="text-muted-foreground">Resource Type</span>
              <span>{resourceTypeLabel(entry.resource_type)}</span>

              {entry.resource_name && (
                <>
                  <span className="text-muted-foreground">Resource Name</span>
                  <span>
                    {entry.resource_name}
                    {entry.resource_vmid > 0 && ` (VMID ${String(entry.resource_vmid)})`}
                  </span>
                </>
              )}

              <span className="text-muted-foreground">Resource ID</span>
              <span className="break-all font-mono text-[10px]">{entry.resource_id}</span>

              <span className="text-muted-foreground">Action</span>
              <span className="font-medium">{formatAction(entry.action)}</span>

              <span className="text-muted-foreground">Source</span>
              <span>{sourceBadge(entry)}</span>

              <span className="text-muted-foreground">User</span>
              <span>
                {entry.user_display_name || entry.user_email}
                {entry.user_display_name && entry.user_email && (
                  <span className="ml-1 text-muted-foreground">({entry.user_email})</span>
                )}
              </span>

              <span className="text-muted-foreground">User ID</span>
              <span className="break-all font-mono text-[10px]">{entry.user_id}</span>
            </div>

            {details && details.length > 0 && (
              <div className="mt-2 border-t pt-2">
                <span className="text-xs font-medium text-muted-foreground">Details</span>
                <div className="mt-1 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
                  {details.map(([label, value]) => (
                    <div key={label} className="contents">
                      <span className="text-muted-foreground">{label}</span>
                      <span
                        className={
                          label === "Error"
                            ? "text-red-500 break-all"
                            : "break-all"
                        }
                      >
                        {value}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </td>
        </tr>
      )}
    </>
  );
}

function triggerDownload(url: string, filename: string) {
  const token = localStorage.getItem("access_token") ?? "";
  void fetch(url, {
    headers: { Authorization: `Bearer ${token}` },
  })
    .then((res) => res.blob())
    .then((blob) => {
      const link = document.createElement("a");
      link.href = URL.createObjectURL(blob);
      link.download = filename;
      link.click();
      URL.revokeObjectURL(link.href);
    });
}

export function EventsPage() {
  const [page, setPage] = useState(0);
  const [clusterFilter, setClusterFilter] = useState("");
  const [resourceFilter, setResourceFilter] = useState("");
  const [userFilter, setUserFilter] = useState("");
  const [actionFilter, setActionFilter] = useState("");
  const [sourceFilter, setSourceFilter] = useState("");
  const [severityFilter, setSeverityFilter] = useState<Severity>("all");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const { data: clusters } = useClusters();
  const { data: actions } = useAuditActions();
  const { data: users } = useAuditUsers();

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
      : data?.items.filter(
          (e) => deriveSeverity(e.action, e.details) === severityFilter,
        );

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
      triggerDownload(url, `audit-log.${ext}`);
    },
    [clusterFilter, resourceFilter, userFilter, actionFilter, startTime, endTime],
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

  const hasFilters =
    clusterFilter !== "" ||
    resourceFilter !== "" ||
    userFilter !== "" ||
    actionFilter !== "" ||
    sourceFilter !== "" ||
    severityFilter !== "all" ||
    startDate !== "" ||
    endDate !== "";

  return (
    <div className="space-y-4 p-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Activity className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-bold">Events</h1>
        </div>

        {/* Export buttons */}
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => { handleExport("csv"); }}
          >
            <Download className="mr-1 h-3 w-3" />
            CSV
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => { handleExport("json"); }}
          >
            <Download className="mr-1 h-3 w-3" />
            JSON
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => { handleExport("syslog"); }}
          >
            <Download className="mr-1 h-3 w-3" />
            Syslog
          </Button>
        </div>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap items-end gap-3">
        <div>
          <label className="mb-1 block text-xs text-muted-foreground">Cluster</label>
          <select
            className={selectClass}
            value={clusterFilter}
            onChange={(e) => {
              setClusterFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value="">All Clusters</option>
            {clusters?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted-foreground">Resource Type</label>
          <select
            className={selectClass}
            value={resourceFilter}
            onChange={(e) => {
              setResourceFilter(e.target.value);
              setPage(0);
            }}
          >
            {resourceTypes.map((rt) => (
              <option key={rt.value || "all"} value={rt.value}>
                {rt.label}
              </option>
            ))}
          </select>
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted-foreground">User</label>
          <select
            className={selectClass}
            value={userFilter}
            onChange={(e) => {
              setUserFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value="">All Users</option>
            {users?.map((u) => (
              <option key={u.id} value={u.id}>
                {u.display_name || u.email}
              </option>
            ))}
          </select>
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted-foreground">Action</label>
          <select
            className={selectClass}
            value={actionFilter}
            onChange={(e) => {
              setActionFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value="">All Actions</option>
            {actions?.map((a) => (
              <option key={a} value={a}>
                {formatAction(a)}
              </option>
            ))}
          </select>
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted-foreground">Source</label>
          <select
            className={selectClass}
            value={sourceFilter}
            onChange={(e) => {
              setSourceFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value="">All Sources</option>
            <option value="nexara">Nexara</option>
            <option value="proxmox">Proxmox</option>
          </select>
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted-foreground">Severity</label>
          <select
            className={selectClass}
            value={severityFilter}
            onChange={(e) => {
              setSeverityFilter(e.target.value as Severity);
              setPage(0);
            }}
          >
            <option value="all">All Levels</option>
            <option value="info">Info</option>
            <option value="warning">Warning</option>
            <option value="error">Error</option>
          </select>
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted-foreground">From</label>
          <input
            type="date"
            className={inputClass}
            value={startDate}
            onChange={(e) => {
              setStartDate(e.target.value);
              setPage(0);
            }}
          />
        </div>

        <div>
          <label className="mb-1 block text-xs text-muted-foreground">To</label>
          <input
            type="date"
            className={inputClass}
            value={endDate}
            onChange={(e) => {
              setEndDate(e.target.value);
              setPage(0);
            }}
          />
        </div>

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

      {/* Syslog forwarding config */}
      <SyslogConfigCard />

      {/* Table */}
      {isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : error ? (
        <p className="text-destructive">
          {error instanceof Error ? error.message : "Failed to load events"}
        </p>
      ) : (
        <>
          <div className="rounded-md border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b bg-muted/50">
                  <th className="px-4 py-2 text-left font-medium">Timestamp</th>
                  <th className="px-4 py-2 text-left font-medium">Severity</th>
                  <th className="px-4 py-2 text-left font-medium">Cluster</th>
                  <th className="px-4 py-2 text-left font-medium">Resource</th>
                  <th className="px-4 py-2 text-left font-medium">Action</th>
                  <th className="px-4 py-2 text-left font-medium">Details</th>
                  <th className="px-4 py-2 text-left font-medium">User</th>
                </tr>
              </thead>
              <tbody>
                {filteredItems?.map((entry) => (
                  <EventRow
                    key={entry.id}
                    entry={entry}
                    expanded={expandedId === entry.id}
                    onToggle={() => {
                      setExpandedId(expandedId === entry.id ? null : entry.id);
                    }}
                  />
                ))}
                {(!filteredItems || filteredItems.length === 0) && (
                  <tr>
                    <td colSpan={7} className="px-4 py-8 text-center text-muted-foreground">
                      No events found.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {/* Pagination */}
          <div className="flex items-center justify-between">
            <p className="text-sm text-muted-foreground">
              {data ? `${String(data.total)} total events` : ""}
            </p>
            <div className="flex items-center gap-2">
              <Button
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
                Page {page + 1} of {Math.max(1, totalPages)}
              </span>
              <Button
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
          </div>
        </>
      )}
    </div>
  );
}
