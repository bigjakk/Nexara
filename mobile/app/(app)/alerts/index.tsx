import { useCallback, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  RefreshControl,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useRouter } from "expo-router";

import { useAlerts } from "@/features/api/alert-queries";
import type { Alert, AlertState } from "@/features/api/types";
import { ListEmpty, ListError } from "@/components/ListEmpty";
import { StatusPill, statusToneFor } from "@/components/StatusPill";
import { formatRelative } from "@/lib/format";

const STATE_FILTERS: { label: string; value: AlertState | "all" }[] = [
  { label: "All", value: "all" },
  { label: "Firing", value: "firing" },
  { label: "Acked", value: "acknowledged" },
  { label: "Resolved", value: "resolved" },
];

export default function AlertsListScreen() {
  const router = useRouter();
  const [filter, setFilter] = useState<AlertState | "all">("all");

  const { data, isLoading, isError, error, refetch } = useAlerts(
    filter === "all" ? undefined : { state: filter },
  );

  const [refreshing, setRefreshing] = useState(false);
  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await refetch();
    } finally {
      setRefreshing(false);
    }
  }, [refetch]);

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      {/* Filter bar */}
      <View className="flex-row gap-2 px-4 pt-3 pb-2">
        {STATE_FILTERS.map((f) => (
          <TouchableOpacity
            key={f.value}
            onPress={() => setFilter(f.value)}
            className={`rounded-full border px-3 py-1 ${
              filter === f.value
                ? "border-primary bg-primary/20"
                : "border-border bg-card"
            }`}
          >
            <Text
              className={`text-xs font-medium ${
                filter === f.value ? "text-primary" : "text-muted-foreground"
              }`}
            >
              {f.label}
            </Text>
          </TouchableOpacity>
        ))}
      </View>

      {isLoading && !data ? (
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator color="#22c55e" />
        </View>
      ) : isError ? (
        <ListError detail={error instanceof Error ? error.message : undefined} />
      ) : (
        <FlatList
          data={data ?? []}
          keyExtractor={(a) => a.id}
          contentContainerClassName="px-4 pb-4"
          ItemSeparatorComponent={() => <View className="h-2" />}
          renderItem={({ item }) => (
            <AlertRow
              alert={item}
              onPress={() => router.push(`/(app)/alerts/${item.id}`)}
            />
          )}
          ListEmptyComponent={
            <ListEmpty
              title="No alerts"
              detail={
                filter === "all"
                  ? "Nothing has fired yet."
                  : `No alerts in state "${filter}".`
              }
            />
          }
          refreshControl={
            <RefreshControl
              refreshing={refreshing}
              onRefresh={onRefresh}
              tintColor="#22c55e"
            />
          }
        />
      )}
    </SafeAreaView>
  );
}

function AlertRow({
  alert,
  onPress,
}: {
  alert: Alert;
  onPress: () => void;
}) {
  return (
    <TouchableOpacity
      onPress={onPress}
      activeOpacity={0.7}
      className="rounded-lg border border-border bg-card p-3"
    >
      <View className="flex-row items-start justify-between gap-2">
        <View className="flex-1">
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
          <Text
            className="mt-1.5 text-sm font-semibold text-foreground"
            numberOfLines={1}
          >
            {alert.resource_name || alert.metric}
          </Text>
          <Text className="text-xs text-muted-foreground" numberOfLines={2}>
            {alert.message}
          </Text>
        </View>
      </View>
      <Text className="mt-2 text-[10px] text-muted-foreground">
        {alert.fired_at
          ? `fired ${formatRelative(alert.fired_at)}`
          : `pending ${formatRelative(alert.pending_at)}`}
      </Text>
    </TouchableOpacity>
  );
}
