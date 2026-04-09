import { useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert as RNAlert,
  ScrollView,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Stack, useLocalSearchParams } from "expo-router";

import {
  useAcknowledgeAlert,
  useAlerts,
  useResolveAlert,
} from "@/features/api/alert-queries";
import type { Alert as AlertType } from "@/features/api/types";
import { ListError } from "@/components/ListEmpty";
import { StatusPill, statusToneFor } from "@/components/StatusPill";
import { formatPercent, formatRelative } from "@/lib/format";

export default function AlertDetailScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();

  // We don't have a single-alert endpoint; reuse the list and find by id.
  // The list query is already cached so this is essentially free.
  const list = useAlerts();
  const alert = useMemo<AlertType | undefined>(
    () => list.data?.find((a) => a.id === id),
    [list.data, id],
  );

  const ack = useAcknowledgeAlert();
  const resolve = useResolveAlert();
  const [busy, setBusy] = useState<"ack" | "resolve" | null>(null);

  if (list.isLoading && !list.data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator color="#22c55e" />
        </View>
      </SafeAreaView>
    );
  }

  if (!alert) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <ListError title="Alert not found" />
      </SafeAreaView>
    );
  }

  async function handleAck() {
    if (!alert) return;
    setBusy("ack");
    try {
      await ack.mutateAsync(alert.id);
    } catch (err) {
      RNAlert.alert(
        "Acknowledge failed",
        err instanceof Error ? err.message : "Unknown error",
      );
    } finally {
      setBusy(null);
    }
  }

  async function handleResolve() {
    if (!alert) return;
    setBusy("resolve");
    try {
      await resolve.mutateAsync(alert.id);
    } catch (err) {
      RNAlert.alert(
        "Resolve failed",
        err instanceof Error ? err.message : "Unknown error",
      );
    } finally {
      setBusy(null);
    }
  }

  const canAck = alert.state === "firing";
  const canResolve = alert.state === "firing" || alert.state === "acknowledged";

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <Stack.Screen options={{ title: alert.resource_name || "Alert" }} />
      <ScrollView contentContainerClassName="p-4">
        {/* Header */}
        <View className="rounded-lg border border-border bg-card p-4">
          <View className="flex-row items-center gap-2">
            <StatusPill
              label={alert.severity}
              tone={statusToneFor(alert.severity)}
            />
            <StatusPill
              label={alert.state}
              tone={statusToneFor(alert.state)}
            />
          </View>
          <Text className="mt-3 text-lg font-semibold text-foreground">
            {alert.resource_name || alert.metric}
          </Text>
          <Text className="mt-1 text-sm text-muted-foreground">
            {alert.message}
          </Text>
        </View>

        {/* Details */}
        <SectionHeader title="Details" />
        <View className="rounded-lg border border-border bg-card">
          <Row label="Metric" value={alert.metric} />
          <Row label="Current value" value={String(alert.current_value)} />
          <Row label="Threshold" value={String(alert.threshold)} />
          <Row
            label="Escalation"
            value={`level ${alert.escalation_level}`}
          />
          <Row label="Cluster" value={alert.cluster_id ?? "—"} />
          <Row label="Pending at" value={formatRelative(alert.pending_at)} />
          <Row
            label="Fired at"
            value={alert.fired_at ? formatRelative(alert.fired_at) : "—"}
          />
          <Row
            label="Acknowledged at"
            value={
              alert.acknowledged_at
                ? formatRelative(alert.acknowledged_at)
                : "—"
            }
          />
          <Row
            label="Resolved at"
            value={
              alert.resolved_at ? formatRelative(alert.resolved_at) : "—"
            }
            last
          />
        </View>

        {/* Actions */}
        {(canAck || canResolve) && (
          <View className="mt-6 gap-3">
            {canAck && (
              <TouchableOpacity
                onPress={handleAck}
                disabled={busy !== null}
                className={`rounded-lg border border-border bg-card py-3 ${
                  busy === "ack" ? "opacity-50" : ""
                }`}
              >
                <Text className="text-center font-semibold text-foreground">
                  {busy === "ack" ? "Acknowledging..." : "Acknowledge"}
                </Text>
              </TouchableOpacity>
            )}
            {canResolve && (
              <TouchableOpacity
                onPress={handleResolve}
                disabled={busy !== null}
                className={`rounded-lg bg-primary py-3 ${
                  busy === "resolve" ? "opacity-50" : ""
                }`}
              >
                <Text className="text-center font-semibold text-primary-foreground">
                  {busy === "resolve" ? "Resolving..." : "Resolve"}
                </Text>
              </TouchableOpacity>
            )}
          </View>
        )}
      </ScrollView>
    </SafeAreaView>
  );
}

function SectionHeader({ title }: { title: string }) {
  return (
    <Text className="mt-6 mb-3 text-xs font-bold uppercase text-muted-foreground">
      {title}
    </Text>
  );
}

function Row({
  label,
  value,
  last,
}: {
  label: string;
  value: string;
  last?: boolean;
}) {
  return (
    <View
      className={`flex-row items-center justify-between px-4 py-3 ${
        last ? "" : "border-b border-border"
      }`}
    >
      <Text className="text-sm text-muted-foreground">{label}</Text>
      <Text
        className="flex-1 pl-3 text-right text-sm text-foreground"
        numberOfLines={1}
      >
        {value}
      </Text>
    </View>
  );
}
