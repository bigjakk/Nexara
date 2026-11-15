import { useEffect, useState } from "react";
import { ApiClientError, sessionScope } from "@/lib/api-client";
import { applyAccentColor } from "@/lib/accent";
import { usePreferencesStore } from "@/stores/preferences-store";
import { useSetting } from "@/features/settings/api/settings-queries";

export const USER_PREFERENCES_KEY = "user.preferences";

/**
 * Applies the signed-in user's saved preferences (Settings > Appearance) once
 * they load, so a new or cleared browser gets them without a visit to that
 * page. The local copy in preferences-store is applied at startup and stays the
 * fast path; this only replaces it when the server answers.
 *
 * Mounted inside the shell, which ProtectedRoute remounts per user, and the
 * query cache is cleared when a session ends, so each sign-in reads its own
 * user's row. A user with no saved row (404) gets the defaults rather than
 * whatever the previous user of this browser left. Any other failure keeps
 * what the browser has, and says nothing.
 */
export function LoadSavedPreferences() {
  const query = useSetting(USER_PREFERENCES_KEY, "user");
  const [ended] = useState(sessionScope);
  const value = query.data?.value;
  const noneSaved =
    query.error instanceof ApiClientError && query.error.status === 404;

  useEffect(() => {
    if (ended()) return;
    if (value === undefined && !noneSaved) return;
    usePreferencesStore.getState().loadFromJSON(value ?? {});
    applyAccentColor(usePreferencesStore.getState().preferences.accentColor);
  }, [value, noneSaved, ended]);

  return null;
}
