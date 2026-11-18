import { useState } from "react";
import { ChevronDown, Trash2, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { CVEScan, CVEScanNode } from "@/types/api";
import { useCVEScanDetail, useDeleteScan } from "../api/cve-queries";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { SeverityBadge } from "./SeverityBadge";
import { DateTime } from "@/components/DateTime";
import { formatExactDateTime } from "@/lib/format";

interface ScanHistoryTableProps {
  scans: CVEScan[];
  clusterId: string;
  /** Deleting a scan takes manage:cve_scan on the cluster; without it there is no Delete. */
  canManage: boolean;
  onSelectScan: (scanId: string) => void;
}

/** How many columns the table has, for a row that spans them all. */
const COLUMN_COUNT = 6;

/** A scan's, or a node's, state in words. Finished is the ordinary case and stays plain. */
function ScanStatus({ status }: { status: string }) {
  if (status === "running") {
    return (
      <span className="inline-flex items-center gap-1.5">
        <Loader2 className="h-3.5 w-3.5 animate-spin text-info" />
        Running
      </span>
    );
  }
  if (status === "failed") {
    return (
      <span className="inline-flex items-center gap-[7px] text-red-700 dark:text-red-400">
        <span
          aria-hidden="true"
          className="h-[7px] w-[7px] shrink-0 rounded-full bg-red-500"
        />
        Failed
      </span>
    );
  }
  return (
    <span className="text-muted-foreground">
      {status.charAt(0).toUpperCase() + status.slice(1)}
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

  return (
    <TableRow className="hover:bg-transparent">
      <TableCell colSpan={COLUMN_COUNT} className="bg-foreground/[0.03] py-3">
        {!data ? (
          <Loader2 className="mx-auto h-4 w-4 animate-spin text-muted-foreground" />
        ) : (
          <>
            <h4 className="text-[12.5px] font-medium text-muted-foreground">
              Node results
            </h4>
            <ul className="mt-1 divide-y">
              {data.nodes.map((node: CVEScanNode) => (
                <li
                  key={node.id}
                  className="flex flex-wrap items-baseline gap-x-5 gap-y-0.5 py-2 last:pb-0"
                >
                  <span className="min-w-[8rem] font-medium">
                    {node.node_name}
                  </span>
                  <ScanStatus status={node.status} />
                  <span className="text-muted-foreground">
                    {node.packages_total} packages
                  </span>
                  <span className="text-muted-foreground">
                    {node.vulns_found} vulns
                  </span>
                  {node.posture_score > 0 && (
                    <span
                      className={cn(
                        node.posture_score >= 90
                          ? "text-muted-foreground"
                          : node.posture_score >= 70
                            ? "text-amber-700 dark:text-amber-400"
                            : "text-red-700 dark:text-red-400",
                      )}
                    >
                      Score: {Math.round(node.posture_score)}
                    </span>
                  )}
                  {node.error_message && (
                    <span className="text-red-700 [overflow-wrap:anywhere] dark:text-red-400">
                      {node.error_message}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </>
        )}
      </TableCell>
    </TableRow>
  );
}

export function ScanHistoryTable({
  scans,
  clusterId,
  canManage,
  onSelectScan,
}: ScanHistoryTableProps) {
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<CVEScan | null>(null);
  const deleteScan = useDeleteScan();

  if (scans.length === 0) {
    return (
      <p className="px-[18px] pb-1 text-sm text-muted-foreground">
        No scans have been run yet.
      </p>
    );
  }

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Scan</TableHead>
            <TableHead>Status</TableHead>
            <TableHead className="hidden sm:table-cell">Nodes</TableHead>
            <TableHead>Vulnerabilities</TableHead>
            <TableHead className="hidden sm:table-cell">By severity</TableHead>
            <TableHead className="w-32">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        {scans.map((scan) => {
          const isExpanded = expandedId === scan.id;
          return (
            <TableBody key={scan.id} className="border-b last:border-0">
              <TableRow
                className="cursor-pointer"
                onClick={() => {
                  setExpandedId(isExpanded ? null : scan.id);
                }}
              >
                <TableCell className="py-2 whitespace-nowrap">
                  <ChevronDown
                    className={cn(
                      "mr-1.5 inline h-3.5 w-3.5 text-muted-foreground transition-transform",
                      !isExpanded && "-rotate-90",
                    )}
                  />
                  <DateTime value={scan.started_at} />
                </TableCell>
                <TableCell className="py-2">
                  <ScanStatus status={scan.status} />
                </TableCell>
                <TableCell className="hidden py-2 tabular-nums text-muted-foreground sm:table-cell">
                  {scan.scanned_nodes}/{scan.total_nodes}
                </TableCell>
                <TableCell
                  className={cn(
                    "py-2 tabular-nums",
                    scan.total_vulns === 0
                      ? "text-muted-foreground"
                      : "font-medium",
                  )}
                >
                  {scan.total_vulns}
                </TableCell>
                <TableCell className="hidden py-2 sm:table-cell">
                  <div className="flex flex-wrap gap-1">
                    {scan.critical_count > 0 && (
                      <SeverityBadge
                        severity="critical"
                        count={scan.critical_count}
                      />
                    )}
                    {scan.high_count > 0 && (
                      <SeverityBadge severity="high" count={scan.high_count} />
                    )}
                    {scan.medium_count > 0 && (
                      <SeverityBadge
                        severity="medium"
                        count={scan.medium_count}
                      />
                    )}
                    {scan.low_count > 0 && (
                      <SeverityBadge severity="low" count={scan.low_count} />
                    )}
                  </div>
                </TableCell>
                <TableCell className="py-1.5">
                  <div className="flex items-center justify-end gap-0.5">
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-8 px-2.5 text-primary hover:text-primary"
                      onClick={(e) => {
                        e.stopPropagation();
                        onSelectScan(scan.id);
                      }}
                      aria-label="View vulnerabilities"
                    >
                      Findings
                    </Button>
                    {canManage && (
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-8 w-8 text-muted-foreground hover:text-destructive"
                        onClick={(e) => {
                          e.stopPropagation();
                          setPendingDelete(scan);
                        }}
                        title="Delete scan"
                        aria-label="Delete scan"
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
              {isExpanded && (
                <ExpandedScanRow clusterId={clusterId} scanId={scan.id} />
              )}
            </TableBody>
          );
        })}
      </Table>
      <ConfirmDeleteDialog
        target={pendingDelete}
        onClose={() => {
          setPendingDelete(null);
        }}
        onConfirm={(scan) => {
          deleteScan.mutate({ clusterId, scanId: scan.id });
        }}
        title={(scan) =>
          `Delete the scan from ${formatExactDateTime(scan.started_at)}?`
        }
        // cve_scan_nodes and cve_scan_vulns are ON DELETE CASCADE from
        // cve_scans (migration 000020). The posture reads the newest
        // remaining completed scan (queries/cve.sql GetClusterSecuritySummary);
        // with none, handlers.CVEHandler.GetSecurityPosture answers
        // status "no_scans", PostureScore 100.
        description={() =>
          "Its per-node results and every vulnerability finding it recorded are deleted. If it is the newest completed scan, the security posture falls back to the newest completed scan left, or, if none is left, reads as though no scan had ever run. This cannot be undone."
        }
      />
    </>
  );
}
