/**
 * SecureStorage wraps expo-secure-store so tokens never touch disk in plaintext.
 * Uses Keychain on iOS and EncryptedSharedPreferences/Keystore on Android.
 *
 * Values are keyed by a short constant namespace so the whole auth state can
 * be wiped with `clearAll()` on logout.
 */

import * as SecureStore from "expo-secure-store";

const KEY_ACCESS_TOKEN = "nx.access_token";
const KEY_REFRESH_TOKEN = "nx.refresh_token";
const KEY_TOKEN_EXPIRES_AT = "nx.token_expires_at";
const KEY_USER = "nx.user";
const KEY_PERMISSIONS = "nx.permissions";
const KEY_SERVER_URL = "nx.server_url";
const KEY_DEVICE_ID = "nx.device_id";
const KEY_BIOMETRIC_ENROLLED = "nx.biometric_enrolled";
const KEY_BIOMETRIC_PROMPT_SHOWN = "nx.biometric_prompt_shown";
const KEY_RECENT_SERVERS = "nx.recent_servers";
const KEY_BACKGROUNDED_AT = "nx.backgrounded_at";

const MAX_RECENT_SERVERS = 5;

const ALL_KEYS = [
  KEY_ACCESS_TOKEN,
  KEY_REFRESH_TOKEN,
  KEY_TOKEN_EXPIRES_AT,
  KEY_USER,
  KEY_PERMISSIONS,
  KEY_BIOMETRIC_ENROLLED,
  KEY_BACKGROUNDED_AT,
] as const;

export interface StoredUser {
  id: string;
  email: string;
  displayName: string;
  role: string;
}

/**
 * We only persist the auth credential bundle via SecureStore. Non-sensitive
 * preferences (server URL, device id, etc.) live here too since SecureStore
 * is the durable key/value API we already depend on.
 */
export const secureStorage = {
  // Access token (JWT, short-lived) ----------------------------------------
  async getAccessToken(): Promise<string | null> {
    return SecureStore.getItemAsync(KEY_ACCESS_TOKEN);
  },
  async setAccessToken(token: string): Promise<void> {
    await SecureStore.setItemAsync(KEY_ACCESS_TOKEN, token);
  },

  // Refresh token (hex, long-lived) ----------------------------------------
  async getRefreshToken(): Promise<string | null> {
    return SecureStore.getItemAsync(KEY_REFRESH_TOKEN);
  },
  async setRefreshToken(token: string): Promise<void> {
    await SecureStore.setItemAsync(KEY_REFRESH_TOKEN, token);
  },

  // Token expiry (unix seconds) --------------------------------------------
  async getTokenExpiresAt(): Promise<number | null> {
    const raw = await SecureStore.getItemAsync(KEY_TOKEN_EXPIRES_AT);
    if (!raw) return null;
    const n = Number(raw);
    return Number.isFinite(n) ? n : null;
  },
  async setTokenExpiresAt(unixSeconds: number): Promise<void> {
    await SecureStore.setItemAsync(KEY_TOKEN_EXPIRES_AT, String(unixSeconds));
  },

  // User profile snapshot --------------------------------------------------
  async getUser(): Promise<StoredUser | null> {
    const raw = await SecureStore.getItemAsync(KEY_USER);
    if (!raw) return null;
    try {
      return JSON.parse(raw) as StoredUser;
    } catch {
      return null;
    }
  },
  async setUser(user: StoredUser): Promise<void> {
    await SecureStore.setItemAsync(KEY_USER, JSON.stringify(user));
  },

  // RBAC permissions list (e.g. ["execute:vm", "view:cluster", ...]).
  // Stored as a JSON string array. Refreshed on every login / TOTP verify
  // / token refresh via persistAuthResponse(). Cleared on logout via
  // clearAll(). Used by usePermissions() to gate UI affordances client-side
  // — the backend re-checks every action, so this is purely for UX.
  async getPermissions(): Promise<string[]> {
    const raw = await SecureStore.getItemAsync(KEY_PERMISSIONS);
    if (!raw) return [];
    try {
      const parsed: unknown = JSON.parse(raw);
      if (
        Array.isArray(parsed) &&
        parsed.every((x): x is string => typeof x === "string")
      ) {
        return parsed;
      }
      return [];
    } catch {
      return [];
    }
  },
  async setPermissions(permissions: string[]): Promise<void> {
    await SecureStore.setItemAsync(KEY_PERMISSIONS, JSON.stringify(permissions));
  },

  // Server URL (not secret but durable) ------------------------------------
  async getServerUrl(): Promise<string | null> {
    return SecureStore.getItemAsync(KEY_SERVER_URL);
  },
  async setServerUrl(url: string): Promise<void> {
    await SecureStore.setItemAsync(KEY_SERVER_URL, url);
  },

  // Recent server URLs (MRU list, used by the server-URL picker) -----------
  //
  // Stored as a JSON array of strings. Intentionally NOT in ALL_KEYS — the
  // list survives sign-out so users don't lose their server bookmarks when
  // they log out. Capped at MAX_RECENT_SERVERS entries; oldest falls off
  // when a new one is added at the head. Still a single-active-session
  // model (tapping a recent URL signs you out of the current server and
  // re-logs into the new one); multi-instance with simultaneous sessions
  // is a separate v1.1 feature.
  async getRecentServerUrls(): Promise<string[]> {
    const raw = await SecureStore.getItemAsync(KEY_RECENT_SERVERS);
    if (!raw) return [];
    try {
      const parsed: unknown = JSON.parse(raw);
      if (
        Array.isArray(parsed) &&
        parsed.every((x): x is string => typeof x === "string")
      ) {
        return parsed.slice(0, MAX_RECENT_SERVERS);
      }
      return [];
    } catch {
      return [];
    }
  },
  async addRecentServerUrl(url: string): Promise<void> {
    const current = await this.getRecentServerUrls();
    // Dedupe: if present, remove from current position so we can re-insert
    // at the head (MRU). Then cap to MAX_RECENT_SERVERS.
    const filtered = current.filter((u) => u !== url);
    const next = [url, ...filtered].slice(0, MAX_RECENT_SERVERS);
    await SecureStore.setItemAsync(KEY_RECENT_SERVERS, JSON.stringify(next));
  },
  async removeRecentServerUrl(url: string): Promise<void> {
    const current = await this.getRecentServerUrls();
    const next = current.filter((u) => u !== url);
    await SecureStore.setItemAsync(KEY_RECENT_SERVERS, JSON.stringify(next));
  },

  // Device ID (stable per install, used for session tagging) --------------
  async getDeviceId(): Promise<string | null> {
    return SecureStore.getItemAsync(KEY_DEVICE_ID);
  },
  async setDeviceId(id: string): Promise<void> {
    await SecureStore.setItemAsync(KEY_DEVICE_ID, id);
  },

  // Biometric enrollment flag ----------------------------------------------
  async isBiometricEnrolled(): Promise<boolean> {
    return (await SecureStore.getItemAsync(KEY_BIOMETRIC_ENROLLED)) === "1";
  },
  async setBiometricEnrolled(enrolled: boolean): Promise<void> {
    if (enrolled) {
      await SecureStore.setItemAsync(KEY_BIOMETRIC_ENROLLED, "1");
    } else {
      await SecureStore.deleteItemAsync(KEY_BIOMETRIC_ENROLLED);
    }
  },

  // "Have we already asked the user whether they want to enable biometric
  // unlock after login?" flag. Deliberately NOT in ALL_KEYS — the answer
  // survives sign-out so a user who tapped "Not now" once doesn't get
  // re-pestered on every login. Users can always enable biometric later
  // via Settings → Security.
  async hasBiometricPromptBeenShown(): Promise<boolean> {
    return (
      (await SecureStore.getItemAsync(KEY_BIOMETRIC_PROMPT_SHOWN)) === "1"
    );
  },
  async setBiometricPromptShown(shown: boolean): Promise<void> {
    if (shown) {
      await SecureStore.setItemAsync(KEY_BIOMETRIC_PROMPT_SHOWN, "1");
    } else {
      await SecureStore.deleteItemAsync(KEY_BIOMETRIC_PROMPT_SHOWN);
    }
  },

  // Background timestamp (unix milliseconds) ------------------------------
  //
  // Persisted to SecureStore so the 5-minute auto-lock survives Android
  // low-memory kills (security review M1). Without this, an in-memory ref
  // gets reset whenever Android tears down the JS bridge while the app is
  // backgrounded — meaning a freshly-killed-and-relaunched app skips the
  // elapsed-time check and lands straight in the authed state. The
  // bootstrap path also picks this up and forces a lock if the threshold
  // was exceeded since the last background.
  async getBackgroundedAt(): Promise<number | null> {
    const raw = await SecureStore.getItemAsync(KEY_BACKGROUNDED_AT);
    if (!raw) return null;
    const n = Number(raw);
    return Number.isFinite(n) ? n : null;
  },
  async setBackgroundedAt(ms: number): Promise<void> {
    await SecureStore.setItemAsync(KEY_BACKGROUNDED_AT, String(ms));
  },
  async clearBackgroundedAt(): Promise<void> {
    await SecureStore.deleteItemAsync(KEY_BACKGROUNDED_AT);
  },

  // Wipe all auth material on logout. Preserves server URL + device id so
  // the user doesn't have to re-enter them after logout.
  async clearAll(): Promise<void> {
    await Promise.all(ALL_KEYS.map((k) => SecureStore.deleteItemAsync(k)));
  },
};
