import { useCallback, useState } from "react";
import {
  ActivityIndicator,
  RefreshControl,
  ScrollView,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Stack, useLocalSearchParams, useRouter } from "expo-router";
import { Boxes, Cpu, Database, HardDrive, Layers } from "lucide-react-native";

import { useCluster } from "@/features/api/cluster-queries";
import { useClusterNodes } from "@/features/api/node-queries";
import { useClusterStoragePools } from "@/features/api/storage-queries";
import { useClusterGuests } from "@/features/api/vm-queries";
import type { Node, StoragePool, VM } from "@/features/api/types";
import { useClusterLiveMetrics } from "@/hooks/useClusterLiveMetrics";
import { ListError } from "@/components/ListEmpty";
import { StatusPill, statusToneFor } from "@/components/StatusPill";
import { SectionHeader } from "@/components/detail-screen";
import { formatBytes, formatUptime } from "@/lib/format";

export default function ClusterDetailScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const router = useRouter();
  const cluster = useCluster(id);
  const nodes = useClusterNodes(id);
  const guests = useClusterGuests(id);
  const storage = useClusterStoragePools(id);
  // Subscribe to live metrics for this cluster. The hook also populates
  // per-VM and per-node entries in the metric store, which the VM and
  // node detail screens read via useLiveVM/useLiveNode without needing
  // to re-subscribe.
  const liveMetrics = useClusterLiveMetrics(id);

  const [refreshing, setRefreshing] = useState(false);
  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await Promise.all([
        cluster.refetch(),
        nodes.refetch(),
        guests.refetch(),
        storage.refetch(),
      ]);
    } finally {
      setRefreshing(false);
    }
  }, [cluster, nodes, guests, storage]);

  if (cluster.isLoading && !cluster.data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator color="#22c55e" />
        </View>
      </SafeAreaView>
    );
  }

  if (cluster.isError || !cluster.data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <ListError
          detail={
            cluster.error instanceof Error
              ? cluster.error.message
              : "Cluster not found"
          }
        />
      </SafeAreaView>
    );
  }

  const onlineNodes = nodes.data?.filter((n) => n.status === "online").length ?? 0;
  const runningGuests =
    guests.data?.filter((g) => g.status === "running").length ?? 0;

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Stack.Screen options={{ title: cluster.data.name }} />
      <ScrollView
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={onRefresh}
            tintColor="#22c55e"
          />
        }
      >
        <View className="p-4">
          {/* Header card */}
          <View className="rounded-lg border border-border bg-card p-4">
            <View className="flex-row items-start justify-between">
              <View className="flex-1">
                <Text className="text-xl font-bold text-foreground">
                  {cluster.data.name}
                </Text>
                <Text className="mt-1 text-xs text-muted-foreground">
                  {cluster.data.api_url}
                </Text>
              </View>
              <StatusPill
                label={cluster.data.status}
                tone={statusToneFor(cluster.data.status)}
              />
            </View>
            <View className="mt-4 flex-row gap-3">
              <SummaryStat
                icon={<Layers color="#22c55e" size={18} />}
                label="Nodes"
                value={`${onlineNodes}/${nodes.data?.length ?? 0}`}
              />
              <SummaryStat
                icon={<Boxes color="#22c55e" size={18} />}
                label="Guests"
                value={`${runningGuests}/${guests.data?.length ?? 0}`}
              />
            </View>
            {/*
              Live aggregated CPU + memory across all nodes in the cluster.
              Updates every ~10s as new collector snapshots arrive on the
              cluster:<id>:metrics WS channel. The first WS message arrives
              shortly after the cluster detail screen mounts; before that
              the section is hidden so the layout doesn't jump.
            */}
            {liveMetrics ? (
              <View className="mt-3 flex-row gap-3">
                <SummaryStat
                  icon={<Cpu color="#22c55e" size={18} />}
                  label="CPU"
                  value={`${liveMetrics.cpuPercent.toFixed(1)}%`}
                />
                <SummaryStat
                  icon={<Boxes color="#22c55e" size={18} />}
                  label="Memory"
                  value={`${liveMetrics.memPercent.toFixed(1)}%`}
                />
              </View>
            ) : null}
          </View>

          {/* Nodes section */}
          <SectionHeader title="Nodes" />
          {nodes.isLoading && !nodes.data ? (
            <ActivityIndicator color="#22c55e" />
          ) : nodes.data && nodes.data.length > 0 ? (
            <View className="gap-2">
              {nodes.data.map((node) => (
                <NodeRow
                  key={node.id}
                  node={node}
                  onPress={() =>
                    router.push(`/(app)/clusters/${id}/nodes/${node.id}`)
                  }
                />
              ))}
            </View>
          ) : (
            <Text className="text-sm text-muted-foreground">No nodes</Text>
          )}

          {/* Guests section */}
          <SectionHeader title="VMs & Containers" />
          {guests.isLoading && !guests.data ? (
            <ActivityIndicator color="#22c55e" />
          ) : guests.data && guests.data.length > 0 ? (
            <View className="gap-2">
              {guests.data.map((g) => (
                <GuestRow
                  key={g.id}
                  guest={g}
                  onPress={() =>
                    router.push(`/(app)/clusters/${id}/vms/${g.id}?type=${g.type}`)
                  }
                />
              ))}
            </View>
          ) : (
            <Text className="text-sm text-muted-foreground">No guests</Text>
          )}

          {/* Storage section */}
          <SectionHeader title="Storage" />
          {storage.isLoading && !storage.data ? (
            <ActivityIndicator color="#22c55e" />
          ) : storage.data && storage.data.length > 0 ? (
            <View className="gap-2">
              {dedupStoragePools(storage.data).map((p) => (
                <StorageRow
                  key={p.id}
                  pool={p}
                  nodeName={
                    nodes.data?.find((n) => n.id === p.node_id)?.name
                  }
                  onPress={() =>
                    router.push(
                      `/(app)/clusters/${id ?? ""}/storage/${p.id}`,
                    )
                  }
                />
              ))}
            </View>
          ) : (
            <Text className="text-sm text-muted-foreground">
              No storage pools
            </Text>
          )}
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

/**
 * Dedup the backend's per-(storage,node) rows for cluster-level display:
 *   - Shared storages (NFS, Ceph, etc.) collapse to one row per storage
 *     name, keyed by the first occurrence. Capacity numbers are identical
 *     across all shared-storage rows so picking any one is fine.
 *   - Non-shared storages (LVM, local disk) stay as per-node rows because
 *     each node has its own distinct local capacity.
 *
 * Mirrors `frontend/src/features/storage/pages/StoragePage.tsx::dedupe`
 * so the cluster overview matches the desktop behavior.
 */
function dedupStoragePools(pools: StoragePool[]): StoragePool[] {
  const sharedSeen = new Map<string, StoragePool>();
  const nonShared: StoragePool[] = [];
  for (const p of pools) {
    if (p.shared) {
      if (!sharedSeen.has(p.storage)) {
        sharedSeen.set(p.storage, p);
      }
    } else {
      nonShared.push(p);
    }
  }
  return [...sharedSeen.values(), ...nonShared];
}

function SummaryStat({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
}) {
  return (
    <View className="flex-1 rounded border border-border bg-background p-3">
      <View className="flex-row items-center gap-2">
        {icon}
        <Text className="text-xs text-muted-foreground">{label}</Text>
      </View>
      <Text className="mt-1 text-lg font-semibold text-foreground">{value}</Text>
    </View>
  );
}

function NodeRow({ node, onPress }: { node: Node; onPress: () => void }) {
  return (
    <TouchableOpacity
      onPress={onPress}
      className="rounded-lg border border-border bg-card p-3"
    >
      <View className="flex-row items-center justify-between">
        <View className="flex-1">
          <Text className="text-base font-semibold text-foreground">
            {node.name}
          </Text>
          <Text className="mt-0.5 text-[11px] text-muted-foreground">
            {node.cpu_count} cores · {formatBytes(node.mem_total)} RAM ·
            up {formatUptime(node.uptime)}
          </Text>
        </View>
        <StatusPill label={node.status} tone={statusToneFor(node.status)} />
      </View>
    </TouchableOpacity>
  );
}

function StorageRow({
  pool,
  nodeName,
  onPress,
}: {
  pool: StoragePool;
  nodeName: string | undefined;
  onPress: () => void;
}) {
  const usagePct =
    pool.total > 0 ? Math.round((pool.used / pool.total) * 100) : 0;
  const contextLine = pool.shared
    ? `${pool.type} · shared`
    : nodeName
      ? `${pool.type} · ${nodeName}`
      : pool.type;
  const barColor =
    usagePct >= 90 ? "#ef4444" : usagePct >= 75 ? "#f59e0b" : "#22c55e";
  return (
    <TouchableOpacity
      onPress={onPress}
      activeOpacity={0.7}
      className="rounded-lg border border-border bg-card p-3"
    >
      <View className="flex-row items-center gap-3">
        <View className="h-9 w-9 items-center justify-center rounded bg-muted">
          <Database color="#a1a1aa" size={18} />
        </View>
        <View className="flex-1">
          <Text className="text-base font-semibold text-foreground">
            {pool.storage}
          </Text>
          <Text className="mt-0.5 text-[11px] text-muted-foreground">
            {contextLine}
          </Text>
          {pool.total > 0 ? (
            <View className="mt-2 flex-row items-center gap-2">
              <View className="h-1 flex-1 overflow-hidden rounded-full bg-muted">
                <View
                  className="h-full"
                  style={{
                    width: (`${usagePct}%` as const),
                    backgroundColor: barColor,
                  }}
                />
              </View>
              <Text className="text-[11px] text-muted-foreground">
                {formatBytes(pool.used)} / {formatBytes(pool.total)}
              </Text>
            </View>
          ) : null}
        </View>
      </View>
    </TouchableOpacity>
  );
}

function GuestRow({ guest, onPress }: { guest: VM; onPress: () => void }) {
  const icon =
    guest.type === "lxc" ? (
      <HardDrive color="#a1a1aa" size={18} />
    ) : (
      <Cpu color="#a1a1aa" size={18} />
    );
  return (
    <TouchableOpacity
      onPress={onPress}
      activeOpacity={0.7}
      className="rounded-lg border border-border bg-card p-3"
    >
      <View className="flex-row items-center gap-3">
        <View className="h-9 w-9 items-center justify-center rounded bg-muted">
          {icon}
        </View>
        <View className="flex-1">
          <View className="flex-row items-center gap-2">
            <Text className="text-base font-semibold text-foreground">
              {guest.name || `vm-${guest.vmid}`}
            </Text>
            <Text className="text-[11px] text-muted-foreground">#{guest.vmid}</Text>
          </View>
          <Text className="mt-0.5 text-[11px] text-muted-foreground">
            {guest.type.toUpperCase()} · {guest.cpu_count} vCPU ·{" "}
            {formatBytes(guest.mem_total)}
          </Text>
        </View>
        <StatusPill label={guest.status} tone={statusToneFor(guest.status)} />
      </View>
    </TouchableOpacity>
  );
}
