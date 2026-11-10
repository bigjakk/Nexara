import { vi } from "vitest";

/**
 * Replaces the global fetch with a recorder that answers GETs from `reads`
 * (keyed by exact URL), a write named in `writes` (keyed "METHOD url") with
 * that body, and every other method with 204. It records each request
 * apiClient actually sent as "METHOD url", the auth refresh aside, so a test
 * asserts on the wire rather than on a mocked hook.
 *
 * Undo with vi.unstubAllGlobals().
 */
export function stubApi(
  reads: Record<string, unknown>,
  writes: Record<string, unknown> = {},
): {
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
      const request = `${method} ${url}`;
      sent.push(request);
      if (method !== "GET") {
        if (request in writes) {
          return Promise.resolve(
            new Response(JSON.stringify(writes[request]), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            }),
          );
        }
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

/** The body of each request of `method` to `url` sent through stubApi. */
export function bodiesOf(method: string, url: string): unknown[] {
  return vi
    .mocked(globalThis.fetch)
    .mock.calls.filter(
      ([input, init]) => input === url && init?.method === method,
    )
    .map(([, init]) => {
      // apiClient sends every body as a JSON string.
      if (typeof init?.body !== "string") {
        throw new Error(`${method} ${url} was sent without a JSON body`);
      }
      return JSON.parse(init.body) as unknown;
    });
}

/** A {items,total} list envelope, the shape every collection endpoint returns. */
export function listOf<T>(items: T[]): { items: T[]; total: number } {
  return { items, total: items.length };
}
