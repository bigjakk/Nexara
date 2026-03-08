import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";

export interface SettingResponse {
  id: string;
  key: string;
  value: unknown;
  scope: string;
  scope_id?: string;
  created_at: string;
  updated_at: string;
}

interface UpsertSettingPayload {
  value: unknown;
  scope?: string;
}

export type BrandingSettings = Record<string, unknown>;

export const settingsKeys = {
  all: ["settings"] as const,
  list: (scope: string) => [...settingsKeys.all, "list", scope] as const,
  detail: (key: string, scope: string) =>
    [...settingsKeys.all, "detail", key, scope] as const,
  branding: () => [...settingsKeys.all, "branding"] as const,
};

export function useSettings(scope: string) {
  return useQuery({
    queryKey: settingsKeys.list(scope),
    queryFn: () =>
      apiClient.get<SettingResponse[]>(
        `/api/v1/settings?scope=${encodeURIComponent(scope)}`,
      ),
  });
}

export function useSetting(key: string, scope = "user") {
  return useQuery({
    queryKey: settingsKeys.detail(key, scope),
    queryFn: () =>
      apiClient.get<SettingResponse>(
        `/api/v1/settings/${encodeURIComponent(key)}?scope=${encodeURIComponent(scope)}`,
      ),
    retry: false,
  });
}

export function useBranding() {
  return useQuery({
    queryKey: settingsKeys.branding(),
    queryFn: () => apiClient.get<BrandingSettings>("/api/v1/settings/branding"),
    staleTime: 5 * 60 * 1000,
  });
}

export function useUpsertSetting() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      key,
      ...payload
    }: UpsertSettingPayload & { key: string }) =>
      apiClient.put<SettingResponse>(
        `/api/v1/settings/${encodeURIComponent(key)}`,
        payload,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.all });
    },
  });
}

export function useDeleteSetting() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key, scope = "user" }: { key: string; scope?: string }) =>
      apiClient.delete(
        `/api/v1/settings/${encodeURIComponent(key)}?scope=${encodeURIComponent(scope)}`,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.all });
    },
  });
}

export function useUploadLogo() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (file: File) => {
      const formData = new FormData();
      formData.append("logo", file);
      const token = localStorage.getItem("access_token");
      const res = await fetch("/api/v1/settings/branding/logo", {
        method: "POST",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
        body: formData,
      });
      if (!res.ok) throw new Error("Upload failed");
      return (await res.json()) as { logo_url: string; filename: string };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.branding() });
    },
  });
}

export function useUploadFavicon() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (file: File) => {
      const formData = new FormData();
      formData.append("favicon", file);
      const token = localStorage.getItem("access_token");
      const res = await fetch("/api/v1/settings/branding/favicon", {
        method: "POST",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
        body: formData,
      });
      if (!res.ok) throw new Error("Upload failed");
      return (await res.json()) as { favicon_url: string; filename: string };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.branding() });
    },
  });
}
