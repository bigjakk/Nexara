import { Stack } from "expo-router";

import { StackHeader } from "@/components/StackHeader";

export default function AlertsStackLayout() {
  return (
    <Stack
      screenOptions={{
        contentStyle: { backgroundColor: "#0a0a0a" },
        // See clusters/_layout.tsx and components/StackHeader.tsx —
        // custom header so the safe area top inset is always respected.
        header: (props) => <StackHeader {...props} />,
      }}
    >
      <Stack.Screen name="index" options={{ title: "Alerts" }} />
      <Stack.Screen name="[id]" options={{ title: "Alert" }} />
    </Stack>
  );
}
