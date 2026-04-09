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
import { useRouter } from "expo-router";
import {
  AlertTriangle,
  CheckCircle,
  Layers,
  Server,
} from "lucide-react-native";

import { useClusters } from "@/features/api/cluster-queries";
import { useAlertSummary, useAlerts } from "@/features/api/alert-queries";
import type { Cluster } from "@/features/api/types";
import { useClustersLiveMetrics } from "@/hooks/useClusterLiveMetrics";
import type { AggregatedClusterMetrics } from "@/stores/metric-store";
import { useAuthStore } from "@/stores/auth-store";
import { StatusPill, statusToneFor } from "@/components/StatusPill";
import { TopConsumers } from "@/features/dashboard/TopConsumers";
import { formatRelative } from "@/lib/format";

export default function DashboardScreen() {
  const user = useAuthStore((s) => s.user);
  const router = useRouter();

  const clusters = useClusters();
  const summary = useAlertSummary();
  const recentAlerts = useAlerts({ state: "firing", limit: 5 });

  // Subscribe to live metric streams for every cluster the user can see.
  // The dashboard is the central place to surface "is anything saturated
  // right now" so subscribing here gives the cluster cards live CPU/Mem
  // bars as soon as the first WS message lands. Reference counting in the
  // ws-store means navigating into a cluster detail screen (which also
  // calls useClusterLiveMetrics(id)) doesn't double-subscribe.
  const clusterIds = clusters.data?.map((c) => c.id) ?? [];
  const liveMetrics = useClustersLiveMetrics(clusterIds);

  const [refreshing, setRefreshing] = useState(false);
  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await Promise.all([
        clusters.refetch(),
        summary.refetch(),
        recentAlerts.refetch(),
      ]);
    } finally {
      setRefreshing(false);
    }
  }, [clusters, summary, recentAlerts]);

  const onlineClusters =
    clusters.data?.filter((c) => c.status === "online").length ?? 0;
  const totalClusters = clusters.data?.length ?? 0;

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <ScrollView
        contentInsetAdjustmentBehavior="automatic"
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={onRefresh}
            tintColor="#22c55e"
          />
        }
      >
        <View className="p-4">
          <Text className="mb-1 text-2xl font-bold text-foreground">
            Welcome back
          </Text>
          <Text className="mb-6 text-muted-foreground">
            {user?.display_name || user?.email}
          </Text>

          {/* Top stat cards */}
          <View className="flex-row gap-3">
            <StatCard
              icon={<Server color="#22c55e" size={20} />}
              label="Clusters"
              value={
                clusters.isLoading && !clusters.data
                  ? "—"
                  : `${onlineClusters}/${totalClusters}`
              }
              detail={onlineClusters === totalClusters ? "all online" : "issues"}
              tone={onlineClusters === totalClusters ? "success" : "warning"}
              onPress={() => router.push("/(app)/clusters")}
            />
            <StatCard
              icon={
                summary.data && summary.data.firing_count > 0 ? (
                  <AlertTriangle color="#ef4444" size={20} />
                ) : (
                  <CheckCircle color="#22c55e" size={20} />
                )
              }
              label="Active alerts"
              value={
                summary.isLoading && !summary.data
                  ? "—"
                  : String(summary.data?.firing_count ?? 0)
              }
              detail={
                summary.data && summary.data.critical_firing > 0
                  ? `${summary.data.critical_firing} critical`
                  : "all clear"
              }
              tone={
                summary.data && summary.data.firing_count > 0
                  ? "danger"
                  : "success"
              }
              onPress={() => router.push("/(app)/alerts")}
            />
          </View>

          {/* Recent firing alerts */}
          <SectionHeader title="Recent firing alerts" />
          {recentAlerts.isLoading && !recentAlerts.data ? (
            <ActivityIndicator color="#22c55e" />
          ) : recentAlerts.data && recentAlerts.data.length > 0 ? (
            <View className="gap-2">
              {recentAlerts.data.map((alert) => (
                <TouchableOpacity
                  key={alert.id}
                  onPress={() => router.push(`/(app)/alerts/${alert.id}`)}
                  activeOpacity={0.7}
                  className="rounded-lg border border-border bg-card p-3"
                >
                  <View className="flex-row items-center gap-2">
                    <StatusPill
                      label={alert.severity}
                      tone={statusToneFor(alert.severity)}
                    />
                    <Text
                      className="flex-1 text-sm font-medium text-foreground"
                      numberOfLines={1}
                    >
                      {alert.resource_name || alert.metric}
                    </Text>
                  </View>
                  <Text
                    className="mt-1 text-xs text-muted-foreground"
                    numberOfLines={1}
                  >
                    {alert.message}
                  </Text>
                  <Text className="mt-1 text-[10px] text-muted-foreground">
                    {alert.fired_at ? `fired ${formatRelative(alert.fired_at)}` : "—"}
                  </Text>
                </TouchableOpacity>
              ))}
            </View>
          ) : (
            <View className="items-center rounded-lg border border-border bg-card p-6">
              <CheckCircle color="#22c55e" size={28} />
              <Text className="mt-2 text-sm text-muted-foreground">
                No alerts firing
              </Text>
            </View>
          )}

          {/* Cluster overview */}
          <SectionHeader title="Clusters" />
          {clusters.isLoading && !clusters.data ? (
            <ActivityIndicator color="#22c55e" />
          ) : clusters.data && clusters.data.length > 0 ? (
            <View className="gap-2">
              {clusters.data.map((c) => (
                <ClusterCard
                  key={c.id}
                  cluster={c}
                  live={liveMetrics.get(c.id)}
                  onPress={() => router.push(`/(app)/clusters/${c.id}`)}
                />
              ))}
            </View>
          ) : (
            <Text className="text-sm text-muted-foreground">
              No clusters configured
            </Text>
          )}

          {/*
            Top consumers — top 10 VMs by live CPU% across every visible
            cluster. Joins the metric store's per-VM map with the cluster
            VMs lists fetched via TanStack Query's useQueries. Tap a row
            to jump to the VM detail screen. The component handles its
            own loading / empty / list states; we just place it here.
          */}
          {clusters.data && clusters.data.length > 0 ? (
            <>
              <SectionHeader title="Top consumers" />
              <TopConsumers clusters={clusters.data} />
            </>
          ) : null}
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

function ClusterCard({
  cluster,
  live,
  onPress,
}: {
  cluster: Cluster;
  live: AggregatedClusterMetrics | undefined;
  onPress: () => void;
}) {
  return (
    <TouchableOpacity
      onPress={onPress}
      activeOpacity={0.7}
      className="rounded-lg border border-border bg-card p-3"
    >
      {/* Top row: icon + name + status */}
      <View className="flex-row items-center gap-3">
        <Layers color="#a1a1aa" size={18} />
        <Text className="flex-1 text-sm font-medium text-foreground" numberOfLines={1}>
          {cluster.name}
        </Text>
        <StatusPill label={cluster.status} tone={statusToneFor(cluster.status)} />
      </View>

      {/* Live metrics row — only renders once the first WS message has
          landed. Hidden on cold load so the layout doesn't jump. The bars
          use the same green→amber→red threshold scheme as storage rows. */}
      {live ? (
        <View className="mt-3 gap-2">
          <MetricBar label="CPU" pct={live.cpuPercent} />
          <MetricBar label="Memory" pct={live.memPercent} />
          <Text className="text-[10px] text-muted-foreground">
            {live.nodeCount} {live.nodeCount === 1 ? "node" : "nodes"} ·{" "}
            {live.vmCount} {live.vmCount === 1 ? "guest" : "guests"}
          </Text>
        </View>
      ) : null}
    </TouchableOpacity>
  );
}

/**
 * Single-line metric bar with a label, percentage value, and a horizontal
 * progress fill. Color shifts green → amber → red at the 75% / 90%
 * thresholds, matching the storage row pattern from cluster detail.
 */
function MetricBar({ label, pct }: { label: string; pct: number }) {
  const clamped = Math.max(0, Math.min(100, pct));
  const color =
    clamped >= 90 ? "#ef4444" : clamped >= 75 ? "#f59e0b" : "#22c55e";
  const width = `${clamped}%` as const;
  return (
    <View>
      <View className="flex-row items-center justify-between">
        <Text className="text-[10px] text-muted-foreground">{label}</Text>
        <Text className="text-[10px] text-muted-foreground">
          {clamped.toFixed(1)}%
        </Text>
      </View>
      <View className="mt-1 h-1 overflow-hidden rounded-full bg-muted">
        <View className="h-full" style={{ width, backgroundColor: color }} />
      </View>
    </View>
  );
}

function SectionHeader({ title }: { title: string }) {
  return (
    <Text className="mt-6 mb-3 text-xs font-bold uppercase text-muted-foreground">
      {title}
    </Text>
  );
}

function StatCard({
  icon,
  label,
  value,
  detail,
  tone,
  onPress,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  detail: string;
  tone: "success" | "warning" | "danger";
  onPress: () => void;
}) {
  const accentClass =
    tone === "danger"
      ? "border-destructive/40"
      : tone === "warning"
        ? "border-yellow-500/40"
        : "border-border";
  return (
    <TouchableOpacity
      onPress={onPress}
      activeOpacity={0.7}
      className={`flex-1 rounded-lg border bg-card p-4 ${accentClass}`}
    >
      <View className="flex-row items-center gap-2">
        {icon}
        <Text className="text-xs text-muted-foreground">{label}</Text>
      </View>
      <Text className="mt-2 text-3xl font-bold text-foreground">{value}</Text>
      <Text className="text-[11px] text-muted-foreground">{detail}</Text>
    </TouchableOpacity>
  );
}

