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
    queryFn: () => apiClient.get<LDAPConfig[]>("/api/v1/ldap/configs"),
  });
}

export function useLDAPConfig(id: string) {
  return useQuery({
    queryKey: ["ldap", "configs", id],
    queryFn: () => apiClient.get<LDAPConfig>(`/api/v1/ldap/configs/${id}`),
    enabled: !!id,
  });
}

export function useCreateLDAPConfig() {
  const qc = useQueryClient();
  return useMutation({
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
    mutationFn: (id: string) =>
      apiClient.delete(`/api/v1/ldap/configs/${id}`),
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
