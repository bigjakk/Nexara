import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type {
  APIKeyResponse,
  CreateAPIKeyRequest,
  CreateAPIKeyResponse,
} from "@/types/api";

export function useAPIKeys() {
  return useQuery({
    queryKey: ["api-keys"],
    queryFn: () => apiClient.list<APIKeyResponse>(apiPath`/api/v1/api-keys`),
  });
}

export function useCreateAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateAPIKeyRequest) =>
      apiClient.post<CreateAPIKeyResponse>(apiPath`/api/v1/api-keys`, data),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });
}

export function useRevokeAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiClient.delete(apiPath`/api/v1/api-keys/${id}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });
}

export function useRevokeAllAPIKeys() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiClient.delete(apiPath`/api/v1/api-keys`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
  });
}
