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
import { ADMIN, authResponse } from "@/test/fake-server";
import { freshClient, type Client } from "@/test/api-client-harness";

/**
 * The runtime half of the ApiPath brand, at the three places a request leaves the
 * SPA: request() behind every apiClient method, apiFetch(), and openApiRequest()
 * for the upload that needs an XMLHttpRequest. A cast gets any value past the
 * compile-time brand; these refuse it before anything is sent.
 */

// What `as ApiPath` lets through the type checker. No apiPath template can
// produce it: the tag refuses a ".." value.
const FORGED = "/api/v1/clusters/c/pools/.." as ApiPath;
// Not even a string: what `as never` lets through.
const NOT_A_STRING = 42 as never;

const TOKEN = `token-${ADMIN.id}`;
const REFRESH_URL = "/api/v1/auth/refresh";

let fetchSpy: MockInstance<typeof fetch>;
// The modules as a page load finds them, a fresh copy per test (freshClient):
// no token held and no session ended, so request() and apiFetch() refresh one first.
let api: Client;
let apiPath: typeof import("./api-path").apiPath;
let PathSegmentError: typeof import("./api-path").PathSegmentError;

beforeEach(async () => {
  api = await freshClient();
  ({ apiPath, PathSegmentError } = await import("./api-path"));
  // Everything is answered with the version document but the refresh: a fresh
  // page has no cookie to resume, and the server says so with a 401.
  fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation((input) =>
    Promise.resolve(
      input === REFRESH_URL
        ? new Response("{}", { status: 401 })
        : new Response('{"version":"dev"}', {
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
  api.storeTokens(authResponse(ADMIN));
}

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

describe("a path the tag did not build", () => {
  it.each<[string, () => Promise<unknown>]>([
    ["request()", () => api.apiClient.get(FORGED)],
    ["apiFetch()", () => api.apiFetch(FORGED)],
    [
      "apiFetch() with a path that is not a string",
      () => api.apiFetch(NOT_A_STRING),
    ],
    [
      // The branch the token refresh and the version probe take: it sends
      // without resolving a token, but not without the check.
      "apiFetch() with auth: false",
      () => api.apiFetch(FORGED, {}, { auth: false }),
    ],
    ["openApiRequest()", () => api.openApiRequest("POST", FORGED)],
    [
      "openApiRequest() with a path that is not a string",
      () => api.openApiRequest("POST", NOT_A_STRING),
    ],
  ])(
    "is refused by %s before anything is sent, the token refresh included",
    async (_name, send) => {
      const openSpy = spyOnOpen();

      await expect(send()).rejects.toBeInstanceOf(PathSegmentError);

      expect(fetchSpy).not.toHaveBeenCalled(); // no session is held: a token would have meant a refresh
      expect(openSpy).not.toHaveBeenCalled();
    },
  );
});

describe("request()", () => {
  it("sends a path the tag built", async () => {
    await api.apiClient.getPublic(apiPath`/api/v1/version`);
    expect(fetchSpy.mock.calls.map((call) => call[0])).toEqual([
      "/api/v1/version",
    ]);
  });

  it("sends a public request with no Authorization header, though a session is held: postPublic and getPublic are what login and register go through", async () => {
    signIn();

    await api.apiClient.postPublic(apiPath`/api/v1/pubp`, {});
    await api.apiClient.getPublic(apiPath`/api/v1/pub`);
    // The control: the same session's ordinary request carries the header, so
    // its absence above is not the probe being blind to it.
    await api.apiClient.get(apiPath`/api/v1/priv`);

    expect(
      fetchSpy.mock.calls.map(([input, init]) => [
        input,
        (init?.headers as Record<string, string> | undefined)?.[
          "Authorization"
        ],
      ]),
    ).toEqual([
      ["/api/v1/pubp", undefined],
      ["/api/v1/pub", undefined],
      ["/api/v1/priv", `Bearer ${TOKEN}`],
    ]);
  });
});

describe("request(), when the answer is not OK", () => {
  /** What a read of /api/v1/version fails with, given what the server answered. */
  async function failureOf(init: ResponseInit, body: string) {
    fetchSpy.mockImplementation(() =>
      Promise.resolve(new Response(body, init)),
    );
    return api.apiClient.getPublic(apiPath`/api/v1/version`).then(
      () => undefined,
      (err: unknown) => err,
    );
  }

  // A 500 whose body parses, but is no {error, message} envelope: what a proxy or
  // a WAF may send. Each has to come out as an ApiClientError of its own status;
  // `null` used to throw a TypeError out of the error's own constructor.
  it.each([
    ["JSON null", "null"],
    ["a JSON string", '"oops"'],
    ["a JSON number", "42"],
    ["a JSON array", "[]"],
    ["an object with no message", '{"error":"internal_server_error"}'],
    ["an object whose message is not a string", '{"error":"x","message":7}'],
  ])(
    "an ApiClientError of the status, with its text as the message, for %s as the body",
    async (_name, body) => {
      const err = await failureOf(
        { status: 500, statusText: "Internal Server Error" },
        body,
      );

      expect(err).toBeInstanceOf(api.ApiClientError);
      expect(err).toMatchObject({
        status: 500,
        message: "Internal Server Error",
        body: { error: "unknown", message: "Internal Server Error" },
      });
    },
  );

  it("control: keeps the server's own envelope, details and all", async () => {
    const err = await failureOf(
      { status: 422, statusText: "Unprocessable Content" },
      JSON.stringify({
        error: "confirm_required",
        message: "Are you sure?",
        details: { target: "linux01" },
      }),
    );

    expect(err).toBeInstanceOf(api.ApiClientError);
    expect(err).toMatchObject({
      status: 422,
      message: "Are you sure?",
      body: {
        error: "confirm_required",
        message: "Are you sure?",
        details: { target: "linux01" },
      },
    });
  });

  it("control: a message that is the empty string is still the server's, and not replaced by the status text", async () => {
    const err = await failureOf(
      { status: 500, statusText: "Internal Server Error" },
      '{"error":"internal_server_error","message":""}',
    );

    expect(err).toBeInstanceOf(api.ApiClientError);
    expect(err).toMatchObject({
      status: 500,
      message: "",
      body: { error: "internal_server_error", message: "" },
    });
  });
});

describe("apiFetch()", () => {
  const initOf = () => ({
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin" as const,
    body: "{}",
  });

  it("sends the init it is given, with the session's access token added", async () => {
    signIn();
    const init = initOf();
    await api.apiFetch(apiPath`/api/v1/settings/branding/logo`, init);
    expect(fetchSpy.mock.calls).toEqual([
      [
        "/api/v1/settings/branding/logo",
        {
          ...init,
          headers: { ...init.headers, Authorization: `Bearer ${TOKEN}` },
        },
      ],
    ]);
  });

  it("with auth: false, sends the init exactly as given and resolves no token", async () => {
    // No token in memory: resolving one would send a refresh first.
    const init = initOf();
    await api.apiFetch(apiPath`/api/v1/auth/refresh`, init, { auth: false });
    expect(fetchSpy.mock.calls).toEqual([[REFRESH_URL, init]]);
  });

  it("on a fresh page, sends no Authorization header when no session can be had", async () => {
    await api.apiFetch(apiPath`/api/v1/version`, {
      credentials: "same-origin",
    });
    // The refresh came first (after the path was checked) and failed; the request
    // went out without a token rather than with "Bearer null".
    expect(fetchSpy.mock.calls.map((call) => call[0])).toEqual([
      REFRESH_URL,
      "/api/v1/version",
    ]);
    expect(fetchSpy.mock.calls[1]?.[1]).toEqual({
      credentials: "same-origin",
    });
  });

  it("once a session has ended, sends no refresh and no Authorization header", async () => {
    signIn();
    api.clearTokens();
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
    // The refresh was tried, and failed; nothing was set rather than "Bearer null".
    expect(fetchSpy.mock.calls.map((call) => call[0])).toEqual([REFRESH_URL]);
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
    // And the refusal cannot itself be swapped out or removed: the request's own
    // open() is neither writable nor configurable.
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
