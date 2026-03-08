import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Eye, Download } from "lucide-react";
import { useReportRuns } from "../api/report-queries";
import type { ReportRun } from "@/types/api";

const REPORT_TYPE_LABELS: Record<string, string> = {
  resource_utilization: "Resource Utilization",
  vm_resource_usage: "VM Resource Usage",
  capacity_forecast: "Capacity Forecast",
  backup_compliance: "Backup Compliance",
  patch_status: "Patch Status",
  uptime_summary: "Uptime Summary",
};

const STATUS_VARIANTS: Record<string, "default" | "secondary" | "destructive" | "outline"> = {
  completed: "default",
  running: "outline",
  pending: "secondary",
  failed: "destructive",
};

interface ReportRunsTableProps {
  onPreview?: (run: ReportRun) => void;
}

export function ReportRunsTable({ onPreview }: ReportRunsTableProps) {
  const { data: runs, isLoading, error } = useReportRuns();

  if (isLoading) return <div className="py-8 text-center text-muted-foreground">Loading...</div>;
  if (error) return <div className="py-8 text-center text-destructive">Failed to load report history: {error instanceof Error ? error.message : "Unknown error"}</div>;
  if (!runs?.length) return <div className="py-8 text-center text-muted-foreground">No report runs yet.</div>;

  const handleDownloadCSV = (run: ReportRun) => {
    const token = localStorage.getItem("access_token") ?? "";
    const link = document.createElement("a");
    // Use fetch to handle auth
    void fetch(`/api/v1/reports/runs/${run.id}/csv`, {
      headers: { Authorization: `Bearer ${token}` },
    })
      .then((res) => res.blob())
      .then((blob) => {
        link.href = URL.createObjectURL(blob);
        link.download = `report-${run.id}.csv`;
        link.click();
        URL.revokeObjectURL(link.href);
      });
  };

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Type</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Created</TableHead>
          <TableHead>Started</TableHead>
          <TableHead>Completed</TableHead>
          <TableHead>Time Range</TableHead>
          <TableHead className="w-[100px]">Actions</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {runs.map((run) => (
          <TableRow key={run.id}>
            <TableCell className="font-medium">
              {REPORT_TYPE_LABELS[run.report_type] ?? run.report_type}
            </TableCell>
            <TableCell>
              <Badge variant={STATUS_VARIANTS[run.status] ?? "secondary"}>
                {run.status}
              </Badge>
              {run.error_message ? (
                <span className="ml-2 text-xs text-destructive">{run.error_message}</span>
              ) : null}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {new Date(run.created_at).toLocaleString()}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {run.started_at ? new Date(run.started_at).toLocaleString() : "-"}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {run.completed_at ? new Date(run.completed_at).toLocaleString() : "-"}
            </TableCell>
            <TableCell>{run.time_range_hours}h</TableCell>
            <TableCell>
              {run.status === "completed" && (
                <div className="flex gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => onPreview?.(run)}
                    title="Preview HTML"
                  >
                    <Eye className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => handleDownloadCSV(run)}
                    title="Download CSV"
                  >
                    <Download className="h-4 w-4" />
                  </Button>
                </div>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
