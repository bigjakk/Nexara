import { useQuery } from "@tanstack/react-query";

import { apiGet } from "./api-client";
import { queryKeys } from "./query-keys";
import type { VM } from "./types";

export function useClusterVMs(clusterId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.clusterVMs(clusterId ?? ""),
    queryFn: () => apiGet<VM[]>(`/clusters/${clusterId ?? ""}/vms`),
    enabled: Boolean(clusterId),
    staleTime: 10_000,
    refetchInterval: 15_000,
    refetchIntervalInBackground: false,
  });
}

export function useClusterContainers(clusterId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.clusterContainers(clusterId ?? ""),
    queryFn: () => apiGet<VM[]>(`/clusters/${clusterId ?? ""}/containers`),
    enabled: Boolean(clusterId),
    staleTime: 10_000,
    refetchInterval: 15_000,
    refetchIntervalInBackground: false,
  });
}

/**
 * Convenience hook that returns all guests (VMs + containers) for a cluster.
 *
 * Implementation note: the backend's `/clusters/:id/vms` endpoint already
 * returns ALL guests regardless of type (the SQL query `ListVMsByCluster`
 * has no `WHERE type` filter — see `queries/vms.sql:20-21`). The
 * `/containers` endpoint is a filtered subset of the same table. So we just
 * fetch /vms and sort. Calling both and merging would double-count
 * containers (which is the bug we hit during M2 testing — duplicate React
 * keys on the cluster detail screen).
 */
export function useClusterGuests(clusterId: string | undefined) {
  const vms = useClusterVMs(clusterId);

  const data: VM[] | undefined = vms.data
    ? [...vms.data].sort((a, b) => a.vmid - b.vmid)
    : undefined;

  return {
    data,
    isLoading: vms.isLoading,
    isError: vms.isError,
    error: vms.error,
    refetch: vms.refetch,
  };
}

export function useVM(
  clusterId: string | undefined,
  vmId: string | undefined,
  type: "qemu" | "lxc",
) {
  const path = type === "lxc" ? "containers" : "vms";
  return useQuery({
    queryKey: queryKeys.vm(clusterId ?? "", vmId ?? ""),
    queryFn: () =>
      apiGet<VM>(`/clusters/${clusterId ?? ""}/${path}/${vmId ?? ""}`),
    enabled: Boolean(clusterId && vmId),
    staleTime: 5_000,
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
  });
}
