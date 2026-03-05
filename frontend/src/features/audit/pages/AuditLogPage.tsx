import { useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useAuditLog } from "../api/audit-queries";

const PAGE_SIZE = 25;

const selectClass =
  "flex h-9 w-[200px] rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const resourceTypes = [
  { value: "", label: "All Types" },
  { value: "vm", label: "VM" },
  { value: "ct", label: "Container" },
  { value: "cluster", label: "Cluster" },
  { value: "storage", label: "Storage" },
] as const;

export function AuditLogPage() {
  const [page, setPage] = useState(0);
  const [clusterFilter, setClusterFilter] = useState("");
  const [resourceFilter, setResourceFilter] = useState("");

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
                  <th className="px-4 py-2 text-left font-medium">User</th>
                </tr>
              </thead>
              <tbody>
                {data?.items.map((entry) => (
                  <tr key={entry.id} className="border-b">
                    <td className="px-4 py-2 text-muted-foreground">
                      {new Date(entry.created_at).toLocaleString()}
                    </td>
                    <td className="px-4 py-2">{entry.cluster_name}</td>
                    <td className="px-4 py-2">
                      <span className="inline-flex items-center rounded-full bg-muted px-2 py-0.5 text-xs font-medium">
                        {entry.resource_type}
                      </span>
                      {entry.resource_name ? (
                        <span className="ml-2 text-xs">
                          {entry.resource_name}
                          {entry.resource_vmid > 0 && (
                            <span className="text-muted-foreground"> ({String(entry.resource_vmid)})</span>
                          )}
                        </span>
                      ) : (
                        <span className="ml-2 text-xs text-muted-foreground">
                          {entry.resource_id.slice(0, 8)}
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2 font-medium">{entry.action}</td>
                    <td className="px-4 py-2">
                      {entry.user_display_name || entry.user_email}
                    </td>
                  </tr>
                ))}
                {data?.items.length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-4 py-8 text-center text-muted-foreground">
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
