import { useState } from "react";
import { PageHeader } from "@/components/page/PageHeader";
import { ReportSchedulesTable } from "../components/ReportSchedulesTable";
import { ReportRunsTable } from "../components/ReportRunsTable";
import { ReportScheduleForm } from "../components/ReportScheduleForm";
import { ReportGenerateDialog } from "../components/ReportGenerateDialog";
import { ReportCatalogue } from "../components/ReportCatalogue";
import { useReportRuns, useReportSchedules } from "../api/report-queries";
import { reportsSummary } from "../lib/reports-summary";
import { useAuth } from "@/hooks/useAuth";
import type { ReportSchedule } from "@/types/api";

export function ReportsPage() {
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "report");
  const canGenerate = hasPermission("generate", "report");
  const { data: runs } = useReportRuns();
  const { data: schedules } = useReportSchedules();

  const [editSchedule, setEditSchedule] = useState<
    ReportSchedule | undefined
  >();
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [scheduleType, setScheduleType] = useState<string | undefined>();
  const [generateOpen, setGenerateOpen] = useState(false);
  const [generateType, setGenerateType] = useState<string | undefined>();

  const handleEdit = (schedule: ReportSchedule) => {
    setEditSchedule(schedule);
    setScheduleType(undefined);
    setScheduleOpen(true);
  };

  return (
    <div className="space-y-5">
      <PageHeader
        title="Reports"
        summary={reportsSummary(schedules, runs)}
        actions={
          <>
            {canManage && <ReportScheduleForm />}
            {canGenerate && <ReportGenerateDialog />}
          </>
        }
      />

      {/* What will be produced and what has been, on the left; what can be
          asked for, on the right. Folds into one column on a narrow screen. */}
      <div className="flex flex-wrap items-start gap-4">
        <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-4">
          <ReportSchedulesTable onEdit={handleEdit} />
          <ReportRunsTable />
        </div>
        <div className="flex min-w-0 flex-[1_1_320px] flex-col gap-4">
          <ReportCatalogue
            runs={runs}
            canGenerate={canGenerate}
            canManage={canManage}
            onGenerate={(type) => {
              setGenerateType(type);
              setGenerateOpen(true);
            }}
            onSchedule={(type) => {
              setEditSchedule(undefined);
              setScheduleType(type);
              setScheduleOpen(true);
            }}
          />
        </div>
      </div>

      {canGenerate && (
        <ReportGenerateDialog
          initialType={generateType}
          open={generateOpen}
          onOpenChange={setGenerateOpen}
        />
      )}
      {canManage && (
        <ReportScheduleForm
          editSchedule={editSchedule}
          initialType={scheduleType}
          open={scheduleOpen}
          onOpenChange={(o) => {
            setScheduleOpen(o);
            if (!o) {
              setEditSchedule(undefined);
              setScheduleType(undefined);
            }
          }}
        />
      )}
    </div>
  );
}
