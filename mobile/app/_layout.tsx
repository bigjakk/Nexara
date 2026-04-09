import "../global.css";

import { useEffect, useState } from "react";
import { ActivityIndicator, Text, View } from "react-native";
import { Slot, useRouter } from "expo-router";
import { GestureHandlerRootView } from "react-native-gesture-handler";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { StatusBar } from "expo-status-bar";
import { QueryClient } from "@tanstack/react-query";
import { PersistQueryClientProvider } from "@tanstack/react-query-persist-client";

import { mmkvQueryPersister } from "@/lib/mmkv";
import { installConsoleHooks } from "@/lib/log-buffer";
import { onAuthLost, registerLogoutCleanup } from "@/features/api/api-client";
import { useAuthStore, type AuthStatus } from "@/stores/auth-store";
import { useMetricStore } from "@/stores/metric-store";
import { useTaskTrackerStore } from "@/stores/task-tracker-store";
import { useWsStore } from "@/stores/ws-store";
import { useAutoLock } from "@/hooks/useAutoLock";
import { DiagnosticsOverlay } from "@/components/DiagnosticsOverlay";

// Install console hooks before any other module gets a chance to log so we
// capture everything from boot onwards.
installConsoleHooks();

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 30_000,
      gcTime: 24 * 60 * 60 * 1000, // keep in persister for a day
      refetchOnWindowFocus: false,
    },
  },
});

export default function RootLayout() {
  const bootstrap = useAuthStore((s) => s.bootstrap);
  const logout = useAuthStore((s) => s.logout);
  const status = useAuthStore((s) => s.status);
  const wsConnect = useWsStore((s) => s.connect);
  const wsDisconnect = useWsStore((s) => s.disconnect);
  const [ready, setReady] = useState(false);

  // 5-min background auto-lock: transitions authed → locked when the app
  // is foregrounded after being in the background for more than 5 minutes
  // AND the user has enabled biometric unlock in Settings. See the hook
  // for the full rationale.
  useAutoLock();

  useEffect(() => {
    void bootstrap().finally(() => setReady(true));
  }, [bootstrap]);

  useEffect(() => {
    // When the API client emits auth-lost (refresh failed), clear state
    // and bounce the user to login.
    const unsubscribe = onAuthLost(() => {
      void logout();
    });
    return unsubscribe;
  }, [logout]);

  // Register a logout cleanup that wipes non-token state — the in-memory
  // TanStack Query cache, its MMKV persister, the live metric store, and
  // the task tracker. Without this, previous-user data survives sign-out
  // on the same device (R2-H4 / R2-L5). Runs exactly once per mount; the
  // cleanup deregisters itself on unmount so hot reload doesn't stack
  // duplicates.
  useEffect(() => {
    return registerLogoutCleanup(async () => {
      queryClient.clear();
      await mmkvQueryPersister.removeClient();
      useMetricStore.getState().clearAll();
      useTaskTrackerStore.getState().clear();
    });
  }, []);

  // Open the WebSocket connection while authed; close it whenever we drop
  // out of the authed state (logout, refresh failure, app lock). Also wipe
  // the live metric store on disconnect so values from the previous user
  // / instance don't bleed into the next session.
  useEffect(() => {
    if (status === "authed") {
      void wsConnect();
    } else {
      wsDisconnect();
      useMetricStore.getState().clearAll();
    }
  }, [status, wsConnect, wsDisconnect]);

  if (!ready) {
    // Branded loading state shown while the auth store hydrates from
    // SecureStore. Replaces the black void gap between native splash and
    // first screen render.
    return (
      <View style={{ flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: "#0a0a0a" }}>
        <Text style={{ color: "#fafafa", fontSize: 28, fontWeight: "700", marginBottom: 12 }}>
          Nexara
        </Text>
        <ActivityIndicator color="#22c55e" />
      </View>
    );
  }

  return (
    <GestureHandlerRootView style={{ flex: 1 }}>
      {/*
        SafeAreaProvider is load-bearing — without it, react-native-safe-area-
        context returns zero insets, which means React Navigation's Stack
        header draws at y=0 and overlaps the status bar / camera punch hole,
        and every <SafeAreaView edges={...}> in the app silently degrades to
        no padding. This was a latent bug since M1; the screens looked OK
        because their internal padding absorbed the gap, but the React
        Navigation header had no such padding and the overlap was visible.
      */}
      <SafeAreaProvider>
        <PersistQueryClientProvider
          client={queryClient}
          persistOptions={{ persister: mmkvQueryPersister }}
        >
          <StatusBar style="light" />
          <AuthGate />
          <DiagnosticsOverlay />
        </PersistQueryClientProvider>
      </SafeAreaProvider>
    </GestureHandlerRootView>
  );
}

/**
 * Routes the user to the correct screen based on auth status. The router is
 * a single source of truth for navigation gating — every status transition
 * triggers a router.replace, and router.replace is a no-op if we're already
 * on the target route.
 *
 * The previous implementation only navigated when the screen GROUP changed,
 * which broke transitions within the (auth) group (e.g. server-url → login).
 */
function AuthGate() {
  const status = useAuthStore((s) => s.status);
  const router = useRouter();

  useEffect(() => {
    const href = hrefForStatus(status);
    if (href) {
      console.log("[AuthGate] status =", status, "→", href);
      router.replace(href);
    }
  }, [status, router]);

  return <Slot />;
}

type GateHref =
  | "/(auth)/server-url"
  | "/(auth)/login"
  | "/(auth)/totp"
  | "/(auth)/locked"
  | "/(app)";

function hrefForStatus(status: AuthStatus): GateHref | null {
  switch (status) {
    case "loading":
      return null;
    case "unconfigured":
      return "/(auth)/server-url";
    case "logged_out":
      return "/(auth)/login";
    case "totp_pending":
      return "/(auth)/totp";
    case "locked":
      return "/(auth)/locked";
    case "authed":
      return "/(app)";
  }
}
