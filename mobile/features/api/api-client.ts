/**
 * Nexara API client for mobile. Mirrors the proactive-refresh pattern used
 * in the web frontend at frontend/src/lib/api-client.ts, but stores tokens
 * in SecureStore and tags sessions with X-Nexara-Device-* headers so the
 * backend can record them as mobile sessions.
 *
 * Refresh rules:
 *   1. If the access token expires within REFRESH_SKEW_SECONDS, proactively
 *      refresh before the outbound request (dedup'd via in-flight promise).
 *   2. If the backend returns 401 on a call, attempt a single refresh and
 *      retry the call once.
 *   3. If the refresh itself fails, clear SecureStore and emit an auth-lost
 *      event so the UI can kick the user back to the login screen.
 */

import axios, {
  type AxiosError,
  type AxiosInstance,
  type AxiosRequestConfig,
  type InternalAxiosRequestConfig,
} from "axios";

import { getDeviceInfo } from "@/lib/device";
import { secureStorage } from "@/lib/secure-storage";
import type { AuthResponse, LoginResponse } from "./types";

const REFRESH_SKEW_SECONDS = 60;

type AuthLostListener = () => void;
const authLostListeners: AuthLostListener[] = [];

export function onAuthLost(listener: AuthLostListener): () => void {
  authLostListeners.push(listener);
  return () => {
    const i = authLostListeners.indexOf(listener);
    if (i >= 0) authLostListeners.splice(i, 1);
  };
}

function emitAuthLost(): void {
  for (const listener of authLostListeners) listener();
}

/**
 * Listener invoked during `apiLogout` to wipe non-token state that lives
 * outside SecureStore — notably the in-memory and persisted TanStack
 * Query cache, and any transient store data (task tracker, etc.). The
 * root layout registers the cleanup on mount.
 *
 * Security review R2-H4: without this, signing out leaves cluster / VM /
 * alert response bodies in the MMKV query-state file (readable on disk)
 * and briefly shows the previous user's cached data in the UI when a
 * new user logs in on the same device.
 */
type LogoutCleanup = () => void | Promise<void>;
const logoutCleanups: LogoutCleanup[] = [];

export function registerLogoutCleanup(cleanup: LogoutCleanup): () => void {
  logoutCleanups.push(cleanup);
  return () => {
    const i = logoutCleanups.indexOf(cleanup);
    if (i >= 0) logoutCleanups.splice(i, 1);
  };
}

let client: AxiosInstance | null = null;
let refreshPromise: Promise<void> | null = null;

/**
 * URL prefixes that must bypass the refresh / auth-lost flow. These are
 * endpoints the user hits BEFORE they have a valid session (login, TOTP
 * verify, OIDC callback) or that manage the refresh token itself. If we
 * let the response interceptor try to refresh on a 401 from one of
 * these, we end up:
 *   - calling `/auth/refresh` with a missing or dead refresh token,
 *   - emitting `auth-lost`,
 *   - which triggers `logout()` which clears `store.error`,
 *   - wiping the "Bad credentials" message the login screen was about
 *     to show the user (they see a brief flash instead of a stable
 *     error).
 *
 * Keep in sync with the request interceptor's `isPublicAuth` check
 * above. Both lists need to match exactly or the behavior gets
 * asymmetric.
 */
function isPublicAuthPath(url: string | undefined): boolean {
  if (!url) return false;
  return (
    url.startsWith("/auth/login") ||
    url.startsWith("/auth/register") ||
    url.startsWith("/auth/refresh") ||
    url.startsWith("/auth/totp/verify-login") ||
    url.startsWith("/auth/oidc/")
  );
}

async function buildDeviceHeaders(): Promise<Record<string, string>> {
  const info = await getDeviceInfo();
  return {
    "X-Nexara-Device-Type": "mobile",
    "X-Nexara-Device-Name": info.name,
    "X-Nexara-Device-ID": info.id,
  };
}

/**
 * Build (or rebuild) the axios client. Called whenever the server URL changes.
 */
export async function configureApiClient(serverUrl: string): Promise<void> {
  const trimmed = serverUrl.replace(/\/$/, "");
  await secureStorage.setServerUrl(trimmed);

  client = axios.create({
    baseURL: `${trimmed}/api/v1`,
    timeout: 20_000,
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
    },
  });

  client.interceptors.request.use(async (config: InternalAxiosRequestConfig) => {
    // Attach device headers to every request so the backend can tag sessions.
    const device = await buildDeviceHeaders();
    config.headers.set("X-Nexara-Device-Type", device["X-Nexara-Device-Type"]);
    config.headers.set("X-Nexara-Device-Name", device["X-Nexara-Device-Name"]);
    config.headers.set("X-Nexara-Device-ID", device["X-Nexara-Device-ID"]);

    // Skip attaching auth to the auth endpoints that don't need it.
    if (!isPublicAuthPath(config.url)) {
      // Proactive refresh: if the access token expires within the skew, refresh first.
      const expiresAt = await secureStorage.getTokenExpiresAt();
      if (expiresAt && Date.now() / 1000 > expiresAt - REFRESH_SKEW_SECONDS) {
        try {
          await refreshAccessToken();
        } catch {
          // fall through — the outbound call will 401 and the response
          // interceptor will make a final attempt.
        }
      }

      const token = await secureStorage.getAccessToken();
      if (token) {
        config.headers.set("Authorization", `Bearer ${token}`);
      }
    }

    return config;
  });

  client.interceptors.response.use(
    (res) => res,
    async (error: AxiosError) => {
      const original = error.config as
        | (InternalAxiosRequestConfig & { _retried?: boolean })
        | undefined;

      // Skip the refresh-and-retry dance for public auth endpoints. A
      // 401 from /auth/login means "wrong password", not "token expired"
      // — the caller (auth-store.login) already handled it by setting a
      // visible error message. Running the retry anyway would emit
      // `auth-lost` on refresh failure, trigger logout(), and clear the
      // error message the user was about to see.
      if (
        error.response?.status === 401 &&
        original &&
        !original._retried &&
        !isPublicAuthPath(original.url)
      ) {
        original._retried = true;
        try {
          await refreshAccessToken();
          const token = await secureStorage.getAccessToken();
          if (token) {
            original.headers.set("Authorization", `Bearer ${token}`);
          }
          return client!.request(original);
        } catch {
          emitAuthLost();
          throw error;
        }
      }

      throw error;
    },
  );
}

/**
 * Get the configured client, auto-initialising from SecureStore if needed.
 * Throws if no server URL is stored yet (caller should route to setup).
 */
export async function getApiClient(): Promise<AxiosInstance> {
  if (client) return client;

  const serverUrl = await secureStorage.getServerUrl();
  if (!serverUrl) {
    throw new Error("Server URL not configured");
  }
  await configureApiClient(serverUrl);
  return client!;
}

/**
 * Proactive, skew-aware refresh intended for non-axios callers (e.g. the
 * WebSocket store). Behavior:
 *
 *   - Returns null if the app isn't configured with a server URL or has no
 *     access token at all (caller should bail out — there's nothing to
 *     connect with anyway).
 *   - Returns the current access token if it's not within the refresh skew
 *     window (no refresh needed).
 *   - Refreshes the access token via the normal dedup'd `refreshAccessToken`
 *     path when inside the skew window OR when the token is already expired,
 *     then returns the fresh token.
 *   - If the refresh itself fails, emits `auth-lost` and throws. This is
 *     stricter than the axios request interceptor, which swallows proactive
 *     refresh errors because it has a 401-retry fallback. WS has no such
 *     fallback — if the token is dead, we want to kick the user out.
 *
 * Used by `stores/ws-store.ts::connect()` to fix the landmine where the
 * event-stream WS would read a stale token once at connect time, the reconnect
 * loop would fail forever after the token expired, and the only recovery was
 * killing the app.
 */
export async function ensureFreshAccessToken(): Promise<string | null> {
  const [token, expiresAt] = await Promise.all([
    secureStorage.getAccessToken(),
    secureStorage.getTokenExpiresAt(),
  ]);
  if (!token) return null;

  const needsRefresh =
    expiresAt !== null &&
    Date.now() / 1000 > expiresAt - REFRESH_SKEW_SECONDS;

  if (!needsRefresh) return token;

  try {
    await refreshAccessToken();
  } catch (err) {
    emitAuthLost();
    throw err;
  }
  return secureStorage.getAccessToken();
}

/**
 * Dedup'd refresh: multiple concurrent callers share a single refresh call.
 */
async function refreshAccessToken(): Promise<void> {
  if (refreshPromise) return refreshPromise;

  refreshPromise = (async () => {
    try {
      const refreshToken = await secureStorage.getRefreshToken();
      if (!refreshToken) throw new Error("No refresh token");

      // Use a fresh axios instance for refresh to bypass our own interceptors.
      const serverUrl = await secureStorage.getServerUrl();
      if (!serverUrl) throw new Error("No server URL");

      const res = await axios.post<AuthResponse>(
        `${serverUrl}/api/v1/auth/refresh`,
        { refresh_token: refreshToken },
        {
          headers: {
            "Content-Type": "application/json",
            ...(await buildDeviceHeaders()),
          },
          timeout: 15_000,
        },
      );

      await persistAuthResponse(res.data);
    } finally {
      refreshPromise = null;
    }
  })();

  return refreshPromise;
}

/**
 * Store the tokens + user + permissions from a successful auth response.
 */
export async function persistAuthResponse(resp: AuthResponse): Promise<void> {
  await Promise.all([
    secureStorage.setAccessToken(resp.access_token),
    secureStorage.setRefreshToken(resp.refresh_token),
    secureStorage.setTokenExpiresAt(resp.expires_at),
    secureStorage.setUser({
      id: resp.user.id,
      email: resp.user.email,
      displayName: resp.user.display_name,
      role: resp.user.role,
    }),
    secureStorage.setPermissions(resp.permissions),
  ]);
}

// ── Auth endpoint wrappers ──────────────────────────────────────────────────

export async function apiLogin(
  email: string,
  password: string,
): Promise<LoginResponse> {
  const c = await getApiClient();
  const res = await c.post<LoginResponse>("/auth/login", { email, password });
  return res.data;
}

export async function apiVerifyTOTP(
  pendingToken: string,
  code: string,
): Promise<AuthResponse> {
  const c = await getApiClient();
  const res = await c.post<AuthResponse>("/auth/totp/verify-login", {
    totp_pending_token: pendingToken,
    code,
  });
  return res.data;
}

export async function apiLogout(): Promise<void> {
  const c = await getApiClient();
  const refreshToken = await secureStorage.getRefreshToken();
  if (refreshToken) {
    try {
      await c.post("/auth/logout", { refresh_token: refreshToken });
    } catch {
      // Swallow: logging out locally is the priority.
    }
  }
  await secureStorage.clearAll();

  // Wipe non-token local state (query cache on disk + in-memory,
  // task tracker, etc.). Each cleanup is isolated so one failing
  // doesn't prevent the others from running. Security review R2-H4.
  for (const cleanup of logoutCleanups) {
    try {
      await cleanup();
    } catch (err) {
      console.warn("[apiLogout] cleanup failed", err);
    }
  }
}

/**
 * Helper for generic typed GET/POST requests used by feature-specific
 * query hooks.
 */
export async function apiGet<T>(
  path: string,
  config?: AxiosRequestConfig,
): Promise<T> {
  const c = await getApiClient();
  const res = await c.get<T>(path, config);
  return res.data;
}

export async function apiPost<T, B = unknown>(
  path: string,
  body?: B,
  config?: AxiosRequestConfig,
): Promise<T> {
  const c = await getApiClient();
  const res = await c.post<T>(path, body, config);
  return res.data;
}

export async function apiDelete<T>(
  path: string,
  config?: AxiosRequestConfig,
): Promise<T> {
  const c = await getApiClient();
  const res = await c.delete<T>(path, config);
  return res.data;
}
