import { useState } from "react";
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
import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { ClusterResponse } from "@/types/api";

const REPORT_TYPES = [
  { value: "resource_utilization", label: "Resource Utilization" },
  { value: "vm_resource_usage", label: "VM Resource Usage" },
  { value: "capacity_forecast", label: "Capacity Forecast" },
  { value: "backup_compliance", label: "Backup Compliance" },
  { value: "patch_status", label: "Patch Status" },
  { value: "uptime_summary", label: "Uptime Summary" },
] as const;

export function ReportGenerateDialog() {
  const [open, setOpen] = useState(false);
  const [reportType, setReportType] = useState("resource_utilization");
  const [clusterId, setClusterId] = useState("");
  const [timeRangeHours, setTimeRangeHours] = useState(168);
  const [successMessage, setSuccessMessage] = useState("");
  const generateReport = useGenerateReport();
  const errorMessage =
    generateReport.error instanceof Error
      ? generateReport.error.message
      : "";

  const { data: clusters } = useQuery({
    queryKey: ["clusters"],
    queryFn: () => apiClient.get<ClusterResponse[]>("/api/v1/clusters"),
    enabled: open,
  });

  const handleOpenChange = (v: boolean) => {
    setOpen(v);
    if (v) {
      generateReport.reset();
      setSuccessMessage("");
    }
  };

  const handleGenerate = () => {
    if (!clusterId || !reportType) return;
    setSuccessMessage("");
    generateReport.mutate(
      {
        report_type: reportType,
        cluster_id: clusterId,
        time_range_hours: timeRangeHours,
      },
      {
        onSuccess: () => {
          setSuccessMessage("Report generated successfully! Check the Report History tab.");
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>
        <Button>
          <Play className="mr-2 h-4 w-4" />
          Generate Report
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Generate Report</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 pt-4">
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

          <div className="space-y-2">
            <Label>Time Range (hours)</Label>
            <Input
              type="number"
              min={1}
              max={8760}
              value={timeRangeHours}
              onChange={(e) => setTimeRangeHours(Number(e.target.value))}
            />
          </div>

          {errorMessage && (
            <p className="text-sm text-destructive">{errorMessage}</p>
          )}
          {successMessage && (
            <p className="text-sm text-green-600">{successMessage}</p>
          )}
          <Button
            onClick={handleGenerate}
            disabled={!clusterId || generateReport.isPending || !!successMessage}
            className="w-full"
          >
            {generateReport.isPending ? "Generating..." : successMessage ? "Done" : "Generate"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
