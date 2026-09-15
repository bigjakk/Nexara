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
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import type { ReportParameters, ReportSchedule } from "@/types/api";
import { REPORT_TYPES, reportTypeInfo } from "../report-types";
import { ReportParametersFields } from "./ReportParametersFields";

interface ReportScheduleFormProps {
  editSchedule?: ReportSchedule | undefined;
  /** Pre-selects a type when opened from a catalogue card. */
  initialType?: string | undefined;
  open?: boolean | undefined;
  onOpenChange?: ((open: boolean) => void) | undefined;
}

export function ReportScheduleForm({
  editSchedule,
  initialType,
  open: controlledOpen,
  onOpenChange,
}: ReportScheduleFormProps) {
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = onOpenChange ?? setInternalOpen;

  const [name, setName] = useState("");
  const [reportType, setReportType] = useState("backup_compliance");
  const [clusterId, setClusterId] = useState("");
  const [timeRangeHours, setTimeRangeHours] = useState(168);
  const [schedule, setSchedule] = useState("");
  const [format, setFormat] = useState("html");
  const [emailEnabled, setEmailEnabled] = useState(false);
  const [emailChannelId, setEmailChannelId] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [parameters, setParameters] = useState<ReportParameters>({});

  const createSchedule = useCreateReportSchedule();
  const updateSchedule = useUpdateReportSchedule();
  const { data: channels } = useNotificationChannels();
  const { data: clusters } = useClusters();

  const emailChannels =
    channels?.filter((c) => c.channel_type === "email") ?? [];
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
      setParameters(editSchedule.parameters);
    } else if (!editSchedule && open) {
      setName("");
      setReportType(initialType ?? "backup_compliance");
      setClusterId(
        clusters && clusters.length === 1 ? (clusters[0]?.id ?? "") : "",
      );
      setTimeRangeHours(168);
      setSchedule("");
      setFormat("html");
      setEmailEnabled(false);
      setEmailChannelId("");
      setEnabled(true);
      setParameters({});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reset on open only
  }, [editSchedule, initialType, open]);

  const info = reportTypeInfo(reportType);

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
      parameters,
      enabled,
    };

    if (isEditing) {
      updateSchedule.mutate(
        { id: editSchedule.id, ...data },
        {
          onSuccess: () => {
            setOpen(false);
          },
        },
      );
    } else {
      createSchedule.mutate(data, {
        onSuccess: () => {
          setOpen(false);
        },
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
          <Input
            value={name}
            onChange={(e) => {
              setName(e.target.value);
            }}
            placeholder="Weekly CPU Report"
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Report Type</Label>
            <Select value={reportType} onValueChange={setReportType}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {REPORT_TYPES.map((t) => (
                  <SelectItem key={t.value} value={t.value}>
                    {t.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {info && (
              <p className="text-xs text-muted-foreground">{info.blurb}</p>
            )}
          </div>

          <div className="space-y-2">
            <Label>Cluster</Label>
            <Select value={clusterId} onValueChange={setClusterId}>
              <SelectTrigger>
                <SelectValue placeholder="Select cluster" />
              </SelectTrigger>
              <SelectContent>
                {clusters?.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Cron Schedule</Label>
            <Input
              value={schedule}
              onChange={(e) => {
                setSchedule(e.target.value);
              }}
              placeholder="0 8 * * 1 (Mon 8am)"
            />
          </div>

          <div className="space-y-2">
            <Label>Time Range (hours)</Label>
            <Input
              type="number"
              min={1}
              max={8760}
              value={timeRangeHours}
              onChange={(e) => {
                setTimeRangeHours(Number(e.target.value));
              }}
            />
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Attachments</Label>
            <Select value={format} onValueChange={setFormat}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="html">HTML report</SelectItem>
                <SelectItem value="csv">HTML report + CSV</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              What an emailed run attaches; the body is always the findings
              digest.
            </p>
          </div>

          <div className="flex items-center gap-2 pt-6">
            <Switch
              checked={enabled}
              onCheckedChange={setEnabled}
              id="schedule-enabled"
            />
            <Label htmlFor="schedule-enabled">Enabled</Label>
          </div>
        </div>

        <ReportParametersFields
          reportType={reportType}
          value={parameters}
          onChange={setParameters}
        />

        <div className="space-y-2">
          <div className="flex items-center gap-2">
            <Switch
              checked={emailEnabled}
              onCheckedChange={setEmailEnabled}
              id="email-enabled"
            />
            <Label htmlFor="email-enabled">Email delivery</Label>
          </div>
          {emailEnabled && (
            <Select value={emailChannelId} onValueChange={setEmailChannelId}>
              <SelectTrigger>
                <SelectValue placeholder="Select email channel" />
              </SelectTrigger>
              <SelectContent>
                {emailChannels.map((ch) => (
                  <SelectItem key={ch.id} value={ch.id}>
                    {ch.name}
                  </SelectItem>
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
          {isPending
            ? "Saving..."
            : isEditing
              ? "Update Schedule"
              : "Create Schedule"}
        </Button>
      </div>
    </DialogContent>
  );

  // A controlled instance (the page's edit / catalogue-driven one) must not
  // mount a trigger of its own, or the page grows a second "New schedule"
  // button beneath the tables.
  if (isEditing || controlledOpen !== undefined) {
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
