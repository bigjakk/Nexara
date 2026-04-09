/**
 * Node detail screen — mirrors the VM detail layout but with
 * node-specific sections (CPU / Memory / System / Subscription).
 *
 * Route: /(app)/clusters/[id]/nodes/[nodeId]
 *
 * Lives inside the clusters Stack navigator alongside the VM detail
 * and console screens, so back navigation works as a normal stack pop.
 *
 * Navigated to from:
 *   - The cluster detail screen's node row (tap to drill in)
 *   - The global search modal (node-type results)
 *
 * Provides an "Open Shell" button that mints a node-shell console token
 * and opens the existing console screen with `type=node_shell`. This is
 * the first UI surface that exercises the node-shell code path; the
 * backend (auth.go console-token, ws/server.go scope validation) and
 * the mobile-console web route have supported it since M1 but nothing
 * triggered it until now.
 */

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
import { Terminal } from "lucide-react-native";

import { useNode } from "@/features/api/node-queries";
import { useNodeMetrics } from "@/features/api/metric-queries";
import {
  useClusterLiveMetrics,
  useLiveNode,
} from "@/hooks/useClusterLiveMetrics";
import { ListError } from "@/components/ListEmpty";
import { StatusPill, statusToneFor } from "@/components/StatusPill";
import {
  LiveStat,
  MetricsCard,
  Row,
  SectionHeader,
} from "@/components/detail-screen";
import { formatBytes, formatRelative, formatUptime } from "@/lib/format";

export default function NodeDetailScreen() {
  const params = useLocalSearchParams<{ id: string; nodeId: string }>();
  const clusterId = params.id;
  const nodeId = params.nodeId;
  const router = useRouter();

  const node = useNode(clusterId, nodeId);
  const metrics = useNodeMetrics(clusterId, nodeId, "1h");
  // Subscribe to live metrics for the parent cluster (shared with the
  // cluster detail screen via ws-store reference counting). The hook
  // populates per-node entries in the metric store; useLiveNode reads
  // this node's values without re-subscribing.
  useClusterLiveMetrics(clusterId);
  const live = useLiveNode(nodeId);

  const [refreshing, setRefreshing] = useState(false);
  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await Promise.all([node.refetch(), metrics.refetch()]);
    } finally {
      setRefreshing(false);
    }
  }, [node, metrics]);

  function openShell() {
    if (!node.data) return;
    // Navigate to the existing console screen with type=node_shell. The
    // vmRowId slot in the route is required by the file-based router but
    // unused for node shells — we pass the node UUID as a meaningful
    // sentinel so the URL is still informative. The console screen
    // skips the useVM call when type is node_shell so the node UUID
    // doesn't accidentally hit the /vms/:id endpoint.
    router.push({
      pathname: "/(app)/clusters/[id]/console/[vmRowId]",
      params: {
        id: clusterId ?? "",
        vmRowId: node.data.id,
        node: node.data.name,
        type: "node_shell",
        label: `${node.data.name} shell`,
      },
    });
  }

  if (node.isLoading && !node.data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator color="#22c55e" />
        </View>
      </SafeAreaView>
    );
  }

  if (node.isError || !node.data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <ListError
          detail={
            node.error instanceof Error ? node.error.message : "Node not found"
          }
        />
      </SafeAreaView>
    );
  }

  const n = node.data;

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Stack.Screen options={{ title: n.name }} />
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
                  {n.name}
                </Text>
                <Text className="mt-1 text-xs text-muted-foreground">
                  {n.pve_version || "Proxmox node"}
                </Text>
              </View>
              <StatusPill label={n.status} tone={statusToneFor(n.status)} />
            </View>

            {/* Live CPU + memory — populated via the cluster:<id>:metrics
                WS channel. Hidden until the first message arrives. */}
            {live ? (
              <View className="mt-3 flex-row gap-3">
                <LiveStat label="CPU" value={`${live.cpuPercent.toFixed(1)}%`} />
                <LiveStat
                  label="Memory"
                  value={`${live.memPercent.toFixed(1)}%`}
                />
              </View>
            ) : null}

            {/* Open Shell — disabled when the node is offline */}
            <TouchableOpacity
              onPress={openShell}
              disabled={n.status !== "online"}
              className={`mt-4 flex-row items-center justify-center gap-2 rounded-lg border border-primary py-3 ${
                n.status !== "online" ? "opacity-40" : ""
              }`}
            >
              <Terminal color="#22c55e" size={18} />
              <Text className="text-sm font-semibold text-primary">
                Open Shell
              </Text>
            </TouchableOpacity>
          </View>

          {/* Processor */}
          <SectionHeader title="Processor" />
          <View className="rounded-lg border border-border bg-card">
            <Row label="Model" value={n.cpu_model || "—"} />
            <Row
              label="Cores / threads"
              value={`${String(n.cpu_cores)} / ${String(n.cpu_threads)}`}
            />
            <Row label="Sockets" value={String(n.cpu_sockets)} />
            <Row
              label="Frequency"
              value={n.cpu_mhz ? `${n.cpu_mhz} MHz` : "—"}
              last
            />
          </View>

          {/* Memory */}
          <SectionHeader title="Memory" />
          <View className="rounded-lg border border-border bg-card">
            <Row label="Total RAM" value={formatBytes(n.mem_total)} />
            <Row
              label="Swap"
              value={
                n.swap_total > 0
                  ? `${formatBytes(n.swap_used)} / ${formatBytes(n.swap_total)}`
                  : "—"
              }
              last
            />
          </View>

          {/* System */}
          <SectionHeader title="System" />
          <View className="rounded-lg border border-border bg-card">
            <Row label="PVE version" value={n.pve_version || "—"} />
            <Row label="Kernel" value={n.kernel_version || "—"} />
            <Row label="Uptime" value={formatUptime(n.uptime)} />
            <Row label="Load avg" value={n.load_avg || "—"} />
            <Row
              label="I/O wait"
              value={
                Number.isFinite(n.io_wait) ? `${n.io_wait.toFixed(1)}%` : "—"
              }
            />
            <Row label="Timezone" value={n.timezone || "—"} last />
          </View>

          {/* Metric sparklines */}
          <SectionHeader title="Last hour" />
          <MetricsCard points={metrics.data} loading={metrics.isLoading} />

          {/* Subscription (only shown when there's actually subscription info) */}
          {n.subscription_status ? (
            <>
              <SectionHeader title="Subscription" />
              <View className="rounded-lg border border-border bg-card">
                <Row
                  label="Status"
                  value={n.subscription_status}
                  last={!n.subscription_level}
                />
                {n.subscription_level ? (
                  <Row label="Level" value={n.subscription_level} last />
                ) : null}
              </View>
            </>
          ) : null}

          {/* Metadata */}
          <SectionHeader title="Metadata" />
          <View className="rounded-lg border border-border bg-card">
            <Row label="Last seen" value={formatRelative(n.last_seen_at)} />
            <Row label="Created" value={formatRelative(n.created_at)} last />
          </View>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

