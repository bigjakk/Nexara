import { useEffect, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiFetch } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

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
  // Not under `all`: a write of any setting would read both files again.
  brandingFiles: ["branding-file"] as const,
};

export function useSettings(scope: string) {
  return useQuery({
    queryKey: settingsKeys.list(scope),
    queryFn: () =>
      apiClient.list<SettingResponse>(apiPath`/api/v1/settings?scope=${scope}`),
  });
}

export function useSetting(key: string, scope = "user") {
  return useQuery({
    queryKey: settingsKeys.detail(key, scope),
    queryFn: () =>
      apiClient.get<SettingResponse>(
        apiPath`/api/v1/settings/${key}?scope=${scope}`,
      ),
    retry: false,
  });
}

export function useBranding() {
  return useQuery({
    queryKey: settingsKeys.branding(),
    queryFn: () =>
      apiClient.get<BrandingSettings>(apiPath`/api/v1/settings/branding`),
    staleTime: 5 * 60 * 1000,
  });
}

export function useUpsertSetting() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key, ...payload }: UpsertSettingPayload & { key: string }) =>
      apiClient.put<SettingResponse>(apiPath`/api/v1/settings/${key}`, payload),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.all });
    },
  });
}

/**
 * Writes several settings as one save, so a failure is reported once (by the
 * app's global handler) rather than once per setting.
 */
export function useUpsertSettings() {
  const qc = useQueryClient();
  return useMutation({
    // Waits for every write before failing, so the refresh below cannot read
    // a setting whose write is still on its way and cache the old value.
    mutationFn: async (writes: (UpsertSettingPayload & { key: string })[]) => {
      const results = await Promise.allSettled(
        writes.map(({ key, ...payload }) =>
          apiClient.put<SettingResponse>(
            apiPath`/api/v1/settings/${key}`,
            payload,
          ),
        ),
      );
      const failed = results.find((r) => r.status === "rejected");
      if (failed) throw failed.reason;
      return results.map(
        (r) => (r as PromiseFulfilledResult<SettingResponse>).value,
      );
    },
    // Settled, not success: when one write fails the others may have landed.
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.all });
    },
  });
}

export function useDeleteSetting() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key, scope = "user" }: { key: string; scope?: string }) =>
      apiClient.delete(apiPath`/api/v1/settings/${key}?scope=${scope}`),
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
      const res = await apiFetch(apiPath`/api/v1/settings/branding/logo`, {
        method: "POST",
        body: formData,
        credentials: "same-origin",
      });
      if (!res.ok) throw new Error("Upload failed");
      return (await res.json()) as { logo_url: string; filename: string };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.branding() });
      void qc.invalidateQueries({ queryKey: settingsKeys.brandingFiles });
    },
  });
}

export function useUploadFavicon() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (file: File) => {
      const formData = new FormData();
      formData.append("favicon", file);
      const res = await apiFetch(apiPath`/api/v1/settings/branding/favicon`, {
        method: "POST",
        body: formData,
        credentials: "same-origin",
      });
      if (!res.ok) throw new Error("Upload failed");
      return (await res.json()) as { favicon_url: string; filename: string };
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: settingsKeys.branding() });
      void qc.invalidateQueries({ queryKey: settingsKeys.brandingFiles });
    },
  });
}

const brandingFiles = {
  logo: apiPath`/api/v1/settings/branding/logo-file`,
  favicon: apiPath`/api/v1/settings/branding/favicon-file`,
};

/**
 * An uploaded logo or favicon as a URL an <img> or the tab's icon link can
 * show, or null while there is none (`stored`, the setting, is null) or it is
 * loading. The API serves the file only with the session's bearer token,
 * which an <img> cannot send, so every custom logo was a broken image: it is
 * read through apiFetch and shown from a blob: URL, which the CSP allows.
 */
export function useBrandingImage(
  kind: keyof typeof brandingFiles,
  stored: string | null,
): string | null {
  const qc = useQueryClient();
  const { data } = useQuery({
    // An upload invalidates it (see useUploadLogo); a removal drops it below.
    queryKey: [...settingsKeys.brandingFiles, kind],
    queryFn: async () => {
      const res = await apiFetch(brandingFiles[kind], {
        credentials: "same-origin",
      });
      if (!res.ok) throw new Error(`Failed to load the ${kind}`);
      return res.blob();
    },
    enabled: stored !== null,
    // Read again after five minutes, as the settings are: one replaced in
    // another tab then shows here.
    staleTime: 5 * 60 * 1000,
  });
  // A removed image leaves the cache, so one uploaded after it is never shown
  // the old picture while the new one loads.
  useEffect(() => {
    if (stored === null) {
      qc.removeQueries({ queryKey: [...settingsKeys.brandingFiles, kind] });
    }
  }, [stored, kind, qc]);
  return useObjectUrl(stored === null ? undefined : data);
}

/** A blob: URL for `blob`, released when the blob changes or the caller unmounts. */
function useObjectUrl(blob: Blob | undefined): string | null {
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    if (!blob) {
      setUrl(null);
      return;
    }
    const next = URL.createObjectURL(blob);
    setUrl(next);
    return () => {
      URL.revokeObjectURL(next);
    };
  }, [blob]);
  return url;
}
