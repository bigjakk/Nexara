import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Play } from "lucide-react";
import { useGenerateReport } from "../api/report-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import type { ReportParameters } from "@/types/api";
import { REPORT_TYPES, reportTypeInfo } from "../report-types";
import { ReportParametersFields } from "./ReportParametersFields";

interface ReportGenerateDialogProps {
  /** Pre-selects a type when opened from a catalogue card. */
  initialType?: string | undefined;
  /** Controlled mode, for the catalogue; uncontrolled renders its own button. */
  open?: boolean | undefined;
  onOpenChange?: ((open: boolean) => void) | undefined;
}

export function ReportGenerateDialog({
  initialType,
  open: controlledOpen,
  onOpenChange,
}: ReportGenerateDialogProps) {
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = onOpenChange ?? setInternalOpen;
  const navigate = useNavigate();

  const [reportType, setReportType] = useState(
    initialType ?? "backup_compliance",
  );
  const [clusterId, setClusterId] = useState("");
  const [timeRangeHours, setTimeRangeHours] = useState(168);
  const [parameters, setParameters] = useState<ReportParameters>({});
  const generateReport = useGenerateReport();
  const errorMessage =
    generateReport.error instanceof Error ? generateReport.error.message : "";

  const { data: clusters } = useClusters();

  // Reset to the card's type each time the dialog opens, and pick the only
  // cluster automatically on a single-cluster install.
  useEffect(() => {
    if (!open) return;
    generateReport.reset();
    setParameters({});
    if (initialType) setReportType(initialType);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reset on open only
  }, [open, initialType]);

  useEffect(() => {
    if (clusters && clusters.length === 1 && !clusterId) {
      const only = clusters[0];
      if (only) setClusterId(only.id);
    }
  }, [clusters, clusterId]);

  const info = reportTypeInfo(reportType);

  const handleGenerate = () => {
    if (!clusterId || !reportType) return;
    generateReport.mutate(
      {
        report_type: reportType,
        cluster_id: clusterId,
        time_range_hours: timeRangeHours,
        parameters,
      },
      {
        onSuccess: (run) => {
          setOpen(false);
          void navigate(`/reports/runs/${run.id}`);
        },
      },
    );
  };

  const content = (
    <DialogContent>
      <DialogHeader>
        <DialogTitle>Generate report</DialogTitle>
      </DialogHeader>
      <div className="space-y-4 pt-2">
        <div className="space-y-2">
          <Label>Report</Label>
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

        <div className="grid grid-cols-2 gap-4">
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
          <div className="space-y-2">
            <Label htmlFor="generate-hours">Period (hours)</Label>
            <Input
              id="generate-hours"
              type="number"
              min={1}
              max={8760}
              value={timeRangeHours}
              onChange={(e) => {
                setTimeRangeHours(Number(e.target.value));
              }}
            />
            {info && !info.windowed && (
              <p className="text-xs text-muted-foreground">
                This report is a snapshot of now; the period is context only.
              </p>
            )}
          </div>
        </div>

        <ReportParametersFields
          reportType={reportType}
          value={parameters}
          onChange={setParameters}
        />

        {errorMessage && (
          <p className="text-sm text-destructive">{errorMessage}</p>
        )}
        <Button
          onClick={handleGenerate}
          disabled={!clusterId || generateReport.isPending}
          className="w-full"
        >
          {generateReport.isPending ? "Generating…" : "Generate"}
        </Button>
      </div>
    </DialogContent>
  );

  if (controlledOpen !== undefined) {
    return (
      <Dialog open={open} onOpenChange={setOpen}>
        {content}
      </Dialog>
    );
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>
          <Play className="mr-2 h-4 w-4" />
          Generate report
        </Button>
      </DialogTrigger>
      {content}
    </Dialog>
  );
}
