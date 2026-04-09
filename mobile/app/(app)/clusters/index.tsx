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
import { Server } from "lucide-react-native";

import { useClusters } from "@/features/api/cluster-queries";
import type { Cluster } from "@/features/api/types";
import { ListEmpty, ListError } from "@/components/ListEmpty";
import { StatusPill, statusToneFor } from "@/components/StatusPill";
import { formatRelative } from "@/lib/format";

export default function ClustersListScreen() {
  const router = useRouter();
  const { data, isLoading, isError, error, refetch } = useClusters();
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
        keyExtractor={(c) => c.id}
        contentContainerClassName="p-4"
        ItemSeparatorComponent={() => <View className="h-3" />}
        renderItem={({ item }) => (
          <ClusterRow
            cluster={item}
            onPress={() => router.push(`/(app)/clusters/${item.id}`)}
          />
        )}
        ListEmptyComponent={
          <ListEmpty
            title="No clusters"
            detail="Add a Proxmox cluster from the web UI to see it here."
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

function ClusterRow({
  cluster,
  onPress,
}: {
  cluster: Cluster;
  onPress: () => void;
}) {
  return (
    <TouchableOpacity
      onPress={onPress}
      className="rounded-lg border border-border bg-card p-4"
      activeOpacity={0.7}
    >
      <View className="flex-row items-center gap-3">
        <View className="h-10 w-10 items-center justify-center rounded bg-muted">
          <Server color="#a1a1aa" size={20} />
        </View>
        <View className="flex-1">
          <Text className="text-base font-semibold text-foreground">
            {cluster.name}
          </Text>
          <Text className="mt-0.5 text-xs text-muted-foreground" numberOfLines={1}>
            {cluster.api_url}
          </Text>
        </View>
        <StatusPill label={cluster.status} tone={statusToneFor(cluster.status)} />
      </View>
      <View className="mt-3 flex-row items-center justify-between">
        <Text className="text-[11px] text-muted-foreground">
          updated {formatRelative(cluster.updated_at)}
        </Text>
        <Text className="text-[11px] text-muted-foreground">
          sync {cluster.sync_interval_seconds}s
        </Text>
      </View>
    </TouchableOpacity>
  );
}
