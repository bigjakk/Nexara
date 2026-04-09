import { Stack } from "expo-router";

import { StackHeader } from "@/components/StackHeader";

export default function ClustersStackLayout() {
  return (
    <Stack
      screenOptions={{
        contentStyle: { backgroundColor: "#0a0a0a" },
        // Use a custom header that handles its own safe area top inset.
        // The default native-stack header was overlapping the status bar
        // / camera punch on Android edge-to-edge mode (Expo SDK 53 +
        // API 35) — see `components/StackHeader.tsx` for the rationale.
        header: (props) => <StackHeader {...props} />,
      }}
    >
      <Stack.Screen name="index" options={{ title: "Clusters" }} />
      <Stack.Screen name="[id]" options={{ title: "Cluster" }} />
    </Stack>
  );
}
