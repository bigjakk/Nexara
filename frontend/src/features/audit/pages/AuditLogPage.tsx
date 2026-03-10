import { useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  ChevronDown,
  Monitor,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useAuditLog, type AuditLogEntry } from "../api/audit-queries";

const PAGE_SIZE = 25;

const selectClass =
  "flex h-9 w-[200px] rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const resourceTypes = [
  { value: "", label: "All Types" },
  { value: "vm", label: "VM / CT" },
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
] as const;

/** Convert snake_case action names to human-readable labels. */
function formatAction(action: string): string {
  return action
    .replace(/_/g, " ")
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

/** Resource type badge labels. */
function resourceTypeLabel(type: string): string {
  switch (type) {
    case "vm": return "VM";
    case "container": return "CT";
    case "migration": return "Migration";
    case "ceph_pool": return "Ceph";
    case "storage": return "Storage";
    case "auth": return "Auth";
    case "task": return "Task";
    case "drs": return "DRS";
    case "firewall": return "Firewall";
    case "sdn": return "SDN";
    case "schedule": return "Schedule";
    case "pbs": return "PBS";
    case "backup": return "Backup";
    case "cluster": return "Cluster";
    case "network": return "Network";
    default: return type;
  }
}

/** Parse the details JSON and render key human-readable summary parts. */
function formatDetailsSummary(entry: AuditLogEntry): string | null {
  if (!entry.details || entry.details === "{}" || entry.details === "null") {
    return null;
  }
  try {
    const d = JSON.parse(entry.details) as Record<string, unknown>;
    const parts: string[] = [];

    if (typeof d["vm_type"] === "string") {
      parts.push(d["vm_type"]);
    }
    if (typeof d["source_node"] === "string" && typeof d["target_node"] === "string") {
      parts.push(`${d["source_node"]} → ${d["target_node"]}`);
    }
    if (typeof d["migration_type"] === "string") {
      parts.push(d["migration_type"]);
    }
    if (d["online"] === true) {
      parts.push("live");
    }
    if (typeof d["error"] === "string") {
      parts.push(`Error: ${d["error"]}`);
    }
    if (typeof d["status"] === "string" && d["status"] !== "completed") {
      parts.push(d["status"]);
    }

    return parts.length > 0 ? parts.join(" · ") : null;
  } catch {
    return null;
  }
}

/** Pretty-print label for detail keys. */
function detailKeyLabel(key: string): string {
  switch (key) {
    case "vm_type": return "Type";
    case "vmid": return "VMID";
    case "source_node": return "Source Node";
    case "target_node": return "Target Node";
    case "migration_type": return "Migration Type";
    case "online": return "Live Migration";
    case "error": return "Error";
    case "status": return "Status";
    case "bwlimit_kib": return "BW Limit (KiB)";
    case "delete_source": return "Delete Source";
    case "target_vmid": return "Target VMID";
    case "storage": return "Storage";
    case "pool": return "Pool";
    case "name": return "Name";
    case "size": return "Size";
    default: return key.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
  }
}

/** Format detail values for display. */
function detailValue(_key: string, val: unknown): string {
  if (typeof val === "boolean") return val ? "Yes" : "No";
  if (typeof val === "number") return String(val);
  if (typeof val === "string") return val;
  if (val === null || val === undefined) return "-";
  return JSON.stringify(val);
}

/** Parse details JSON into key-value pairs. */
function parseDetails(entry: AuditLogEntry): Array<[string, string]> | null {
  if (!entry.details || entry.details === "{}" || entry.details === "null") {
    return null;
  }
  try {
    const d = JSON.parse(entry.details) as Record<string, unknown>;
    const pairs: Array<[string, string]> = [];
    for (const [k, v] of Object.entries(d)) {
      pairs.push([detailKeyLabel(k), detailValue(k, v)]);
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

/** When the VM record no longer exists (e.g. cross-cluster migration changed the UUID),
 *  extract VMID/name from the details JSON so the row isn't just a truncated UUID. */
function ResourceFallback({ entry }: { entry: AuditLogEntry }) {
  try {
    const d = JSON.parse(entry.details) as Record<string, unknown>;
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
    const vmid = typeof d["vmid"] === "number" ? d["vmid"] : null;
    const vmType = typeof d["vm_type"] === "string" ? d["vm_type"] : null;
    if (vmid != null) {
      return (
        <span className="ml-2 text-xs">
          {vmType ?? "VM"} {String(vmid)}
        </span>
      );
    }
    if (resId) {
      return (
        <span className="ml-2 text-xs text-muted-foreground">VMID {resId}</span>
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

function AuditRow({
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
          <td colSpan={6} className="px-4 py-3">
            <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
              <span className="text-muted-foreground">Timestamp</span>
              <span>{new Date(entry.created_at).toLocaleString()}</span>

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

            {/* Expanded details from JSON */}
            {details && details.length > 0 && (
              <div className="mt-2 border-t pt-2">
                <span className="text-xs font-medium text-muted-foreground">Details</span>
                <div className="mt-1 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
                  {details.map(([label, value]) => (
                    <div key={label} className="contents">
                      <span className="text-muted-foreground">{label}</span>
                      <span className={
                        label === "Error"
                          ? "text-red-500 break-all"
                          : "break-all"
                      }>
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

export function AuditLogPage() {
  const [page, setPage] = useState(0);
  const [clusterFilter, setClusterFilter] = useState("");
  const [resourceFilter, setResourceFilter] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const { data: clusters } = useClusters();
  const { data, isLoading, error } = useAuditLog({
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
    clusterId: clusterFilter || undefined,
    resourceType: resourceFilter || undefined,
  });

  const totalPages = data ? Math.ceil(data.total / PAGE_SIZE) : 0;

  return (
    <div className="space-y-4 p-6">
      <h1 className="text-2xl font-bold">Audit Log</h1>

      {/* Filters */}
      <div className="flex flex-wrap gap-3">
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

      {/* Table */}
      {isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : error ? (
        <p className="text-destructive">{error.message}</p>
      ) : (
        <>
          <div className="rounded-md border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b bg-muted/50">
                  <th className="px-4 py-2 text-left font-medium">Timestamp</th>
                  <th className="px-4 py-2 text-left font-medium">Cluster</th>
                  <th className="px-4 py-2 text-left font-medium">Resource</th>
                  <th className="px-4 py-2 text-left font-medium">Action</th>
                  <th className="px-4 py-2 text-left font-medium">Details</th>
                  <th className="px-4 py-2 text-left font-medium">User</th>
                </tr>
              </thead>
              <tbody>
                {data?.items.map((entry) => (
                  <AuditRow
                    key={entry.id}
                    entry={entry}
                    expanded={expandedId === entry.id}
                    onToggle={() => {
                      setExpandedId(expandedId === entry.id ? null : entry.id);
                    }}
                  />
                ))}
                {data?.items.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-4 py-8 text-center text-muted-foreground">
                      No audit log entries found.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {/* Pagination */}
          <div className="flex items-center justify-between">
            <p className="text-sm text-muted-foreground">
              {data ? `${String(data.total)} total entries` : ""}
            </p>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={page === 0}
                onClick={() => { setPage((p) => Math.max(0, p - 1)); }}
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
                onClick={() => { setPage((p) => p + 1); }}
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
