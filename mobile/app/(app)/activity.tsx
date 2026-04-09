import { useCallback, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  RefreshControl,
  Text,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { useRecentAuditLog } from "@/features/api/audit-queries";
import type { AuditLogEntry } from "@/features/api/types";
import { ListEmpty, ListError } from "@/components/ListEmpty";
import { formatRelative } from "@/lib/format";

export default function ActivityScreen() {
  const { data, isLoading, isError, error, refetch } = useRecentAuditLog();
  const [refreshing, setRefreshing] = useState(false);

  const onRefresh = useCallback(async () => {
    setRefreshing(true);
    try {
      await refetch();
    } finally {
      setRefreshing(false);
    }
  }, [refetch]);

  if (isLoading && !data) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <View className="flex-1 items-center justify-center">
          <ActivityIndicator color="#22c55e" />
        </View>
      </SafeAreaView>
    );
  }

  if (isError) {
    return (
      <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
        <ListError detail={error instanceof Error ? error.message : undefined} />
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <FlatList
        data={data ?? []}
        keyExtractor={(e) => e.id}
        contentContainerClassName="p-4"
        ItemSeparatorComponent={() => <View className="h-2" />}
        renderItem={({ item }) => <AuditRow entry={item} />}
        ListEmptyComponent={
          <ListEmpty
            title="No recent activity"
            detail="Actions taken on Proxmox resources show up here."
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
    </SafeAreaView>
  );
}

function AuditRow({ entry }: { entry: AuditLogEntry }) {
  const subject = formatSubject(entry);
  return (
    <View className="rounded-lg border border-border bg-card p-3">
      <View className="flex-row items-start justify-between gap-2">
        <View className="flex-1">
          <Text className="text-sm font-semibold text-foreground" numberOfLines={1}>
            {entry.action}
          </Text>
          <Text
            className="mt-0.5 text-xs text-muted-foreground"
            numberOfLines={2}
          >
            {subject}
          </Text>
        </View>
        <Text className="text-[10px] text-muted-foreground">
          {formatRelative(entry.created_at)}
        </Text>
      </View>
      {entry.user_email ? (
        <Text className="mt-2 text-[10px] text-muted-foreground">
          {entry.user_display_name || entry.user_email}
          {entry.source ? ` · ${entry.source}` : ""}
        </Text>
      ) : null}
    </View>
  );
}

function formatSubject(entry: AuditLogEntry): string {
  const parts: string[] = [];
  if (entry.resource_type) parts.push(entry.resource_type);
  if (entry.resource_name) parts.push(entry.resource_name);
  else if (entry.resource_vmid > 0) parts.push(`#${entry.resource_vmid}`);
  if (entry.cluster_name) parts.push(`on ${entry.cluster_name}`);
  return parts.join(" ") || "—";
}
