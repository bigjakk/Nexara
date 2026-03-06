import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type {
  DRSConfig,
  DRSConfigRequest,
  DRSRule,
  CreateRuleRequest,
  CreateHARuleRequest,
  EvaluateResponse,
  DRSHistoryEntry,
} from "../types/drs";

export function useDRSConfig(clusterId: string) {
  return useQuery({
    queryKey: ["drs", "config", clusterId],
    queryFn: () =>
      apiClient.get<DRSConfig>(
        `/api/v1/clusters/${clusterId}/drs/config`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useUpdateDRSConfig(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (config: DRSConfigRequest) =>
      apiClient.put<DRSConfig>(
        `/api/v1/clusters/${clusterId}/drs/config`,
        config,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["drs", "config", clusterId],
      });
    },
  });
}

export function useDRSRules(clusterId: string) {
  return useQuery({
    queryKey: ["drs", "rules", clusterId],
    queryFn: () =>
      apiClient.get<DRSRule[]>(
        `/api/v1/clusters/${clusterId}/drs/rules`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateDRSRule(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rule: CreateRuleRequest) =>
      apiClient.post<DRSRule>(
        `/api/v1/clusters/${clusterId}/drs/rules`,
        rule,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["drs", "rules", clusterId],
      });
    },
  });
}

export function useDeleteDRSRule(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (ruleId: string) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/drs/rules/${ruleId}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["drs", "rules", clusterId],
      });
    },
  });
}

export function useHARules(clusterId: string) {
  return useQuery({
    queryKey: ["drs", "ha-rules", clusterId],
    queryFn: () =>
      apiClient.get<DRSRule[]>(
        `/api/v1/clusters/${clusterId}/drs/ha-rules`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateHARule(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rule: CreateHARuleRequest) =>
      apiClient.post<{ status: string; rule_name: string }>(
        `/api/v1/clusters/${clusterId}/drs/ha-rules`,
        rule,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["drs", "ha-rules", clusterId],
      });
    },
  });
}

export function useDeleteHARule(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (ruleName: string) =>
      apiClient.delete<{ status: string }>(
        `/api/v1/clusters/${clusterId}/drs/ha-rules/${ruleName}`,
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["drs", "ha-rules", clusterId],
      });
    },
  });
}

export function useTriggerEvaluation(clusterId: string) {
  return useMutation({
    mutationFn: () =>
      apiClient.post<EvaluateResponse>(
        `/api/v1/clusters/${clusterId}/drs/evaluate`,
      ),
  });
}

export function useDRSHistory(clusterId: string, limit: number = 25) {
  return useQuery({
    queryKey: ["drs", "history", clusterId, limit],
    queryFn: () =>
      apiClient.get<DRSHistoryEntry[]>(
        `/api/v1/clusters/${clusterId}/drs/history?limit=${limit}`,
      ),
    enabled: clusterId.length > 0,
  });
}
