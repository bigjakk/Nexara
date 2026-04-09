/**
 * Build-time feature flags for the mobile app.
 *
 * Add a flag here when you have working code that you want to keep around
 * but ship with the user-facing surface disabled. The flag should default
 * to OFF until everything required to enable it is in place.
 *
 * Re-enabling a flag is a one-line change. The corresponding code paths
 * stay compiled and type-checked so they don't rot.
 */

export const featureFlags = {
  /**
   * Push notifications via Expo Push API.
   *
   * Disabled because:
   *   - Requires an EAS project ID + FCM credentials to actually deliver
   *   - The user explicitly chose to skip push for v1 and rely on existing
   *     channels (Slack, email, PagerDuty, etc.)
   *
   * To re-enable end-to-end:
   *   1. Run `npx eas init` from `mobile/` (creates the Expo project,
   *      writes `expo.extra.eas.projectId` into `app.json`).
   *   2. Set this flag to `true` and rebuild the APK.
   *   3. Re-enable the backend channel type by adding `"expo_push": true`
   *      back to `validChannelTypes` in
   *      `internal/api/handlers/alerts.go`.
   *   4. Re-add `{ value: "expo_push", label: "Mobile push (Expo)" }` to
   *      `CHANNEL_TYPES` in `frontend/src/features/alerts/components/ChannelForm.tsx`.
   *
   * The full code path (registration hook, tap handler, dispatcher,
   * /me/devices endpoints, settings UI) is kept intact behind this flag.
   */
  pushNotifications: false,
} as const;
