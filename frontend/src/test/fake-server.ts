import { vi } from "vitest";
import type { AuthResponse, User } from "@/types/api";

/**
 * A small server for tests that run the real api-client and auth store, with
 * only fetch replaced. Answers are per exact "METHOD url" key; an answer may be
 * a Promise a test holds back, which is how a request is kept in flight while
 * the session around it ends.
 *
 * Undo with vi.unstubAllGlobals().
 */

export type Route = (
  init: RequestInit | undefined,
) => Response | Promise<Response>;

export interface FakeServer {
  /** Keyed "METHOD /api/v1/path". An unstubbed request is answered 404. */
  routes: Record<string, Route>;
  /** Every request the SPA sent, as "METHOD url", in order. */
  sent: string[];
  /** How many times the SPA sent exactly this request. */
  times: (key: string) => number;
}

export function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

export function installFakeServer(): FakeServer {
  const routes: Record<string, Route> = {};
  const sent: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      const key = `${init?.method ?? "GET"} ${url}`;
      sent.push(key);
      const route = routes[key];
      return Promise.resolve(
        route
          ? route(init)
          : json({ error: "unstubbed", message: `unstubbed ${key}` }, 404),
      );
    }),
  );
  return {
    routes,
    sent,
    times: (key) => sent.filter((k) => k === key).length,
  };
}

/**
 * What the server answers a sign-in or a refresh with. The access token names
 * its user, so a route can tell whose request it is (callerOf).
 */
export function authResponse(
  user: User,
  {
    permissions = [],
    expiresIn = 3600,
  }: { permissions?: string[]; expiresIn?: number } = {},
): AuthResponse {
  return {
    user,
    access_token: `token-${user.id}`,
    refresh_token: "",
    expires_at: Math.floor(Date.now() / 1000) + expiresIn,
    permissions,
  };
}

/** Whose request this is, read from the token the server issued them. */
export function callerOf(init: RequestInit | undefined): string {
  const headers = init?.headers as Record<string, string> | undefined;
  return (headers?.["Authorization"] ?? "").replace("Bearer token-", "");
}

export function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

export const sleep = (ms: number) =>
  new Promise<void>((r) => setTimeout(r, ms));

/** Lets settled promises run their continuations, and TanStack its notifications. */
export async function flush() {
  await sleep(0);
  await sleep(0);
}

export const ADMIN: User = {
  id: "user-admin",
  email: "admin@example.com",
  display_name: "Admin",
  role: "admin",
};

export const VIEWER: User = {
  id: "user-viewer",
  email: "viewer@example.com",
  display_name: "Viewer",
  role: "user",
};
