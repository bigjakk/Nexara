import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, getValidAccessToken } from "@/lib/api-client";
import type { ReportSchedule, ReportRun, ReportParameters } from "@/types/api";

// --- Report Schedules ---

export function useReportSchedules() {
  return useQuery({
    queryKey: ["report-schedules"],
    queryFn: () => apiClient.list<ReportSchedule>("/api/v1/reports/schedules"),
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
  parameters: ReportParameters;
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

export interface GenerateReportRequest {
  report_type: string;
  cluster_id: string;
  time_range_hours: number;
  parameters?: ReportParameters | undefined;
}

export function useGenerateReport() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: GenerateReportRequest) =>
      apiClient.post<ReportRun>("/api/v1/reports/generate", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["report-runs"] });
    },
  });
}

// --- Report Runs ---

export function useReportRuns() {
  return useQuery({
    queryKey: ["report-runs"],
    queryFn: () => apiClient.list<ReportRun>("/api/v1/reports/runs"),
  });
}

export function useReportRun(id: string) {
  return useQuery({
    queryKey: ["report-runs", id],
    queryFn: () => apiClient.get<ReportRun>(`/api/v1/reports/runs/${id}`),
    enabled: !!id,
    // A run still generating is polled until it settles; a finished one is
    // not re-read.
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "pending" || status === "running" ? 3000 : false;
    },
  });
}

export function useDeleteReportRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/reports/runs/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["report-runs"] });
    },
  });
}

export interface EmailReportRunRequest {
  id: string;
  channel_id: string;
  recipients: string[];
  with_csv: boolean;
}

export function useEmailReportRun() {
  return useMutation({
    mutationFn: ({ id, ...body }: EmailReportRunRequest) =>
      apiClient.post<unknown>(`/api/v1/reports/runs/${id}/email`, body),
  });
}

/**
 * The rendered HTML and CSV are served as documents rather than JSON, and
 * only with a bearer token, so they bypass apiClient's JSON path. Kept here,
 * beside the other report calls, rather than in a component.
 */
async function fetchReportFile(id: string, kind: "html" | "csv") {
  const token = (await getValidAccessToken()) ?? "";
  const res = await fetch(`/api/v1/reports/runs/${id}/${kind}`, {
    headers: { Authorization: `Bearer ${token}` },
    credentials: "same-origin",
  });
  if (!res.ok) throw new Error(`Failed to fetch report ${kind.toUpperCase()}`);
  return res;
}

export function useReportRunHTML(id: string) {
  return useQuery({
    queryKey: ["report-runs", id, "html"],
    queryFn: async () => (await fetchReportFile(id, "html")).text(),
    enabled: !!id,
  });
}

/** Saves a run's HTML or CSV through the browser's download path. */
export async function downloadReportRun(
  run: ReportRun,
  kind: "html" | "csv",
  filename: string,
) {
  const res = await fetchReportFile(run.id, kind);
  const blob = await res.blob();
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = filename;
  link.click();
  URL.revokeObjectURL(link.href);
}
