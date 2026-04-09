import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

import { apiGet } from "./api-client";
import { queryKeys } from "./query-keys";
import type { Node } from "./types";

export function useClusterNodes(clusterId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.clusterNodes(clusterId ?? ""),
    queryFn: () => apiGet<Node[]>(`/clusters/${clusterId ?? ""}/nodes`),
    enabled: Boolean(clusterId),
    staleTime: 10_000,
    refetchInterval: 15_000,
    refetchIntervalInBackground: false,
  });
}

/**
 * Convenience hook that returns a single node from a cluster.
 *
 * Implementation note: the backend has no `GET /api/v1/clusters/:cluster_id/nodes/:node_id`
 * endpoint — only the list. Adding one would be a small backend change but
 * it's not necessary for v1 mobile use cases. Instead we reuse the cluster
 * nodes list query (which is usually already in the cache because the user
 * just came from the cluster detail screen) and filter client-side.
 *
 * Benefits of this approach:
 *   - No backend changes
 *   - Cache reuse — instant render if the user navigated from cluster detail
 *   - Consistent invalidation — node list updates from WS events refresh
 *     this hook for free
 *
 * Trade-offs:
 *   - Fetches the entire node list even when we only need one (negligible
 *     for fleets with <100 nodes per cluster)
 *   - The returned `data` is a wrapper that mirrors the underlying query's
 *     status fields (`isLoading`, `isError`, etc.) but with `data` narrowed
 *     to a single Node | undefined
 */
export function useNode(
  clusterId: string | undefined,
  nodeId: string | undefined,
) {
  const list = useClusterNodes(clusterId);

  const node = useMemo<Node | undefined>(() => {
    if (!list.data || !nodeId) return undefined;
    return list.data.find((n) => n.id === nodeId);
  }, [list.data, nodeId]);

  return {
    data: node,
    isLoading: list.isLoading,
    isError: list.isError,
    error: list.error,
    refetch: list.refetch,
  };
}
