import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ReportSchedulesTable } from "../components/ReportSchedulesTable";
import { ReportRunsTable } from "../components/ReportRunsTable";
import { ReportScheduleForm } from "../components/ReportScheduleForm";
import { ReportGenerateDialog } from "../components/ReportGenerateDialog";
import { ReportPreview } from "../components/ReportPreview";
import { useAuth } from "@/hooks/useAuth";
import type { ReportSchedule, ReportRun } from "@/types/api";

export function ReportsPage() {
  const { hasPermission } = useAuth();
  const canManage = hasPermission("manage", "report");
  const canGenerate = hasPermission("generate", "report");

  const [editSchedule, setEditSchedule] = useState<ReportSchedule | undefined>();
  const [editOpen, setEditOpen] = useState(false);
  const [previewRunId, setPreviewRunId] = useState("");
  const [previewOpen, setPreviewOpen] = useState(false);

  const handleEdit = (schedule: ReportSchedule) => {
    setEditSchedule(schedule);
    setEditOpen(true);
  };

  const handlePreview = (run: ReportRun) => {
    setPreviewRunId(run.id);
    setPreviewOpen(true);
  };

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Reports</h1>
        <div className="flex gap-2">
          {canGenerate && <ReportGenerateDialog />}
          {canManage && <ReportScheduleForm />}
        </div>
      </div>

      <Tabs defaultValue="schedules">
        <TabsList>
          <TabsTrigger value="schedules">Schedules</TabsTrigger>
          <TabsTrigger value="history">Report History</TabsTrigger>
        </TabsList>

        <TabsContent value="schedules" className="mt-4">
          <ReportSchedulesTable onEdit={handleEdit} />
        </TabsContent>

        <TabsContent value="history" className="mt-4">
          <ReportRunsTable onPreview={handlePreview} />
        </TabsContent>
      </Tabs>

      {canManage && (
        <ReportScheduleForm
          editSchedule={editSchedule}
          open={editOpen}
          onOpenChange={(o) => {
            setEditOpen(o);
            if (!o) setEditSchedule(undefined);
          }}
        />
      )}

      <ReportPreview
        runId={previewRunId}
        open={previewOpen}
        onOpenChange={setPreviewOpen}
      />
    </div>
  );
}
