/**
 * TopConsumers — dashboard widget showing the top 10 VMs by CPU% across
 * every cluster the user can see.
 *
 * How it joins data:
 *
 *   1. The dashboard already calls `useClustersLiveMetrics(clusterIds)`
 *      which subscribes to every cluster's `cluster:<id>:metrics` WS
 *      channel. That hook also populates the per-VM map in the metric
 *      store via `processMessage()`. Each entry is keyed by VM row UUID
 *      and holds the live `cpuPercent` / `memPercent` / mem totals.
 *
 *   2. Live metric data has no VM name / cluster / type — those come
 *      from the `/clusters/:id/vms` endpoint. We use TanStack Query's
 *      `useQueries` to fetch the VM list for every visible cluster in
 *      parallel. The query keys match `useClusterVMs` so the cache is
 *      shared with any cluster detail screen the user has visited.
 *
 *   3. We build a `vmIndex` lookup keyed by VM row UUID that maps to
 *      the VM record + its cluster id/name. Joining the live metric
 *      store with this index gives us the data needed for the rows.
 *
 *   4. Templates are filtered out — Proxmox doesn't report metrics for
 *      templates anyway, but the filter is defense-in-depth so a bug
 *      in a future Proxmox version doesn't surface a stopped template
 *      at the top of the list.
 *
 *   5. Sort by CPU% desc, slice to 10. Render a clickable row per VM.
 *
 * Why a separate component (rather than inlining in the dashboard):
 * the join logic + the per-cluster `useQueries` call + the memoization
 * adds up to ~80 LoC. Splitting it out keeps the dashboard's main file
 * focused on layout.
 */

import { useMemo } from "react";
import {
  ActivityIndicator,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { useRouter } from "expo-router";
import { useQueries } from "@tanstack/react-query";
import { Activity } from "lucide-react-native";

import { apiGet } from "@/features/api/api-client";
import { queryKeys } from "@/features/api/query-keys";
import type { Cluster, VM } from "@/features/api/types";
import { useMetricStore } from "@/stores/metric-store";
import {
  type TopConsumerRow,
  joinTopConsumers,
} from "./top-consumers-join";

export function TopConsumers({ clusters }: { clusters: Cluster[] }) {
  const router = useRouter();

  // Fetch VMs for every visible cluster in parallel. The query keys
  // match `useClusterVMs` so the cache is shared with cluster detail
  // screens that may already have populated it. Disabled when there
  // are no clusters yet (cold load on the dashboard).
  const guestsQueries = useQueries({
    queries: clusters.map((c) => ({
      queryKey: queryKeys.clusterVMs(c.id),
      queryFn: () => apiGet<VM[]>(`/clusters/${c.id}/vms`),
      enabled: clusters.length > 0,
      staleTime: 30_000,
    })),
  });

  // Read live VM metrics from the store. Subscribing to `version` forces
  // re-render whenever the store mutates the Maps in place; the actual
  // read happens via getState() inside the memo so we always see fresh
  // data.
  const version = useMetricStore((s) => s.version);

  // Stable dep key — without it the parallel array of VM lists (a fresh
  // array reference every render) would re-trigger the join even when
  // nothing actually changed.
  const dataUpdatedKey = guestsQueries
    .map((q) => q.dataUpdatedAt)
    .join(",");

  const top = useMemo<TopConsumerRow[]>(() => {
    void version;
    const liveVMs = useMetricStore.getState().vms;
    const vmsByCluster = guestsQueries.map((q) => q.data);
    return joinTopConsumers(clusters, vmsByCluster, liveVMs);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusters, dataUpdatedKey, version]);

  // Loading state: at least one cluster's VM list still in flight, no
  // joined rows yet to show. Skip the spinner if the user has no clusters
  // configured (a separate empty state, handled by the dashboard's
  // cluster section).
  const anyLoading = guestsQueries.some((q) => q.isLoading);
  const showLoading = anyLoading && top.length === 0 && clusters.length > 0;

  // Empty state: queries done, live metrics haven't reported anyone with
  // CPU usage yet (or every running VM has 0% CPU, which would be unusual).
  // Hide the entire section in the no-clusters case to avoid a confusing
  // "No active VMs" message before any clusters are even configured.
  if (clusters.length === 0) return null;

  return (
    <View>
      {showLoading ? (
        <View className="items-center rounded-lg border border-border bg-card p-6">
          <ActivityIndicator color="#22c55e" />
        </View>
      ) : top.length === 0 ? (
        <View className="items-center rounded-lg border border-border bg-card p-6">
          <Activity color="#71717a" size={24} />
          <Text className="mt-2 text-center text-xs text-muted-foreground">
            No active VMs reporting metrics yet. Waiting for the next
            collector cycle…
          </Text>
        </View>
      ) : (
        <View className="overflow-hidden rounded-lg border border-border bg-card">
          {top.map((row, idx) => {
            const last = idx === top.length - 1;
            return (
              <TouchableOpacity
                key={row.vm.id}
                onPress={() => {
                  router.push({
                    pathname: "/(app)/clusters/[id]/vms/[vmId]",
                    params: {
                      id: row.clusterId,
                      vmId: row.vm.id,
                      type: row.vm.type,
                    },
                  });
                }}
                activeOpacity={0.7}
                className={`px-4 py-3 ${last ? "" : "border-b border-border"}`}
              >
                <View className="flex-row items-center gap-3">
                  <View className="w-6 items-center">
                    <Text className="text-[11px] font-bold text-muted-foreground">
                      {idx + 1}
                    </Text>
                  </View>
                  <View className="flex-1">
                    <Text
                      className="text-sm font-medium text-foreground"
                      numberOfLines={1}
                    >
                      {row.vm.name || `vm-${String(row.vm.vmid)}`}
                    </Text>
                    <Text
                      className="mt-0.5 text-[11px] text-muted-foreground"
                      numberOfLines={1}
                    >
                      {row.clusterName} · {row.vm.type === "lxc" ? "ct" : "vm"}{" "}
                      {row.vm.vmid}
                    </Text>
                  </View>
                  <View className="items-end">
                    <Text className="text-sm font-semibold text-foreground">
                      {row.cpuPercent.toFixed(1)}%
                    </Text>
                    <Text className="text-[10px] text-muted-foreground">
                      MEM {row.memPercent.toFixed(0)}%
                    </Text>
                  </View>
                </View>
              </TouchableOpacity>
            );
          })}
        </View>
      )}
    </View>
  );
}
