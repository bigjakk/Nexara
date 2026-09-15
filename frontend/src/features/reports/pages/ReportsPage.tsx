import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ReportSchedulesTable } from "../components/ReportSchedulesTable";
import { ReportRunsTable } from "../components/ReportRunsTable";
import { ReportScheduleForm } from "../components/ReportScheduleForm";
import { ReportGenerateDialog } from "../components/ReportGenerateDialog";
import { ReportCatalogue } from "../components/ReportCatalogue";
import { useReportRuns } from "../api/report-queries";
import { useAuth } from "@/hooks/useAuth";
import type { ReportSchedule } from "@/types/api";

export function ReportsPage() {
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "report");
  const canGenerate = hasPermission("generate", "report");
  const { data: runs } = useReportRuns();

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
    <div className="space-y-6 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Reports</h1>
          <p className="text-sm text-muted-foreground">
            Generate a report now, or schedule one and have it emailed.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {canGenerate && <ReportGenerateDialog />}
          {canManage && <ReportScheduleForm />}
        </div>
      </div>

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

      <Tabs defaultValue="history">
        <TabsList>
          <TabsTrigger value="history">Report history</TabsTrigger>
          <TabsTrigger value="schedules">Schedules</TabsTrigger>
        </TabsList>

        <TabsContent value="history" className="mt-4">
          <ReportRunsTable />
        </TabsContent>

        <TabsContent value="schedules" className="mt-4">
          <ReportSchedulesTable onEdit={handleEdit} />
        </TabsContent>
      </Tabs>

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
