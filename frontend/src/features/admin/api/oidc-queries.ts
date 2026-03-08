import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  OIDCConfig,
  OIDCConfigRequest,
  OIDCTestResponse,
} from "@/types/api";

export function useOIDCConfigs() {
  return useQuery({
    queryKey: ["oidc", "configs"],
    queryFn: () => apiClient.get<OIDCConfig[]>("/api/v1/oidc/configs"),
  });
}

export function useCreateOIDCConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: OIDCConfigRequest) =>
      apiClient.post<OIDCConfig>("/api/v1/oidc/configs", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["oidc", "configs"] });
    },
  });
}

export function useUpdateOIDCConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: OIDCConfigRequest & { id: string }) =>
      apiClient.put<OIDCConfig>(`/api/v1/oidc/configs/${id}`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["oidc", "configs"] });
    },
  });
}

export function useDeleteOIDCConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(`/api/v1/oidc/configs/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["oidc", "configs"] });
    },
  });
}

export function useTestOIDCConnection() {
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.post<OIDCTestResponse>(`/api/v1/oidc/configs/${id}/test`),
  });
}
