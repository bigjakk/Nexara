import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  APIKeyResponse,
  CreateAPIKeyRequest,
  CreateAPIKeyResponse,
} from "@/types/api";

export function useAPIKeys() {
  return useQuery({
    queryKey: ["api-keys"],
    queryFn: () => apiClient.get<APIKeyResponse[]>("/api/v1/api-keys"),
  });
}

export function useCreateAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateAPIKeyRequest) =>
      apiClient.post<CreateAPIKeyResponse>("/api/v1/api-keys", data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });
}

export function useRevokeAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/api-keys/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });
}

export function useRevokeAllAPIKeys() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiClient.delete("/api/v1/api-keys"),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });
}
