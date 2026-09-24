import { vi } from "vitest";

/**
 * Replaces the global fetch with a recorder that answers GETs from `reads`
 * (keyed by exact URL) and every other method with 204. It records each
 * request apiClient actually sent as "METHOD url", the auth refresh aside, so
 * a test asserts on the wire rather than on a mocked hook.
 *
 * Undo with vi.unstubAllGlobals().
 */
export function stubApi(reads: Record<string, unknown>): {
  sent: string[];
  /** Every request that was not a GET. */
  writes: () => string[];
} {
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
      // No session in a test: the refresh fails and the request goes out
      // without a token, which the stub does not check.
      if (url === "/api/v1/auth/refresh") {
        return Promise.resolve(new Response("{}", { status: 401 }));
      }
      const method = init?.method ?? "GET";
      sent.push(`${method} ${url}`);
      if (method !== "GET") {
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (!(url in reads)) {
        return Promise.resolve(
          new Response(JSON.stringify({ error: `unstubbed GET ${url}` }), {
            status: 404,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      return Promise.resolve(
        new Response(JSON.stringify(reads[url]), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }),
  );
  return {
    sent,
    writes: () => sent.filter((r) => !r.startsWith("GET ")),
  };
}

/** A {items,total} list envelope, the shape every collection endpoint returns. */
export function listOf<T>(items: T[]): { items: T[]; total: number } {
  return { items, total: items.length };
}
