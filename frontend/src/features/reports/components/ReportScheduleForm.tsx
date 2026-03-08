import { useState, useEffect } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Plus } from "lucide-react";
import {
  useCreateReportSchedule,
  useUpdateReportSchedule,
} from "../api/report-queries";
import { useNotificationChannels } from "@/features/alerts/api/alert-queries";
import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { ClusterResponse, ReportSchedule } from "@/types/api";

const REPORT_TYPES = [
  { value: "resource_utilization", label: "Resource Utilization" },
  { value: "vm_resource_usage", label: "VM Resource Usage" },
  { value: "capacity_forecast", label: "Capacity Forecast" },
  { value: "backup_compliance", label: "Backup Compliance" },
  { value: "patch_status", label: "Patch Status" },
  { value: "uptime_summary", label: "Uptime Summary" },
] as const;

interface ReportScheduleFormProps {
  editSchedule?: ReportSchedule | undefined;
  open?: boolean | undefined;
  onOpenChange?: ((open: boolean) => void) | undefined;
}

export function ReportScheduleForm({
  editSchedule,
  open: controlledOpen,
  onOpenChange,
}: ReportScheduleFormProps) {
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = onOpenChange ?? setInternalOpen;

  const [name, setName] = useState("");
  const [reportType, setReportType] = useState("resource_utilization");
  const [clusterId, setClusterId] = useState("");
  const [timeRangeHours, setTimeRangeHours] = useState(168);
  const [schedule, setSchedule] = useState("");
  const [format, setFormat] = useState("html");
  const [emailEnabled, setEmailEnabled] = useState(false);
  const [emailChannelId, setEmailChannelId] = useState("");
  const [enabled, setEnabled] = useState(true);

  const createSchedule = useCreateReportSchedule();
  const updateSchedule = useUpdateReportSchedule();
  const { data: channels } = useNotificationChannels();
  const { data: clusters } = useQuery({
    queryKey: ["clusters"],
    queryFn: () => apiClient.get<ClusterResponse[]>("/api/v1/clusters"),
    enabled: open,
  });

  const emailChannels = channels?.filter((c) => c.channel_type === "email") ?? [];
  const isEditing = !!editSchedule;

  useEffect(() => {
    if (editSchedule && open) {
      setName(editSchedule.name);
      setReportType(editSchedule.report_type);
      setClusterId(editSchedule.cluster_id);
      setTimeRangeHours(editSchedule.time_range_hours);
      setSchedule(editSchedule.schedule);
      setFormat(editSchedule.format);
      setEmailEnabled(editSchedule.email_enabled);
      setEmailChannelId(editSchedule.email_channel_id ?? "");
      setEnabled(editSchedule.enabled);
    } else if (!editSchedule && open) {
      setName("");
      setReportType("resource_utilization");
      setClusterId("");
      setTimeRangeHours(168);
      setSchedule("");
      setFormat("html");
      setEmailEnabled(false);
      setEmailChannelId("");
      setEnabled(true);
    }
  }, [editSchedule, open]);

  const handleSubmit = () => {
    const data = {
      name,
      report_type: reportType,
      cluster_id: clusterId,
      time_range_hours: timeRangeHours,
      schedule,
      format,
      email_enabled: emailEnabled,
      email_channel_id: emailEnabled ? emailChannelId || undefined : undefined,
      email_recipients: [] as string[],
      parameters: {} as Record<string, unknown>,
      enabled,
    };

    if (isEditing) {
      updateSchedule.mutate(
        { id: editSchedule.id, ...data },
        { onSuccess: () => { setOpen(false); } },
      );
    } else {
      createSchedule.mutate(data, {
        onSuccess: () => { setOpen(false); },
      });
    }
  };

  const isPending = createSchedule.isPending || updateSchedule.isPending;

  const content = (
    <DialogContent className="max-w-lg">
      <DialogHeader>
        <DialogTitle>
          {isEditing ? "Edit Report Schedule" : "New Report Schedule"}
        </DialogTitle>
      </DialogHeader>
      <div className="space-y-4 pt-4">
        <div className="space-y-2">
          <Label>Name</Label>
          <Input value={name} onChange={(e) => { setName(e.target.value); }} placeholder="Weekly CPU Report" />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Report Type</Label>
            <Select value={reportType} onValueChange={setReportType}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                {REPORT_TYPES.map((t) => (
                  <SelectItem key={t.value} value={t.value}>{t.label}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <Label>Cluster</Label>
            <Select value={clusterId} onValueChange={setClusterId}>
              <SelectTrigger><SelectValue placeholder="Select cluster" /></SelectTrigger>
              <SelectContent>
                {clusters?.map((c) => (
                  <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Cron Schedule</Label>
            <Input value={schedule} onChange={(e) => { setSchedule(e.target.value); }} placeholder="0 8 * * 1 (Mon 8am)" />
          </div>

          <div className="space-y-2">
            <Label>Time Range (hours)</Label>
            <Input type="number" min={1} max={8760} value={timeRangeHours} onChange={(e) => { setTimeRangeHours(Number(e.target.value)); }} />
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Format</Label>
            <Select value={format} onValueChange={setFormat}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="html">HTML</SelectItem>
                <SelectItem value="csv">CSV</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-center gap-2 pt-6">
            <Switch checked={enabled} onCheckedChange={setEnabled} id="schedule-enabled" />
            <Label htmlFor="schedule-enabled">Enabled</Label>
          </div>
        </div>

        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <Switch checked={emailEnabled} onCheckedChange={setEmailEnabled} id="email-enabled" />
            <Label htmlFor="email-enabled">Email delivery</Label>
          </div>
          {emailEnabled && (
            <Select value={emailChannelId} onValueChange={setEmailChannelId}>
              <SelectTrigger><SelectValue placeholder="Select email channel" /></SelectTrigger>
              <SelectContent>
                {emailChannels.map((ch) => (
                  <SelectItem key={ch.id} value={ch.id}>{ch.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>

        <Button
          onClick={handleSubmit}
          disabled={!name || !clusterId || isPending}
          className="w-full"
        >
          {isPending ? "Saving..." : isEditing ? "Update Schedule" : "Create Schedule"}
        </Button>
      </div>
    </DialogContent>
  );

  if (isEditing) {
    return (
      <Dialog open={open} onOpenChange={setOpen}>
        {content}
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline">
          <Plus className="mr-2 h-4 w-4" />
          New Schedule
        </Button>
      </DialogTrigger>
      {content}
    </Dialog>
  );
}
