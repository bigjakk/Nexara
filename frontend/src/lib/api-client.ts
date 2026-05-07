import type {
  ApiError,
  AuthResponse,
} from "@/types/api";

// Cached user metadata for instant render after a hard refresh. Not security
// sensitive — the JWT is the actual auth gate; this is just so the SPA can
// show "Welcome, alice" before /auth/refresh resolves.
const USER_KEY = "nexara_user";

// Legacy localStorage keys used by builds before the cookie migration. We
// purge any value at these keys on storeTokens / clearTokens so existing
// users do not carry their pre-upgrade access/refresh tokens around in
// JS-reachable storage indefinitely.
const LEGACY_TOKEN_KEYS = ["access_token", "refresh_token", "expires_at", "user"] as const;

function purgeLegacyTokenKeys() {
  for (const key of LEGACY_TOKEN_KEYS) {
    localStorage.removeItem(key);
  }
}

// Access token lives in this module's closure only — never written to
// localStorage or any DOM-reachable storage. The HttpOnly refresh cookie set
// by the server is the persistent auth artefact across reloads.
let accessTokenInMemory: string | null = null;
let accessTokenExpiresAt = 0;

// One-shot legacy cleanup on module load — covers the SPA boot path before
// any login/refresh runs.
purgeLegacyTokenKeys();

let onAuthFailure: (() => void) | null = null;
let refreshPromise: Promise<AuthResponse> | null = null;

export function setAuthFailureCallback(cb: () => void) {
  onAuthFailure = cb;
}

export function getAccessToken(): string | null {
  return accessTokenInMemory;
}

/**
 * Returns a non-stale access token, triggering a cookie-backed refresh if the
 * current one is missing or about to expire. Use this for fetch/XHR calls that
 * bypass `apiClient` (downloads, uploads, WebSocket URLs) so they participate
 * in the same refresh dedupe logic.
 */
export async function getValidAccessToken(): Promise<string | null> {
  return ensureValidToken();
}

export function storeTokens(res: AuthResponse) {
  accessTokenInMemory = res.access_token;
  accessTokenExpiresAt = res.expires_at;
  localStorage.setItem(USER_KEY, JSON.stringify(res.user));
  purgeLegacyTokenKeys();
}

export function clearTokens() {
  accessTokenInMemory = null;
  accessTokenExpiresAt = 0;
  localStorage.removeItem(USER_KEY);
  purgeLegacyTokenKeys();
}

export function getStoredUser() {
  const raw = localStorage.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as AuthResponse["user"];
  } catch {
    return null;
  }
}

async function refreshTokens(): Promise<AuthResponse> {
  // Body is empty — the HttpOnly cookie carries the refresh token. The mobile
  // shell sends `X-Nexara-Device-Type: mobile` and a body refresh_token; web
  // clients rely on the cookie alone.
  const res = await fetch("/api/v1/auth/refresh", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: "{}",
  });

  if (!res.ok) {
    clearTokens();
    onAuthFailure?.();
    throw new Error("Refresh failed");
  }

  const data = (await res.json()) as AuthResponse;
  storeTokens(data);
  return data;
}

function refreshOnce(): Promise<AuthResponse> {
  if (!refreshPromise) {
    refreshPromise = refreshTokens().finally(() => {
      refreshPromise = null;
    });
  }
  return refreshPromise;
}

async function ensureValidToken(): Promise<string | null> {
  // First call after page load — try a cookie-based refresh to populate memory.
  if (!accessTokenInMemory) {
    try {
      const r = await refreshOnce();
      return r.access_token;
    } catch {
      return null;
    }
  }

  // Proactively refresh if the token expires within 60 seconds.
  const now = Math.floor(Date.now() / 1000);
  if (accessTokenExpiresAt > 0 && accessTokenExpiresAt - now < 60) {
    try {
      const r = await refreshOnce();
      return r.access_token;
    } catch {
      return null;
    }
  }

  return accessTokenInMemory;
}

class ApiClientError extends Error {
  constructor(
    public status: number,
    public body: ApiError,
  ) {
    super(body.message);
    this.name = "ApiClientError";
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  skipAuth = false,
): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };

  if (!skipAuth) {
    const token = await ensureValidToken();
    if (token) {
      headers["Authorization"] = `Bearer ${token}`;
    }
  }

  const serializedBody = body != null ? JSON.stringify(body) : null;

  let res = await fetch(path, {
    method,
    headers,
    body: serializedBody,
    credentials: "same-origin",
  });

  // 401 retry with refresh (single attempt)
  if (res.status === 401 && !skipAuth) {
    try {
      const refreshResult = await refreshOnce();
      headers["Authorization"] = `Bearer ${refreshResult.access_token}`;
      res = await fetch(path, {
        method,
        headers,
        body: serializedBody,
        credentials: "same-origin",
      });
    } catch {
      clearTokens();
      onAuthFailure?.();
      throw new ApiClientError(401, {
        error: "unauthorized",
        message: "Session expired",
      });
    }
  }

  if (!res.ok) {
    let errorBody: ApiError;
    try {
      errorBody = (await res.json()) as ApiError;
    } catch {
      errorBody = {
        error: "unknown",
        message: res.statusText,
      };
    }
    throw new ApiClientError(res.status, errorBody);
  }

  // Handle 204 No Content (e.g. DELETE responses)
  if (res.status === 204 || res.headers.get("content-length") === "0") {
    return undefined as T;
  }

  return (await res.json()) as T;
}

export const apiClient = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  delete: <T>(path: string, body?: unknown) => request<T>("DELETE", path, body),
  postPublic: <T>(path: string, body?: unknown) =>
    request<T>("POST", path, body, true),
  getPublic: <T>(path: string) => request<T>("GET", path, undefined, true),
};

export { ApiClientError };
