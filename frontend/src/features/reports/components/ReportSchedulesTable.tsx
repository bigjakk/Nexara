import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import { Switch } from "@/components/ui/switch";
import { Trash2, Pencil } from "lucide-react";
import {
  useReportSchedules,
  useUpdateReportSchedule,
  useDeleteReportSchedule,
} from "../api/report-queries";
import { useAuth } from "@/hooks/useAuth";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { formatDateTime, formatExactDateTime } from "@/lib/format";
import type { ReportSchedule } from "@/types/api";
import { reportTypeLabel, periodLabel } from "../report-types";
import { DateTime } from "@/components/DateTime";

interface ReportSchedulesTableProps {
  onEdit?: (schedule: ReportSchedule) => void;
}

/** What a run of the schedule sends, in words. */
function delivers(s: ReportSchedule): string {
  const files = s.format === "csv" ? "HTML and CSV" : "HTML";
  return s.email_enabled ? `Email digest with ${files}` : `${files}, stored`;
}

export function ReportSchedulesTable({ onEdit }: ReportSchedulesTableProps) {
  const { data: schedules, isLoading, error } = useReportSchedules();
  const updateSchedule = useUpdateReportSchedule();
  const deleteSchedule = useDeleteReportSchedule();
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "report");
  const { data: clusters } = useClusters();

  const handleToggle = (s: ReportSchedule) => {
    updateSchedule.mutate({ id: s.id, enabled: !s.enabled });
  };

  const handleDelete = (s: ReportSchedule) => {
    if (confirm(`Delete schedule "${s.name}"?`)) {
      deleteSchedule.mutate(s.id);
    }
  };

  const enabledCount = schedules?.filter((s) => s.enabled).length ?? 0;

  return (
    <Panel
      flush
      title="Schedules"
      aside={
        schedules && schedules.length > 0
          ? `${String(enabledCount)} of ${String(schedules.length)} enabled`
          : undefined
      }
    >
      {isLoading ? (
        <p className="px-[18px] pb-1 text-sm text-muted-foreground">
          Loading...
        </p>
      ) : error ? (
        <p className="px-[18px] pb-1 text-sm text-destructive">
          Failed to load schedules:{" "}
          {error instanceof Error ? error.message : "Unknown error"}
        </p>
      ) : !schedules?.length ? (
        <p className="px-[18px] pb-1 text-sm text-muted-foreground">
          No report schedules configured. A schedule generates a report on a
          timer and can email it.
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>Schedule</TableHead>
              <TableHead>Runs</TableHead>
              <TableHead className="hidden sm:table-cell">Delivers</TableHead>
              <TableHead>Next run</TableHead>
              <TableHead>Enabled</TableHead>
              {canManage && (
                <TableHead className="w-20">
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {schedules.map((s) => (
              <TableRow key={s.id}>
                <TableCell className="py-2">
                  <div className="font-medium">{s.name}</div>
                  <div className="text-[12.5px] text-muted-foreground">
                    {reportTypeLabel(s.report_type)}
                    {" · "}
                    {clusters?.find((c) => c.id === s.cluster_id)?.name ?? "—"}
                  </div>
                </TableCell>
                <TableCell className="py-2">
                  <div className="whitespace-nowrap font-mono text-[12.5px]">
                    {s.schedule || "Manual"}
                  </div>
                  <div className="whitespace-nowrap text-[12.5px] text-muted-foreground">
                    covers {periodLabel(s.time_range_hours)}
                  </div>
                </TableCell>
                <TableCell className="hidden py-2 text-muted-foreground sm:table-cell">
                  {delivers(s)}
                </TableCell>
                <TableCell className="py-2">
                  <div className="whitespace-nowrap">
                    <DateTime value={s.next_run_at} />
                  </div>
                  <div
                    className="text-[12.5px] text-muted-foreground"
                    title={
                      s.last_run_at
                        ? formatExactDateTime(s.last_run_at)
                        : undefined
                    }
                  >
                    {s.last_run_at
                      ? `last ran ${formatDateTime(s.last_run_at)}`
                      : "never run"}
                  </div>
                </TableCell>
                <TableCell className="py-2">
                  {canManage ? (
                    <Switch
                      checked={s.enabled}
                      onCheckedChange={() => {
                        handleToggle(s);
                      }}
                      aria-label={`${s.name} enabled`}
                      title={s.enabled ? "Disable" : "Enable"}
                    />
                  ) : (
                    <span
                      className={
                        s.enabled ? undefined : "text-muted-foreground"
                      }
                    >
                      {s.enabled ? "Enabled" : "Disabled"}
                    </span>
                  )}
                </TableCell>
                {canManage && (
                  <TableCell className="py-1.5">
                    <div className="flex justify-end gap-0.5">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-8 w-8 text-muted-foreground"
                        onClick={() => onEdit?.(s)}
                        title="Edit"
                        aria-label={`Edit ${s.name}`}
                      >
                        <Pencil className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-8 w-8 text-muted-foreground hover:text-destructive"
                        onClick={() => {
                          handleDelete(s);
                        }}
                        title="Delete"
                        aria-label={`Delete ${s.name}`}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  );
}
