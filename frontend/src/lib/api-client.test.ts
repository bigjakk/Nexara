import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
  type MockInstance,
} from "vitest";

import type { ApiPath } from "./api-path";
import type { AuthResponse } from "@/types/api";

/**
 * The runtime half of the ApiPath brand, at the three places a request
 * leaves the SPA: request() behind every apiClient method, apiFetch() for the
 * downloads and uploads apiClient does not make, and openApiRequest() for the
 * upload that needs an XMLHttpRequest. A cast gets any value past the
 * compile-time brand; these refuse it before anything is sent — the token
 * refresh that request() and apiFetch() would otherwise send first included.
 */

// A forged path: what `as ApiPath` lets through the type checker. No apiPath
// template can produce it — the tag refuses a ".." value.
const FORGED = "/api/v1/clusters/c/pools/.." as ApiPath;
// Not even a string: what `as never` lets through.
const NOT_A_STRING = 42 as never;

const TOKEN = "access-token-01";
const REFRESH = "/api/v1/auth/refresh";

let fetchSpy: MockInstance<typeof fetch>;
// The modules as a page load finds them, a copy of each per test (below).
let api: typeof import("./api-client");
let apiPath: typeof import("./api-path").apiPath;
let PathSegmentError: typeof import("./api-path").PathSegmentError;

beforeEach(async () => {
  // A page load, before auth-store's initialize() has run: no access token in
  // memory and no session ended in it yet, so request() and apiFetch() would
  // refresh one — a request of its own — before sending anything. clearTokens()
  // is not that state: it records that a session ended, after which nothing is
  // resumed off the refresh cookie.
  vi.resetModules();
  api = await import("./api-client");
  ({ apiPath, PathSegmentError } = await import("./api-path"));
  fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(() =>
    Promise.resolve(
      new Response('{"version":"dev"}', {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ),
  );
});

afterEach(() => {
  vi.restoreAllMocks();
  api.clearTokens();
});

/** A signed-in session: an access token that is nowhere near expiry. */
function signIn() {
  api.storeTokens({
    access_token: TOKEN,
    refresh_token: "",
    expires_at: Math.floor(Date.now() / 1000) + 3600,
    permissions: [],
    user: { id: "u1", email: "user01@example.com" },
  } as unknown as AuthResponse);
}

describe("request()", () => {
  it("refuses a forged path before sending anything, the token refresh included", async () => {
    await expect(api.apiClient.get(FORGED)).rejects.toBeInstanceOf(
      PathSegmentError,
    );
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("sends a path the tag built", async () => {
    await api.apiClient.getPublic(apiPath`/api/v1/version`);
    expect(fetchSpy.mock.calls.map((call) => call[0])).toEqual([
      "/api/v1/version",
    ]);
  });
});

describe("apiFetch()", () => {
  it("refuses a forged path before sending anything, the token refresh included", async () => {
    await expect(api.apiFetch(FORGED)).rejects.toBeInstanceOf(PathSegmentError);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("refuses a path that is not a string with the same error", async () => {
    await expect(api.apiFetch(NOT_A_STRING)).rejects.toBeInstanceOf(
      PathSegmentError,
    );
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("with auth: false, still refuses a forged path before sending it", async () => {
    // The branch the token refresh and the version probe take. It sends
    // without resolving a token, but not without the check.
    await expect(
      api.apiFetch(FORGED, {}, { auth: false }),
    ).rejects.toBeInstanceOf(PathSegmentError);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("sends the init it is given, with the session's access token added", async () => {
    signIn();
    const init = {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin" as const,
      body: "{}",
    };
    await api.apiFetch(apiPath`/api/v1/settings/branding/logo`, init);
    expect(fetchSpy.mock.calls).toEqual([
      [
        "/api/v1/settings/branding/logo",
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${TOKEN}`,
          },
          credentials: "same-origin",
          body: "{}",
        },
      ],
    ]);
  });

  it("with auth: false, sends the init exactly as given and resolves no token", async () => {
    // No token in memory: resolving one would send a refresh first.
    const init = {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin" as const,
      body: "{}",
    };
    await api.apiFetch(apiPath`/api/v1/auth/refresh`, init, { auth: false });
    expect(fetchSpy.mock.calls).toEqual([["/api/v1/auth/refresh", init]]);
  });

  it("on a fresh page, sends no Authorization header when no session can be had", async () => {
    fetchSpy.mockImplementation((input) =>
      Promise.resolve(
        input === REFRESH
          ? new Response("{}", { status: 401 })
          : new Response("{}", { status: 200 }),
      ),
    );
    await api.apiFetch(apiPath`/api/v1/version`, {
      credentials: "same-origin",
    });
    // The refresh came first — and after the path was checked — and failed;
    // the request went out without a token rather than with "Bearer null".
    expect(fetchSpy.mock.calls.map((call) => call[0])).toEqual([
      REFRESH,
      "/api/v1/version",
    ]);
    expect(fetchSpy.mock.calls[1]?.[1]).toEqual({
      credentials: "same-origin",
    });
  });

  it("once a session has ended, sends no refresh and no Authorization header", async () => {
    signIn();
    api.clearTokens();
    fetchSpy.mockImplementation(() =>
      Promise.resolve(new Response("{}", { status: 200 })),
    );
    await api.apiFetch(apiPath`/api/v1/version`, {
      credentials: "same-origin",
    });
    // The refresh cookie may still be good, but nobody signed in here may use it
    // to sign the session that ended back in.
    expect(fetchSpy.mock.calls.map((call) => call[0])).toEqual([
      "/api/v1/version",
    ]);
    expect(fetchSpy.mock.calls[0]?.[1]).toEqual({
      credentials: "same-origin",
    });
  });
});

describe("openApiRequest()", () => {
  /** Watches every XMLHttpRequest's open(), without jsdom acting on it. */
  function spyOnOpen() {
    return vi
      .spyOn(XMLHttpRequest.prototype, "open")
      .mockImplementation(() => undefined);
  }

  /** Watches every header set on an XMLHttpRequest. */
  function spyOnHeaders() {
    return vi
      .spyOn(XMLHttpRequest.prototype, "setRequestHeader")
      .mockImplementation(() => undefined);
  }

  it("refuses a forged path before opening a request or refreshing a token", async () => {
    const openSpy = spyOnOpen();
    await expect(api.openApiRequest("POST", FORGED)).rejects.toBeInstanceOf(
      PathSegmentError,
    );
    await expect(
      api.openApiRequest("POST", NOT_A_STRING),
    ).rejects.toBeInstanceOf(PathSegmentError);
    expect(openSpy).not.toHaveBeenCalled();
    // No session is held, so a token would have meant a refresh request.
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("opens a path the tag built, on the request it returns", async () => {
    const openSpy = spyOnOpen();
    const xhr = await api.openApiRequest("POST", apiPath`/api/v1/version`);
    expect(xhr).toBeInstanceOf(XMLHttpRequest);
    expect(openSpy.mock.calls).toEqual([["POST", "/api/v1/version"]]);
    // The same object, not merely an equal one: two XMLHttpRequests compare
    // equal field by field.
    expect(openSpy.mock.contexts).toHaveLength(1);
    expect(openSpy.mock.contexts[0]).toBe(xhr);
  });

  it("sets the session's Authorization header on the request, after opening it", async () => {
    signIn();
    const openSpy = spyOnOpen();
    const headerSpy = spyOnHeaders();
    const xhr = await api.openApiRequest("POST", apiPath`/api/v1/version`);
    expect(headerSpy.mock.calls).toEqual([
      ["Authorization", `Bearer ${TOKEN}`],
    ]);
    expect(headerSpy.mock.contexts[0]).toBe(xhr);
    // An XMLHttpRequest takes headers only once it is open.
    expect(openSpy.mock.invocationCallOrder[0]).toBeLessThan(
      headerSpy.mock.invocationCallOrder[0] ?? 0,
    );
  });

  it("on a fresh page, sets no Authorization header when no session can be had", async () => {
    fetchSpy.mockImplementation(() =>
      Promise.resolve(new Response("{}", { status: 401 })),
    );
    spyOnOpen();
    const headerSpy = spyOnHeaders();
    await api.openApiRequest("POST", apiPath`/api/v1/version`);
    // The refresh was tried, and failed; nothing was set rather than
    // "Bearer null".
    expect(fetchSpy.mock.calls.map((call) => call[0])).toEqual([REFRESH]);
    expect(headerSpy).not.toHaveBeenCalled();
  });

  it("once a session has ended, tries no refresh and sets no Authorization header", async () => {
    signIn();
    api.clearTokens();
    spyOnOpen();
    const headerSpy = spyOnHeaders();
    await api.openApiRequest("POST", apiPath`/api/v1/version`);
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(headerSpy).not.toHaveBeenCalled();
  });

  it("returns a request that refuses to be re-opened, by its type and at runtime", async () => {
    const openSpy = spyOnOpen();
    const xhr = await api.openApiRequest("POST", apiPath`/api/v1/version`);
    // @ts-expect-error -- open() is left out of the returned type: opening the request again would point it at a path assertApiPath never saw. tsc -b fails here if it comes back.
    const reopen: unknown = xhr.open;
    expect(reopen).toBeTypeOf("function");
    // A cast gets past the type; the request itself refuses.
    expect(() => {
      (xhr as XMLHttpRequest).open("DELETE", "/api/v1/clusters/c1");
    }).toThrow(PathSegmentError);
    expect(openSpy.mock.calls).toEqual([["POST", "/api/v1/version"]]);
    // And the refusal cannot itself be swapped out or removed: the request's
    // own open() is neither writable nor configurable.
    const descriptor = Object.getOwnPropertyDescriptor(xhr, "open");
    expect(descriptor?.writable).toBe(false);
    expect(descriptor?.configurable).toBe(false);
    expect(() => {
      (xhr as XMLHttpRequest).open = () => undefined;
    }).toThrow(TypeError);
    expect(
      Reflect.deleteProperty(xhr as unknown as Record<string, unknown>, "open"),
    ).toBe(false);
  });
});
