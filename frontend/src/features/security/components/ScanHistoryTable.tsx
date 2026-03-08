import { useState } from "react";
import { ChevronDown, ChevronRight, Trash2, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import type { CVEScan, CVEScanNode } from "@/types/api";
import { useCVEScanDetail, useDeleteScan } from "../api/cve-queries";
import { SeverityBadge } from "./SeverityBadge";

interface ScanHistoryTableProps {
  scans: CVEScan[];
  clusterId: string;
  onSelectScan: (scanId: string) => void;
}

function StatusBadge({ status }: { status: string }) {
  const styles: Record<string, string> = {
    completed: "bg-green-500/10 text-green-500",
    running: "bg-blue-500/10 text-blue-500",
    pending: "bg-yellow-500/10 text-yellow-500",
    failed: "bg-red-500/10 text-red-500",
  };

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium",
        styles[status] ?? "bg-gray-500/10 text-gray-500",
      )}
    >
      {status === "running" && <Loader2 className="h-3 w-3 animate-spin" />}
      {status}
    </span>
  );
}

function ExpandedScanRow({
  clusterId,
  scanId,
}: {
  clusterId: string;
  scanId: string;
}) {
  const { data } = useCVEScanDetail(clusterId, scanId);

  if (!data) {
    return (
      <tr>
        <td colSpan={7} className="px-4 py-3 text-center text-sm text-muted-foreground">
          <Loader2 className="mx-auto h-4 w-4 animate-spin" />
        </td>
      </tr>
    );
  }

  return (
    <tr>
      <td colSpan={7} className="bg-muted/30 px-4 py-3">
        <div className="space-y-2">
          <h4 className="text-sm font-medium">Node Results</h4>
          <div className="grid gap-2">
            {data.nodes.map((node: CVEScanNode) => (
              <div
                key={node.id}
                className="flex items-center justify-between rounded-md border bg-card px-3 py-2 text-sm"
              >
                <div className="flex items-center gap-3">
                  <span className="font-medium">{node.node_name}</span>
                  <StatusBadge status={node.status} />
                </div>
                <div className="flex items-center gap-4 text-muted-foreground">
                  <span>{node.packages_total} packages</span>
                  <span>{node.vulns_found} vulns</span>
                  {node.posture_score > 0 && (
                    <span
                      className={cn(
                        "font-medium",
                        node.posture_score >= 90
                          ? "text-green-500"
                          : node.posture_score >= 70
                            ? "text-yellow-500"
                            : "text-red-500",
                      )}
                    >
                      Score: {Math.round(node.posture_score)}
                    </span>
                  )}
                  {node.error_message && (
                    <span className="text-red-500">{node.error_message}</span>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      </td>
    </tr>
  );
}

export function ScanHistoryTable({
  scans,
  clusterId,
  onSelectScan,
}: ScanHistoryTableProps) {
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const deleteScan = useDeleteScan();

  if (scans.length === 0) {
    return (
      <div className="rounded-lg border bg-card p-8 text-center text-muted-foreground">
        No scans have been run yet.
      </div>
    );
  }

  return (
    <div className="rounded-lg border bg-card">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-left text-muted-foreground">
            <th className="w-8 px-4 py-3" />
            <th className="px-4 py-3">Date</th>
            <th className="px-4 py-3">Status</th>
            <th className="px-4 py-3">Nodes</th>
            <th className="px-4 py-3">Vulnerabilities</th>
            <th className="px-4 py-3">Breakdown</th>
            <th className="w-16 px-4 py-3" />
          </tr>
        </thead>
        <tbody>
          {scans.map((scan) => {
            const isExpanded = expandedId === scan.id;
            return (
              <tbody key={scan.id}>
                <tr
                  className="cursor-pointer border-b transition-colors hover:bg-muted/50"
                  onClick={() => setExpandedId(isExpanded ? null : scan.id)}
                >
                  <td className="px-4 py-3">
                    {isExpanded ? (
                      <ChevronDown className="h-4 w-4" />
                    ) : (
                      <ChevronRight className="h-4 w-4" />
                    )}
                  </td>
                  <td className="px-4 py-3">
                    {new Date(scan.started_at).toLocaleString()}
                  </td>
                  <td className="px-4 py-3">
                    <StatusBadge status={scan.status} />
                  </td>
                  <td className="px-4 py-3">
                    {scan.scanned_nodes}/{scan.total_nodes}
                  </td>
                  <td className="px-4 py-3 font-medium">{scan.total_vulns}</td>
                  <td className="px-4 py-3">
                    <div className="flex gap-1">
                      {scan.critical_count > 0 && (
                        <SeverityBadge severity="critical" count={scan.critical_count} />
                      )}
                      {scan.high_count > 0 && (
                        <SeverityBadge severity="high" count={scan.high_count} />
                      )}
                      {scan.medium_count > 0 && (
                        <SeverityBadge severity="medium" count={scan.medium_count} />
                      )}
                      {scan.low_count > 0 && (
                        <SeverityBadge severity="low" count={scan.low_count} />
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex gap-1">
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          onSelectScan(scan.id);
                        }}
                        className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-accent-foreground"
                        title="View vulnerabilities"
                      >
                        <ChevronRight className="h-4 w-4" />
                      </button>
                      <button
                        onClick={(e) => {
                          e.stopPropagation();
                          deleteScan.mutate({ clusterId, scanId: scan.id });
                        }}
                        className="rounded p-1 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                        title="Delete scan"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </td>
                </tr>
                {isExpanded && (
                  <ExpandedScanRow clusterId={clusterId} scanId={scan.id} />
                )}
              </tbody>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
