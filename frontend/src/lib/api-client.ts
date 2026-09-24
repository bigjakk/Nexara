import type { ApiError, AuthResponse } from "@/types/api";
import {
  apiPath,
  assertApiPath,
  PathSegmentError,
  type ApiPath,
} from "@/lib/api-path";

// Cached user metadata for instant render after a hard refresh. Not security
// sensitive — the JWT is the actual auth gate; this is just so the SPA can
// show "Welcome, alice" before /auth/refresh resolves.
const USER_KEY = "nexara_user";

// Legacy localStorage keys used by builds before the cookie migration. We
// purge any value at these keys on storeTokens / clearTokens so existing
// users do not carry their pre-upgrade access/refresh tokens around in
// JS-reachable storage indefinitely.
const LEGACY_TOKEN_KEYS = [
  "access_token",
  "refresh_token",
  "expires_at",
  "user",
] as const;

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
let onAuthRefresh: ((res: AuthResponse) => void) | null = null;
let refreshPromise: Promise<AuthResponse> | null = null;

export function setAuthFailureCallback(cb: () => void) {
  onAuthFailure = cb;
}

/**
 * Registers a callback invoked on every successful background refresh
 * (Finding A11). The auth-store uses this to re-hydrate user + permissions
 * from the refresh response so a permission rotation propagates to the SPA
 * within one access-token lifetime, without forcing a logout.
 */
export function setAuthRefreshCallback(cb: (res: AuthResponse) => void) {
  onAuthRefresh = cb;
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
  // Body is empty — the HttpOnly cookie carries the refresh token, and since
  // v1.9.x that is the only delivery path the server offers. auth: false,
  // because this IS the refresh: resolving an access token for it would start
  // another one.
  const res = await apiFetch(
    apiPath`/api/v1/auth/refresh`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: "{}",
    },
    { auth: false },
  );

  if (!res.ok) {
    clearTokens();
    onAuthFailure?.();
    throw new Error("Refresh failed");
  }

  const data = (await res.json()) as AuthResponse;
  storeTokens(data);
  onAuthRefresh?.(data);
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

/** What apiFetch hands fetch(): a RequestInit whose headers are a plain object. */
type ApiFetchInit = Omit<RequestInit, "headers"> & {
  headers?: Record<string, string>;
};

/**
 * fetch() for an API request apiClient does not make — a file read as a
 * Blob, a multipart upload, a public probe — in the same order as request():
 * the path is re-checked with assertApiPath before anything is sent, because
 * the ApiPath brand is a compile-time type only, and only then is an access
 * token resolved (which may refresh it — a request of its own) and added to
 * `init` as the Authorization header. `auth: false` resolves and sends no
 * token, for the refresh itself and for public endpoints; `init` then reaches
 * fetch() as given.
 *
 * Every fetch the SPA makes goes through here or through request(): ESLint
 * refuses fetch anywhere but this file (eslint.config.js).
 */
export async function apiFetch(
  path: ApiPath,
  init: ApiFetchInit = {},
  { auth = true }: { auth?: boolean } = {},
): Promise<Response> {
  assertApiPath(path);
  if (!auth) {
    return fetch(path, init);
  }
  const token = await ensureValidToken();
  return fetch(
    path,
    token
      ? {
          ...init,
          headers: { ...init.headers, Authorization: `Bearer ${token}` },
        }
      : init,
  );
}

/**
 * The request openApiRequest returns: an XMLHttpRequest, already opened and,
 * when there is a session, carrying its Authorization header, whose type
 * leaves out open().
 * Opening it again would aim that header at a path nothing checked: without
 * open() in the type that does not compile, and a cast that gets past the
 * type meets the request's own open(), which throws.
 */
export type OpenedApiRequest = Omit<XMLHttpRequest, "open">;

/**
 * A new XMLHttpRequest, opened on an API path — for the one request fetch
 * cannot make, an upload that reports its progress — in the same order as
 * apiFetch: the path is checked with assertApiPath, the request opened, and
 * only then an access token resolved (which may refresh it, a request of its
 * own) and set as the Authorization header. The caller adds the rest and
 * sends it.
 *
 * ESLint refuses XMLHttpRequest anywhere but this file (eslint.config.js), so
 * this is the only way to get one. No exported function returns the stored
 * access token, and the three helpers that attach it — this one, request()
 * and apiFetch() — attach it only to a path assertApiPath has passed. That
 * is a promise about where the stored token goes, not about who can obtain
 * one: any module can call an auth endpoint through apiClient and read the
 * token in its answer, as the login and OIDC callback flows do before they
 * hand it to storeTokens().
 */
export async function openApiRequest(
  method: string,
  path: ApiPath,
): Promise<OpenedApiRequest> {
  assertApiPath(path);
  const xhr = new XMLHttpRequest();
  xhr.open(method, path);
  // An own property, neither writable nor configurable, in front of the
  // prototype's open(): the re-aim a cast would allow now throws, and the
  // property can be neither reassigned nor deleted. It stops every spelling
  // that goes through the request. The prototype's own open() —
  // Object.getPrototypeOf(xhr).open.call(xhr, …) — still gets past it, and
  // what makes that harmless is the platform: open() empties the request's
  // author headers (XMLHttpRequest Standard, open(): "Empty this's author
  // request headers"), so a re-aimed request carries no Authorization, and
  // the one credential the browser adds by itself, the refresh cookie, is
  // scoped to /api/v1/auth/ (RefreshCookiePath,
  // internal/api/handlers/auth_cookies.go).
  Object.defineProperty(xhr, "open", {
    value: () => {
      throw new PathSegmentError(
        path,
        "an opened API request cannot be re-opened",
      );
    },
  });
  const token = await ensureValidToken();
  if (token) {
    xhr.setRequestHeader("Authorization", `Bearer ${token}`);
  }
  return xhr;
}

async function request<T>(
  method: string,
  path: ApiPath,
  body?: unknown,
  skipAuth = false,
): Promise<T> {
  // Before the token refresh below, which is itself a request: a path the
  // brand let through by a cast must not cause even that to be sent.
  assertApiPath(path);
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

/**
 * The envelope every collection endpoint returns (Go: handlers.ListResponse).
 * `total` is the count matching the request's filters before limit/offset, so
 * it can exceed `items.length` on a paginated endpoint.
 */
export interface ListResponse<T> {
  items: T[];
  total: number;
}

/**
 * Unwraps a list envelope, throwing if the response is not one.
 *
 * Deliberately strict rather than falling back to `Array.isArray(body)`. A
 * tolerant unwrap would silently paper over an endpoint that never got
 * converted, which is precisely the failure this envelope exists to remove —
 * and it fails quietly, since iterating an object's values yields no error.
 * A thrown error names the path, so a missed endpoint surfaces as a broken
 * query with a usable message instead of an empty list.
 */
function unwrapList<T>(path: string, body: unknown): ListResponse<T> {
  if (
    typeof body === "object" &&
    body !== null &&
    Array.isArray((body as ListResponse<T>).items)
  ) {
    return body as ListResponse<T>;
  }
  throw new Error(
    `GET ${path}: expected a {items,total} list envelope, got ${
      Array.isArray(body) ? "a bare array" : typeof body
    }`,
  );
}

/**
 * Every method takes an {@link ApiPath}, which only the apiPath tag
 * (lib/api-path.ts) produces: a path whose interpolated values were each
 * checked to be one segment and encoded. A path built any other way — a
 * plain template literal, a concatenation — does not type-check.
 */
export const apiClient = {
  get: <T>(path: ApiPath) => request<T>("GET", path),
  /**
   * GET a collection, returning just the rows. The common case — reach for
   * `page` instead when the caller needs `total` for pagination.
   */
  list: async <T>(path: ApiPath): Promise<T[]> =>
    unwrapList<T>(path, await request<unknown>("GET", path)).items,
  /** GET a collection with its total, for paginated views. */
  page: async <T>(path: ApiPath): Promise<ListResponse<T>> =>
    unwrapList<T>(path, await request<unknown>("GET", path)),
  post: <T>(path: ApiPath, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: ApiPath, body?: unknown) => request<T>("PUT", path, body),
  patch: <T>(path: ApiPath, body?: unknown) => request<T>("PATCH", path, body),
  delete: <T>(path: ApiPath, body?: unknown) =>
    request<T>("DELETE", path, body),
  postPublic: <T>(path: ApiPath, body?: unknown) =>
    request<T>("POST", path, body, true),
  getPublic: <T>(path: ApiPath) => request<T>("GET", path, undefined, true),
};

export { ApiClientError };
