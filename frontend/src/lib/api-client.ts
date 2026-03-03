import type {
  ApiError,
  AuthResponse,
  RefreshRequest,
} from "@/types/api";

const TOKEN_KEY = "access_token";
const REFRESH_KEY = "refresh_token";
const EXPIRES_KEY = "expires_at";
const USER_KEY = "user";

let onAuthFailure: (() => void) | null = null;
let refreshPromise: Promise<AuthResponse> | null = null;

export function setAuthFailureCallback(cb: () => void) {
  onAuthFailure = cb;
}

function getStoredToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

function getStoredRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_KEY);
}

function getStoredExpiresAt(): number {
  const v = localStorage.getItem(EXPIRES_KEY);
  return v ? Number(v) : 0;
}

export function storeTokens(res: AuthResponse) {
  localStorage.setItem(TOKEN_KEY, res.access_token);
  localStorage.setItem(REFRESH_KEY, res.refresh_token);
  localStorage.setItem(EXPIRES_KEY, String(res.expires_at));
  localStorage.setItem(USER_KEY, JSON.stringify(res.user));
}

export function clearTokens() {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(REFRESH_KEY);
  localStorage.removeItem(EXPIRES_KEY);
  localStorage.removeItem(USER_KEY);
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
  const refreshToken = getStoredRefreshToken();
  if (!refreshToken) {
    throw new Error("No refresh token");
  }

  const body: RefreshRequest = { refresh_token: refreshToken };
  const res = await fetch("/api/v1/auth/refresh", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
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

async function ensureValidToken(): Promise<string | null> {
  const token = getStoredToken();
  if (!token) return null;

  const expiresAt = getStoredExpiresAt();
  const now = Math.floor(Date.now() / 1000);

  // Proactively refresh if token expires within 60 seconds
  if (expiresAt > 0 && expiresAt - now < 60) {
    try {
      // Deduplicate concurrent refresh calls
      if (!refreshPromise) {
        refreshPromise = refreshTokens().finally(() => {
          refreshPromise = null;
        });
      }
      const result = await refreshPromise;
      return result.access_token;
    } catch {
      return null;
    }
  }

  return token;
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
  });

  // 401 retry with refresh (single attempt)
  if (res.status === 401 && !skipAuth) {
    try {
      if (!refreshPromise) {
        refreshPromise = refreshTokens().finally(() => {
          refreshPromise = null;
        });
      }
      const refreshResult = await refreshPromise;
      headers["Authorization"] = `Bearer ${refreshResult.access_token}`;
      res = await fetch(path, {
        method,
        headers,
        body: serializedBody,
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

  return (await res.json()) as T;
}

export const apiClient = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  delete: <T>(path: string) => request<T>("DELETE", path),
  postPublic: <T>(path: string, body?: unknown) =>
    request<T>("POST", path, body, true),
  getPublic: <T>(path: string) => request<T>("GET", path, undefined, true),
};

export { ApiClientError };
