import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  LDAPConfig,
  LDAPConfigRequest,
  LDAPTestResponse,
  LDAPSyncResponse,
} from "@/types/api";

export function useLDAPConfigs() {
  return useQuery({
    queryKey: ["ldap", "configs"],
    queryFn: () => apiClient.list<LDAPConfig>("/api/v1/ldap/configs"),
  });
}

export function useLDAPConfig(id: string) {
  return useQuery({
    queryKey: ["ldap", "configs", id],
    queryFn: () => apiClient.get<LDAPConfig>(`/api/v1/ldap/configs/${id}`),
    enabled: !!id,
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

export function useCreateLDAPConfig() {
  const qc = useQueryClient();
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: (data: LDAPConfigRequest) =>
      apiClient.post<LDAPConfig>("/api/v1/ldap/configs", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["ldap", "configs"] });
    },
  });
}

export function useUpdateLDAPConfig() {
  const qc = useQueryClient();
  return useMutation({
    ...errorsHandledLocally,
    mutationFn: ({ id, ...data }: LDAPConfigRequest & { id: string }) =>
      apiClient.put<LDAPConfig>(`/api/v1/ldap/configs/${id}`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["ldap", "configs"] });
    },
  });
}

export function useDeleteLDAPConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/ldap/configs/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["ldap", "configs"] });
    },
  });
}

export function useTestLDAPConnection() {
  return useMutation({
    mutationFn: ({
      id,
      test_username,
    }: {
      id: string;
      test_username?: string;
    }) =>
      apiClient.post<LDAPTestResponse>(`/api/v1/ldap/configs/${id}/test`, {
        test_username,
      }),
  });
}

export function useSyncLDAP() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post<LDAPSyncResponse>(`/api/v1/ldap/configs/${id}/sync`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["ldap", "configs"] });
      void qc.invalidateQueries({ queryKey: ["admin", "users"] });
    },
  });
}
