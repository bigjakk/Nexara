import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
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
        apiPath`/api/v1/clusters/${clusterId}/drs/config`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useUpdateDRSConfig(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (config: DRSConfigRequest) =>
      apiClient.put<DRSConfig>(
        apiPath`/api/v1/clusters/${clusterId}/drs/config`,
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
      apiClient.list<DRSRule>(apiPath`/api/v1/clusters/${clusterId}/drs/rules`),
    enabled: clusterId.length > 0,
  });
}

export function useCreateDRSRule(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rule: CreateRuleRequest) =>
      apiClient.post<DRSRule>(
        apiPath`/api/v1/clusters/${clusterId}/drs/rules`,
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
        apiPath`/api/v1/clusters/${clusterId}/drs/rules/${ruleId}`,
      ),
    // onSettled, not onSuccess: deleting a rule that is already gone answers
    // 404, and the list that offered it is the stale part — refetching it on
    // the failure too is what makes the row disappear. There is deliberately
    // no onError here, so the app-wide handler still toasts the 404.
    onSettled: () => {
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
      apiClient.list<DRSRule>(
        apiPath`/api/v1/clusters/${clusterId}/drs/ha-rules`,
      ),
    enabled: clusterId.length > 0,
  });
}

export function useCreateHARule(clusterId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (rule: CreateHARuleRequest) =>
      apiClient.post<{ status: string; rule_name: string }>(
        apiPath`/api/v1/clusters/${clusterId}/drs/ha-rules`,
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
        apiPath`/api/v1/clusters/${clusterId}/drs/ha-rules/${ruleName}`,
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
        apiPath`/api/v1/clusters/${clusterId}/drs/evaluate`,
      ),
  });
}

export function useDRSHistory(clusterId: string, limit: number = 25) {
  return useQuery({
    queryKey: ["drs", "history", clusterId, limit],
    queryFn: () =>
      apiClient.list<DRSHistoryEntry>(
        apiPath`/api/v1/clusters/${clusterId}/drs/history?limit=${limit}`,
      ),
    enabled: clusterId.length > 0,
  });
}
