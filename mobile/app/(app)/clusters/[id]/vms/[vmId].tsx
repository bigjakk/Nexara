import { useCallback, useMemo, useState } from "react";
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
import { Monitor } from "lucide-react-native";

import { useVM } from "@/features/api/vm-queries";
import { useVMMetrics } from "@/features/api/metric-queries";
import { useClusterNodes } from "@/features/api/node-queries";
import type { VMType } from "@/features/api/types";
import {
  useClusterLiveMetrics,
  useLiveVM,
} from "@/hooks/useClusterLiveMetrics";
import { ListError } from "@/components/ListEmpty";
import { StatusPill, statusToneFor } from "@/components/StatusPill";
import { PowerActions } from "@/components/PowerActions";
import {
  LiveStat,
  MetricsCard,
  Row,
  SectionHeader,
} from "@/components/detail-screen";
import { SnapshotsSection } from "@/features/snapshots/SnapshotsSection";
import { formatBytes, formatRelative, formatUptime } from "@/lib/format";

export default function VMDetailScreen() {
  const params = useLocalSearchParams<{
    id: string;
    vmId: string;
    type?: string;
  }>();
  const clusterId = params.id;
  const vmId = params.vmId;
  const type: VMType = params.type === "lxc" ? "lxc" : "qemu";

  const router = useRouter();
  const vm = useVM(clusterId, vmId, type);
  const metrics = useVMMetrics(clusterId, vmId, "1h");
  const nodes = useClusterNodes(clusterId);
  // Subscribe to live metrics for the parent cluster. The hook populates
  // every VM and node entry in the metric store, so the per-VM selector
  // below picks up this VM's values automatically. Once the cluster
  // detail screen is also subscribed (which it is, in normal navigation
  // flow) the WS subscription is shared via reference counting in the
  // ws-store and we don't double-subscribe.
  useClusterLiveMetrics(clusterId);
  const live = useLiveVM(vmId);

  // Resolve the Proxmox node name from the VM's node_id. The console
  // WebSocket needs the human-readable name (e.g. "pve1"), not the DB
  // UUID. nodes list is usually already cached from cluster detail.
  const nodeName = useMemo(() => {
    if (!vm.data || !nodes.data) return null;
    return nodes.data.find((n) => n.id === vm.data?.node_id)?.name ?? null;
  }, [vm.data, nodes.data]);

  const [refreshing, setRefreshing] = useState(false);
  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await Promise.all([vm.refetch(), metrics.refetch(), nodes.refetch()]);
    } finally {
      setRefreshing(false);
    }
  }, [vm, metrics, nodes]);

  function openConsole() {
    if (!vm.data || !nodeName) return;
    const consoleType = vm.data.type === "lxc" ? "ct_vnc" : "vm_vnc";
    // The console screen lives inside the clusters Stack navigator
    // (`clusters/[id]/console/[vmRowId].tsx`) so back navigation works as
    // a normal stack pop and lands on this VM detail screen. Cluster ID
    // comes from the parent route segment and the VM row UUID from the
    // console route segment; everything else is search params.
    router.push({
      pathname: "/(app)/clusters/[id]/console/[vmRowId]",
      params: {
        id: clusterId ?? "",
        vmRowId: vm.data.id,
        node: nodeName,
        vmid: String(vm.data.vmid),
        type: consoleType,
        label: vm.data.name || `vm-${String(vm.data.vmid)}`,
      },
    });
  }

  if (vm.isLoading && !vm.data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator color="#22c55e" />
        </View>
      </SafeAreaView>
    );
  }

  if (vm.isError || !vm.data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <ListError
          detail={vm.error instanceof Error ? vm.error.message : "Not found"}
        />
      </SafeAreaView>
    );
  }

  const v = vm.data;

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Stack.Screen
        options={{ title: v.name || `vm-${v.vmid}` }}
      />
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
                  {v.name || `vm-${v.vmid}`}
                </Text>
                <Text className="mt-1 text-xs text-muted-foreground">
                  {v.type.toUpperCase()} #{v.vmid}
                  {v.template ? " · template" : ""}
                </Text>
              </View>
              <StatusPill label={v.status} tone={statusToneFor(v.status)} />
            </View>

            {/* Live CPU + memory — populated by the cluster:<id>:metrics WS
                channel via the metric store. Hidden until the first message
                arrives so the layout doesn't jump on cold load. */}
            {live ? (
              <View className="mt-3 flex-row gap-3">
                <LiveStat label="CPU" value={`${live.cpuPercent.toFixed(1)}%`} />
                <LiveStat
                  label="Memory"
                  value={`${live.memPercent.toFixed(1)}%`}
                />
              </View>
            ) : null}

            {/* Open Console — disabled until we know the node name */}
            <TouchableOpacity
              onPress={openConsole}
              disabled={!nodeName || v.status !== "running"}
              className={`mt-4 flex-row items-center justify-center gap-2 rounded-lg border border-primary py-3 ${
                !nodeName || v.status !== "running" ? "opacity-40" : ""
              }`}
            >
              <Monitor color="#22c55e" size={18} />
              <Text className="text-sm font-semibold text-primary">
                Open Console
              </Text>
            </TouchableOpacity>

            <PowerActions vm={v} clusterId={clusterId ?? ""} />
          </View>

          {/* Specs grid */}
          <SectionHeader title="Specs" />
          <View className="rounded-lg border border-border bg-card">
            <Row label="vCPUs" value={String(v.cpu_count)} />
            <Row label="Memory" value={formatBytes(v.mem_total)} />
            <Row label="Disk" value={formatBytes(v.disk_total)} />
            <Row label="Uptime" value={formatUptime(v.uptime)} />
            <Row label="HA state" value={v.ha_state || "—"} />
            <Row label="Pool" value={v.pool || "—"} />
            <Row label="Tags" value={v.tags || "—"} last />
          </View>

          {/* Metric sparklines (last hour) */}
          <SectionHeader title="Last hour" />
          <MetricsCard points={metrics.data} loading={metrics.isLoading} />

          {/* Snapshots — list, create, restore, delete. Hidden for templates
              and when the user lacks the relevant RBAC permission. */}
          <SnapshotsSection vm={v} clusterId={clusterId ?? ""} />

          {/* Metadata */}
          <SectionHeader title="Metadata" />
          <View className="rounded-lg border border-border bg-card">
            <Row label="Last seen" value={formatRelative(v.last_seen_at)} />
            <Row label="Created" value={formatRelative(v.created_at)} last />
          </View>

        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

