/**
 * Registers the current device for push notifications via the Expo Push
 * service and uploads the resulting token to the Nexara backend.
 *
 * Lifecycle:
 *   1. On every authenticated launch, request notification permission
 *      (idempotent — Android may show the system prompt the first time;
 *      iOS shows it once and remembers).
 *   2. If granted, fetch an Expo push token via `getExpoPushTokenAsync`.
 *      The token is opaque and stable for this install (until the user
 *      uninstalls or restores from backup, at which point it rotates).
 *   3. POST the token + device metadata to `/api/v1/me/devices`. The
 *      backend's UPSERT keeps the same row across re-registrations as
 *      long as the device_id matches.
 *
 * The hook intentionally swallows errors (logs them via console.error so
 * they show up in the diagnostics overlay) so push setup failures never
 * block the user from using the rest of the app.
 */

import { useEffect } from "react";
import * as Notifications from "expo-notifications";
import Constants from "expo-constants";
import { Platform } from "react-native";

import { useRegisterDevice } from "@/features/api/device-queries";
import { getDeviceInfo } from "@/lib/device";
import { useAuthStore } from "@/stores/auth-store";
import { featureFlags } from "@/lib/feature-flags";

// Configure how foreground notifications are handled — show them as a
// banner with sound, even when the app is open. Only registered when push
// is enabled so we don't override Expo defaults for users who don't use
// push at all.
if (featureFlags.pushNotifications) {
  Notifications.setNotificationHandler({
    handleNotification: async () => ({
      shouldShowBanner: true,
      shouldShowList: true,
      shouldPlaySound: true,
      shouldSetBadge: true,
    }),
  });
}

export function usePushRegistration(): void {
  const status = useAuthStore((s) => s.status);
  const register = useRegisterDevice();

  useEffect(() => {
    if (!featureFlags.pushNotifications) return;
    if (status !== "authed") return;

    let cancelled = false;

    void (async () => {
      try {
        // 1. Request permission
        const perms = await Notifications.getPermissionsAsync();
        let granted = perms.status === "granted";
        if (!granted) {
          const ask = await Notifications.requestPermissionsAsync();
          granted = ask.status === "granted";
        }
        if (!granted) {
          console.warn("[push] notification permission not granted");
          return;
        }

        // 2. Get the Expo push token
        // expo-notifications needs the projectId for SDK 53+. We pull it
        // from EAS config if available, falling back to the easConfig
        // section in app.json.
        const projectId =
          Constants.expoConfig?.extra?.eas?.projectId ??
          Constants.easConfig?.projectId;

        const tokenResp = await Notifications.getExpoPushTokenAsync(
          projectId ? { projectId } : undefined,
        );
        if (cancelled) return;

        if (!tokenResp.data) {
          console.warn("[push] getExpoPushTokenAsync returned no token");
          return;
        }

        // 3. Upload to backend
        const info = await getDeviceInfo();
        await register.mutateAsync({
          device_id: info.id,
          device_name: info.name,
          platform: Platform.OS === "ios" ? "ios" : "android",
          expo_push_token: tokenResp.data,
        });
        console.log("[push] device registered");
      } catch (err) {
        console.error("[push] registration failed", err);
      }
    })();

    return () => {
      cancelled = true;
    };
    // We intentionally only run on auth status changes, not on register
    // mutation identity changes (which would loop).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [status]);
}
