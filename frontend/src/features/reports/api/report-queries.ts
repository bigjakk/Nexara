import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { ReportSchedule, ReportRun } from "@/types/api";

// --- Report Schedules ---

export function useReportSchedules() {
  return useQuery({
    queryKey: ["report-schedules"],
    queryFn: () =>
      apiClient.get<ReportSchedule[]>("/api/v1/reports/schedules"),
  });
}

export function useReportSchedule(id: string) {
  return useQuery({
    queryKey: ["report-schedules", id],
    queryFn: () =>
      apiClient.get<ReportSchedule>(`/api/v1/reports/schedules/${id}`),
    enabled: !!id,
  });
}

interface ReportScheduleRequest {
  name: string;
  report_type: string;
  cluster_id: string;
  time_range_hours: number;
  schedule: string;
  format: string;
  email_enabled: boolean;
  email_channel_id?: string | undefined;
  email_recipients: string[];
  parameters: Record<string, unknown>;
  enabled: boolean;
}

export function useCreateReportSchedule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: ReportScheduleRequest) =>
      apiClient.post<ReportSchedule>("/api/v1/reports/schedules", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["report-schedules"] });
    },
  });
}

export function useUpdateReportSchedule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...data
    }: Partial<ReportScheduleRequest> & { id: string }) =>
      apiClient.put<ReportSchedule>(`/api/v1/reports/schedules/${id}`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["report-schedules"] });
    },
  });
}

export function useDeleteReportSchedule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(`/api/v1/reports/schedules/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["report-schedules"] });
    },
  });
}

// --- Report Generation ---

export function useGenerateReport() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      report_type: string;
      cluster_id: string;
      time_range_hours: number;
      format?: string;
    }) => apiClient.post<ReportRun>("/api/v1/reports/generate", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["report-runs"] });
    },
  });
}

// --- Report Runs ---

export function useReportRuns() {
  return useQuery({
    queryKey: ["report-runs"],
    queryFn: () => apiClient.get<ReportRun[]>("/api/v1/reports/runs"),
  });
}

export function useReportRun(id: string) {
  return useQuery({
    queryKey: ["report-runs", id],
    queryFn: () => apiClient.get<ReportRun>(`/api/v1/reports/runs/${id}`),
    enabled: !!id,
  });
}

export function useReportRunHTML(id: string) {
  return useQuery({
    queryKey: ["report-runs", id, "html"],
    queryFn: async () => {
      const res = await fetch(`/api/v1/reports/runs/${id}/html`, {
        headers: {
          Authorization: `Bearer ${localStorage.getItem("access_token") ?? ""}`,
        },
      });
      if (!res.ok) throw new Error("Failed to fetch report HTML");
      return res.text();
    },
    enabled: !!id,
  });
}
