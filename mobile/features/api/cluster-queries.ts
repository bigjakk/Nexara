import { useQuery } from "@tanstack/react-query";

import { apiGet } from "./api-client";
import { queryKeys } from "./query-keys";
import type { Cluster } from "./types";

export function useClusters() {
  return useQuery({
    queryKey: queryKeys.clusters(),
    queryFn: () => apiGet<Cluster[]>("/clusters"),
    staleTime: 30_000,
  });
}

export function useCluster(id: string | undefined) {
  return useQuery({
    queryKey: queryKeys.cluster(id ?? ""),
    queryFn: () => apiGet<Cluster>(`/clusters/${id ?? ""}`),
    enabled: Boolean(id),
    staleTime: 30_000,
  });
}
