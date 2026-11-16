import { useEffect, useRef, useState } from "react";
import { ApiClientError, sessionScope } from "@/lib/api-client";
import { applyAccentColor } from "@/lib/accent";
import { usePreferencesStore } from "@/stores/preferences-store";
import { useThemeStore } from "@/stores/theme-store";
import {
  useSetting,
  useUpsertSetting,
} from "@/features/settings/api/settings-queries";

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
 *
 * The theme is saved here too, whichever control changed it (the shell's
 * toggle, Appearance, the search palette), but only once this user's row has
 * been applied: saving before then would write the previous user's local copy
 * over this user's row.
 */
export function LoadSavedPreferences() {
  const query = useSetting(USER_PREFERENCES_KEY, "user");
  const upsert = useUpsertSetting();
  const [ended] = useState(sessionScope);
  const applied = useRef(false);
  const value = query.data?.value;
  const noneSaved =
    query.error instanceof ApiClientError && query.error.status === 404;

  useEffect(() => {
    if (ended()) return;
    if (value === undefined && !noneSaved) return;
    const store = usePreferencesStore.getState();
    store.loadFromJSON(value ?? {});
    const { accentColor, theme } = usePreferencesStore.getState().preferences;
    if (theme) useThemeStore.getState().setMode(theme);
    else applyAccentColor(accentColor);
    applied.current = true;
  }, [value, noneSaved, ended]);

  const save = upsert.mutate;
  useEffect(
    () =>
      useThemeStore.subscribe(({ mode }) => {
        const prefs = usePreferencesStore.getState();
        if (!applied.current || ended() || prefs.preferences.theme === mode) {
          return;
        }
        prefs.setPreferences({ theme: mode });
        save({
          key: USER_PREFERENCES_KEY,
          value: usePreferencesStore.getState().preferences,
          scope: "user",
        });
      }),
    [save, ended],
  );

  return null;
}
