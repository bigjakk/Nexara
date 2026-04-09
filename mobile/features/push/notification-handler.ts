/**
 * Routes notification taps to the right screen.
 *
 * The dispatcher's `data` payload includes `alert_id` (the rule id, or the
 * alert id depending on context — see `expo_push.go`). When the user taps
 * a notification we deep-link them to the alert detail screen.
 */

import { useEffect } from "react";
import { useRouter } from "expo-router";
import * as Notifications from "expo-notifications";

import { featureFlags } from "@/lib/feature-flags";

interface NotificationData {
  alert_id?: string;
  cluster_id?: string;
  severity?: string;
  resource_name?: string;
}

export function useNotificationTapHandler(): void {
  const router = useRouter();

  useEffect(() => {
    if (!featureFlags.pushNotifications) return;
    const sub = Notifications.addNotificationResponseReceivedListener(
      (response) => {
        const raw = response.notification.request.content.data as
          | NotificationData
          | undefined;
        if (!raw) return;

        if (raw.alert_id) {
          router.push(`/(app)/alerts/${raw.alert_id}`);
          return;
        }
        // Fall back to the alerts list if we can't pinpoint the alert.
        router.push("/(app)/alerts");
      },
    );

    return () => {
      sub.remove();
    };
  }, [router]);
}
