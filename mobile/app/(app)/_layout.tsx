import { useMemo } from "react";
import { View } from "react-native";
import { Tabs } from "expo-router";
import {
  Activity,
  Bell,
  LayoutDashboard,
  Settings,
  Server,
} from "lucide-react-native";

import { useClusters } from "@/features/api/cluster-queries";
import { useEventInvalidation } from "@/features/api/use-event-invalidation";
import { featureFlags } from "@/lib/feature-flags";
import { usePushRegistration } from "@/features/push/use-push-registration";
import { useNotificationTapHandler } from "@/features/push/notification-handler";
import { SearchButton } from "@/features/search/SearchButton";
import { SearchModal } from "@/features/search/SearchModal";
import { TaskNotificationBar } from "@/components/TaskNotificationBar";
import { useTaskCompletion } from "@/hooks/useTaskCompletion";

export default function AppLayout() {
  // Subscribe to cluster-scoped event channels for every cluster the user
  // can see, plus the global system events channel. Memoised so the effect
  // in `useEventInvalidation` only re-subscribes when the cluster set
  // actually changes.
  const clusters = useClusters();
  const clusterIds = useMemo(
    () => clusters.data?.map((c) => c.id) ?? [],
    [clusters.data],
  );
  useEventInvalidation(clusterIds);
  // Subscribes to the same WS channels and completes tracked tasks
  // (power actions etc.) when matching vm_state_change events arrive.
  // See `hooks/useTaskCompletion.ts` for the correlation logic.
  useTaskCompletion(clusterIds);

  // Push notifications: register the device on every authed launch and
  // route taps to the matching alert. Disabled until EAS setup lands —
  // see `lib/feature-flags.ts`. The hooks themselves are unconditional
  // because rules-of-hooks demands it; they no-op when the flag is off.
  usePushRegistration();
  useNotificationTapHandler();

  return (
    <View style={{ flex: 1 }}>
      <Tabs
        screenOptions={{
          headerStyle: { backgroundColor: "#0a0a0a" },
          headerTintColor: "#fafafa",
          // Search icon on the right side of every Tabs header. Tabs with
          // `headerShown: false` (clusters/alerts) wear their own custom
          // StackHeader which also includes the SearchButton, so the icon
          // appears on every screen in the (app) group.
          headerRight: () => <SearchButton />,
          tabBarStyle: {
            backgroundColor: "#0a0a0a",
            borderTopColor: "#262626",
          },
          tabBarActiveTintColor: "#22c55e",
          tabBarInactiveTintColor: "#71717a",
        }}
      >
        <Tabs.Screen
          name="index"
          options={{
            title: "Dashboard",
            tabBarIcon: ({ color, size }) => (
              <LayoutDashboard color={color} size={size} />
            ),
          }}
        />
        <Tabs.Screen
          name="clusters"
          options={{
            title: "Clusters",
            headerShown: false,
            tabBarIcon: ({ color, size }) => <Server color={color} size={size} />,
          }}
        />
        <Tabs.Screen
          name="alerts"
          options={{
            title: "Alerts",
            headerShown: false,
            tabBarIcon: ({ color, size }) => <Bell color={color} size={size} />,
          }}
        />
        <Tabs.Screen
          name="activity"
          options={{
            title: "Activity",
            tabBarIcon: ({ color, size }) => (
              <Activity color={color} size={size} />
            ),
          }}
        />
        <Tabs.Screen
          name="settings"
          options={{
            title: "Settings",
            tabBarIcon: ({ color, size }) => (
              <Settings color={color} size={size} />
            ),
          }}
        />
      </Tabs>

      {/*
        Global search modal mounted once at the (app) layout level.
        Visibility is driven by `useSearchStore`. Any header button
        anywhere in the (app) group can open it via `searchStore.open()`.
        Mounted as a sibling of <Tabs/> so it overlays the entire tab bar
        when shown.
      */}
      <SearchModal />

      {/*
        Global task notification bar mounted once at the (app) layout
        level. Visibility is driven by `useTaskTrackerStore`. Power
        actions and snapshot mutations push tracked tasks here so the
        user gets persistent feedback while operations are running, even
        if they navigate away from the screen that triggered the action.
        Power actions are completed via WS event correlation
        (`useTaskCompletion`); snapshots are completed on a fixed delay
        because the backend publishes snapshot events on dispatch rather
        than after Proxmox completion.
      */}
      <TaskNotificationBar />
    </View>
  );
}
