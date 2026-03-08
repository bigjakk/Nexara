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
import { Trash2, Pencil, ToggleLeft, ToggleRight } from "lucide-react";
import { useReportSchedules, useUpdateReportSchedule, useDeleteReportSchedule } from "../api/report-queries";
import { useAuth } from "@/hooks/useAuth";
import type { ReportSchedule } from "@/types/api";

const REPORT_TYPE_LABELS: Record<string, string> = {
  resource_utilization: "Resource Utilization",
  vm_resource_usage: "VM Resource Usage",
  capacity_forecast: "Capacity Forecast",
  backup_compliance: "Backup Compliance",
  patch_status: "Patch Status",
  uptime_summary: "Uptime Summary",
};

interface ReportSchedulesTableProps {
  onEdit?: (schedule: ReportSchedule) => void;
}

export function ReportSchedulesTable({ onEdit }: ReportSchedulesTableProps) {
  const { data: schedules, isLoading, error } = useReportSchedules();
  const updateSchedule = useUpdateReportSchedule();
  const deleteSchedule = useDeleteReportSchedule();
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "report");

  if (isLoading) return <div className="py-8 text-center text-muted-foreground">Loading...</div>;
  if (error) return <div className="py-8 text-center text-destructive">Failed to load schedules: {error instanceof Error ? error.message : "Unknown error"}</div>;
  if (!schedules?.length) return <div className="py-8 text-center text-muted-foreground">No report schedules configured.</div>;

  const handleToggle = (s: ReportSchedule) => {
    updateSchedule.mutate({ id: s.id, enabled: !s.enabled });
  };

  const handleDelete = (s: ReportSchedule) => {
    if (confirm(`Delete schedule "${s.name}"?`)) {
      deleteSchedule.mutate(s.id);
    }
  };

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Type</TableHead>
          <TableHead>Schedule</TableHead>
          <TableHead>Format</TableHead>
          <TableHead>Email</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Last Run</TableHead>
          <TableHead>Next Run</TableHead>
          {canManage && <TableHead className="w-[100px]">Actions</TableHead>}
        </TableRow>
      </TableHeader>
      <TableBody>
        {schedules.map((s) => (
          <TableRow key={s.id}>
            <TableCell className="font-medium">{s.name}</TableCell>
            <TableCell>{REPORT_TYPE_LABELS[s.report_type] ?? s.report_type}</TableCell>
            <TableCell className="font-mono text-xs">{s.schedule || "Manual"}</TableCell>
            <TableCell><Badge variant="outline">{s.format.toUpperCase()}</Badge></TableCell>
            <TableCell>{s.email_enabled ? "Yes" : "No"}</TableCell>
            <TableCell>
              <Badge variant={s.enabled ? "default" : "secondary"}>
                {s.enabled ? "Enabled" : "Disabled"}
              </Badge>
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {s.last_run_at ? new Date(s.last_run_at).toLocaleString() : "-"}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">
              {s.next_run_at ? new Date(s.next_run_at).toLocaleString() : "-"}
            </TableCell>
            {canManage && (
              <TableCell>
                <div className="flex gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => { handleToggle(s); }}
                    title={s.enabled ? "Disable" : "Enable"}
                  >
                    {s.enabled ? <ToggleRight className="h-4 w-4" /> : <ToggleLeft className="h-4 w-4" />}
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => onEdit?.(s)}
                    title="Edit"
                  >
                    <Pencil className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => { handleDelete(s); }}
                    title="Delete"
                  >
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </div>
              </TableCell>
            )}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
