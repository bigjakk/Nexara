/**
 * Storage pool query hooks. Same pattern as `node-queries.ts`:
 *
 *   - `useClusterStoragePools(clusterId)` fetches the full list
 *   - `useStoragePool(clusterId, storageId)` is a client-side filter
 *     over the list (no backend `GET /clusters/:cid/storage/:sid`
 *     endpoint exists — adding one would be a small backend change
 *     but isn't necessary given the usual cache flow)
 *
 * The list endpoint returns one row per (node × storage name) tuple.
 * Shared storages appear once per node with typically identical
 * capacity numbers; non-shared storages have per-node capacities.
 * The detail screen renders whatever row the user tapped — no client-
 * side cross-row dedup.
 */

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

import { apiGet } from "./api-client";
import { queryKeys } from "./query-keys";
import type { StorageContentItem, StoragePool } from "./types";

export function useClusterStoragePools(clusterId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.clusterStorage(clusterId ?? ""),
    queryFn: () =>
      apiGet<StoragePool[]>(`/clusters/${clusterId ?? ""}/storage`),
    enabled: Boolean(clusterId),
    staleTime: 10_000,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });
}

export function useStoragePool(
  clusterId: string | undefined,
  storageId: string | undefined,
) {
  const list = useClusterStoragePools(clusterId);

  const pool = useMemo<StoragePool | undefined>(() => {
    if (!list.data || !storageId) return undefined;
    return list.data.find((p) => p.id === storageId);
  }, [list.data, storageId]);

  return {
    data: pool,
    isLoading: list.isLoading,
    isError: list.isError,
    error: list.error,
    refetch: list.refetch,
  };
}

/**
 * Fetch the content of a single storage pool (ISOs, templates, backups,
 * disk images, etc.). The backend proxies this live to Proxmox on every
 * call — there's no DB cache — so it can be slow on storages with
 * hundreds of items, and it can fail if the storage is offline.
 *
 * `enabled` is gated by both inputs being present so the query stays
 * idle until the storage detail screen has resolved its route params.
 *
 * No auto-refetch interval — content lists rarely churn at sub-minute
 * granularity, and the live WS metric channel doesn't carry content
 * updates. Pull-to-refresh on the detail screen is the canonical way
 * to refresh.
 */
export function useStorageContent(
  clusterId: string | undefined,
  storageId: string | undefined,
) {
  return useQuery({
    queryKey: queryKeys.storageContent(clusterId ?? "", storageId ?? ""),
    queryFn: () =>
      apiGet<StorageContentItem[]>(
        `/clusters/${clusterId ?? ""}/storage/${storageId ?? ""}/content`,
      ),
    enabled: Boolean(clusterId && storageId),
    staleTime: 30_000,
  });
}
