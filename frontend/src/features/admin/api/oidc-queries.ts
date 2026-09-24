import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type {
  OIDCConfig,
  OIDCConfigRequest,
  OIDCTestResponse,
} from "@/types/api";

export function useOIDCConfigs() {
  return useQuery({
    queryKey: ["oidc", "configs"],
    queryFn: () => apiClient.list<OIDCConfig>(apiPath`/api/v1/oidc/configs`),
  });
}

/**
 * Opts a mutation out of the global error toast in lib/query-client.ts.
 *
 * That toast is a safety net for mutations with no error handling of their own.
 * It checks `mutation.options.onError`, which only sees callbacks given to
 * useMutation — not the per-call ones passed to mutate(). So a mutation whose
 * component already renders the failure gets it reported twice without this,
 * and a confirm-required 422 reads as a hard red failure next to the amber
 * prompt offering to proceed.
 *
 * Apply it ONLY where the component surfaces the error somewhere the operator
 * is looking at the moment it happens — an open dialog, or a banner in the
 * section body. Applying it to a mutation that relies on the toast makes the
 * failure silent, which is strictly worse than reporting it twice.
 */
const errorsHandledLocally = { onError: () => undefined };

export function useCreateOIDCConfig() {
  const qc = useQueryClient();
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: (data: OIDCConfigRequest) =>
      apiClient.post<OIDCConfig>(apiPath`/api/v1/oidc/configs`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["oidc", "configs"] });
    },
  });
}

export function useUpdateOIDCConfig() {
  const qc = useQueryClient();
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: ({ id, ...data }: OIDCConfigRequest & { id: string }) =>
      apiClient.put<OIDCConfig>(apiPath`/api/v1/oidc/configs/${id}`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["oidc", "configs"] });
    },
  });
}

export function useDeleteOIDCConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(apiPath`/api/v1/oidc/configs/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["oidc", "configs"] });
    },
  });
}

export function useTestOIDCConnection() {
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post<OIDCTestResponse>(
        apiPath`/api/v1/oidc/configs/${id}/test`,
      ),
  });
}
