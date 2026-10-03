import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
  type Mock,
} from "vitest";
import type { AuthResponse } from "@/types/api";
import {
  ADMIN,
  authResponse,
  callerOf,
  deferred,
  flush,
  installFakeServer,
  json,
  VIEWER,
  type FakeServer,
  type Route,
} from "@/test/fake-server";
import {
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import { useAuthStore } from "@/stores/auth-store";
import {
  ApiClientError,
  apiClient,
  apiFetch,
  clearTokens,
  currentSessionEpoch,
  getStoredUser,
  setAuthFailureCallback,
  setAuthRefreshCallback,
  StaleSessionError,
  storeTokens,
} from "./api-client";
import { describeError } from "./api-error";
import { apiPath } from "./api-path";
import { retryUnlessClientError } from "./query-client";

/**
 * What ends a session, and what does not (lib/api-client.ts, refreshTokens).
 *
 * A token refresh that the server answers 401 or 403 ends the session: the
 * refresh token is dead. Every other way a refresh can fail — rate limited, the
 * server or a proxy failing, no network, an answer that is no session — says
 * nothing about the session, and leaves it alone: the request that needed the
 * refresh fails with the refresh's own failure (a RefreshFailedError, which is
 * not an ApiClientError, or the network's own error), and the next refresh
 * waits a few seconds (the back-off) instead of being sent again at once.
 *
 * Real api-client, only fetch replaced (test/fake-server.ts). The back-off is
 * measured with performance.now(), which these tests move by hand, and its
 * jitter comes from Math.random, which they pin (0, unless a test says
 * otherwise): nothing here waits for real. The refresh lock and the retry of a
 * superseded refresh are in api-client.lock.test.ts and
 * api-client.superseded.test.ts.
 */

const REFRESH = "POST /api/v1/auth/refresh";
const X = "GET /api/v1/x";

let server: FakeServer;
let onFailure: Mock<() => void>;
let onRefresh: Mock<(res: AuthResponse) => void>;

// The clock the back-off reads, in milliseconds.
let clock = 0;
function elapse(ms: number) {
  clock += ms;
}

// What Math.random answers, which is what the back-off's jitter is made of: 0,
// so that the floor is the 5 s it is written as, until a test says otherwise.
let random = 0;

/** A token about to expire, so the next request refreshes it first. */
const nearExpiry = { expiresIn: 30 };

beforeEach(() => {
  localStorage.clear();
  clearTokens();
  server = installFakeServer();
  onFailure = vi.fn<() => void>();
  onRefresh = vi.fn<(res: AuthResponse) => void>();
  setAuthFailureCallback(onFailure);
  setAuthRefreshCallback(onRefresh);
  clock = 0;
  random = 0;
  vi.spyOn(performance, "now").mockImplementation(() => clock);
  vi.spyOn(Math, "random").mockImplementation(() => random);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  clearTokens();
  localStorage.clear();
});

/** What a request settles as: the error it failed with, or "sent". */
function settle(request: Promise<unknown>) {
  return request.then(
    () => "sent",
    (err: unknown) => err,
  );
}

/**
 * The api-client as a page load finds it: nobody signed in, no token held, and
 * no session ended in it yet (so nothing stops it trying the refresh cookie).
 * clearTokens() is not that — it records that a session ended — so a test of
 * what a fresh page does gets a copy of the module of its own. Its classes are
 * its own too: errors from it are recognised by name, not by instanceof.
 */
async function pageLoad() {
  vi.resetModules();
  const client = await import("./api-client");
  client.setAuthFailureCallback(onFailure);
  client.setAuthRefreshCallback(onRefresh);
  return client;
}

const expired = () => json({ error: "unauthorized", message: "expired" }, 401);

/** X refuses its first `refusals` requests as an expired token, then answers as the caller. */
function expiresAfter(refusals: number): Route {
  let reads = 0;
  return (init) =>
    ++reads <= refusals ? expired() : json({ owner: callerOf(init) });
}

// The one error object the network failure throws, so a test can tell that it
// reached the request as it was and not as something made of it.
const NETWORK_ERROR = new TypeError("Failed to fetch");

// What a refresh answered 200 with something that is no session fails with.
const NOT_A_SESSION = "the server's answer was not a session";

/**
 * A RefreshFailedError for `reason`: not an ApiClientError (see api-client.ts),
 * its status the refresh's, and a message that says the session could not be
 * renewed and what the server answered.
 */
function renewalFailed(status: number, reason: string) {
  return (err: unknown) => {
    expect(err).toMatchObject({
      name: "RefreshFailedError",
      status,
      message: `The session could not be renewed (${reason})`,
    });
  };
}

/**
 * Every way a refresh can fail without the server refusing the session: what
 * the refresh is answered with, and what a request that waited on it fails
 * with. Errors are checked by name and fields (see pageLoad).
 */
const FAILURES: Record<
  string,
  { answer: Route; check: (err: unknown) => void }
> = {
  "a 429": {
    answer: () =>
      json({ error: "too_many_requests", message: "Too Many Requests" }, 429),
    check: renewalFailed(429, "HTTP 429: Too Many Requests"),
  },
  "a 503": {
    answer: () =>
      json(
        { error: "service_unavailable", message: "Service Unavailable" },
        503,
      ),
    check: renewalFailed(503, "HTTP 503: Service Unavailable"),
  },
  "a 500 from the handler itself": {
    answer: () =>
      json(
        {
          error: "internal_server_error",
          message: "Failed to rotate refresh token",
        },
        500,
      ),
    check: renewalFailed(500, "HTTP 500: Failed to rotate refresh token"),
  },
  "a proxy's 502 with no JSON body": {
    answer: () =>
      new Response("<html>Bad Gateway</html>", {
        status: 502,
        statusText: "Bad Gateway",
      }),
    check: renewalFailed(502, "HTTP 502: Bad Gateway"),
  },
  // The statuses callers act on for their own requests: a 404 is "already
  // gone" and a 409 "someone else changed it". The refresh's must not be taken
  // for theirs.
  "any other status (a 404)": {
    answer: () => json({ error: "not_found", message: "no such route" }, 404),
    check: renewalFailed(404, "HTTP 404: no such route"),
  },
  "a 409 that is not a superseded refresh": {
    answer: () =>
      json({ error: "conflict", message: "state has changed" }, 409),
    check: renewalFailed(409, "HTTP 409: state has changed"),
  },
  "the network failing": {
    answer: () => Promise.reject(NETWORK_ERROR),
    check: (err) => {
      expect(err).toBe(NETWORK_ERROR);
    },
  },
  "a 200 that is no session": {
    answer: () => json({ version: "dev" }),
    check: renewalFailed(200, NOT_A_SESSION),
  },
  "a 200 that is not even JSON": {
    answer: () => new Response("<html>sign in</html>", { status: 200 }),
    check: renewalFailed(200, NOT_A_SESSION),
  },
};

describe("a refresh that fails without the server refusing the session", () => {
  describe.each(Object.entries(FAILURES))("%s", (_name, failure) => {
    it("fails the request it was started for with that failure, and the session stands", async () => {
      storeTokens(authResponse(ADMIN));
      const epoch = currentSessionEpoch();
      server.routes[X] = expiresAfter(1);
      server.routes[REFRESH] = failure.answer;

      const err = await settle(apiClient.get(apiPath`/api/v1/x`));

      // Not "Session expired": nobody knows that it is. And not an
      // ApiClientError either, whatever its status: callers act on the status
      // of their own request's ApiClientError (a 404 is "already gone"), and
      // a refresh they never made must not trigger that.
      failure.check(err);
      expect(err).not.toBeInstanceOf(ApiClientError);
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
      expect(currentSessionEpoch()).toBe(epoch);
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(1); // refused once, never replayed
    });

    it("is not asked again inside the back-off: the next request fails at once, with the same failure", async () => {
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(Infinity);
      server.routes[REFRESH] = failure.answer;
      const first = await settle(apiClient.get(apiPath`/api/v1/x`));

      elapse(4_999);
      const second = await settle(apiClient.get(apiPath`/api/v1/x`));

      expect(second).toBe(first);
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(2);
      expect(onFailure).not.toHaveBeenCalled();
    });

    it("is asked again once the back-off is over, and the session carries on", async () => {
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(2); // the failed request, and the next one's first try
      server.routes[REFRESH] = failure.answer;
      await settle(apiClient.get(apiPath`/api/v1/x`));

      elapse(5_000);
      server.routes[REFRESH] = () => json(authResponse(ADMIN));

      expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
        owner: ADMIN.id,
      });
      expect(server.times(REFRESH)).toBe(2);
      expect(onRefresh).toHaveBeenCalledTimes(1);
      expect(onFailure).not.toHaveBeenCalled();
    });

    it("fails a request with no token held with that failure, instead of sending it as nobody", async () => {
      const client = await pageLoad();
      server.routes[REFRESH] = failure.answer;
      server.routes[X] = (init) => json({ owner: callerOf(init) });

      const err = await settle(client.apiClient.get(apiPath`/api/v1/x`));

      failure.check(err);
      // One refresh for the one request: sent as nobody it would have met a
      // 401 and started a second.
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(0);
      expect(onFailure).not.toHaveBeenCalled();

      // Inside the back-off the next one fails at once, the same way.
      expect(await settle(client.apiClient.get(apiPath`/api/v1/x`))).toBe(err);
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(0);

      // Nothing ended: the page is not signed out, and once the back-off is over
      // the cookie is tried again, and resumes the session.
      elapse(5_000);
      server.routes[REFRESH] = () => json(authResponse(ADMIN));
      expect(await client.apiClient.get(apiPath`/api/v1/x`)).toEqual({
        owner: ADMIN.id,
      });
      expect(server.times(REFRESH)).toBe(2);
    });

    it("does not fail a request whose token is only about to expire: it is sent with the token held", async () => {
      storeTokens(authResponse(ADMIN, nearExpiry));
      const epoch = currentSessionEpoch();
      server.routes[REFRESH] = failure.answer;
      server.routes[X] = (init) => json({ owner: callerOf(init) });

      expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
        owner: ADMIN.id,
      });
      expect(server.times(REFRESH)).toBe(1);
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
      expect(currentSessionEpoch()).toBe(epoch);

      // Inside the back-off the next one asks no more than the first did, and
      // goes out the same way.
      expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
        owner: ADMIN.id,
      });
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(2);

      // Once it is over the refresh is tried again, and rotates the token.
      elapse(5_000);
      server.routes[REFRESH] = () => json(authResponse(ADMIN));
      await apiClient.get(apiPath`/api/v1/x`);
      expect(server.times(REFRESH)).toBe(2);
      expect(onRefresh).toHaveBeenCalledTimes(1);
    });

    it("fails a request whose token had really expired with that failure, after one refresh and not two", async () => {
      storeTokens(authResponse(ADMIN, nearExpiry));
      server.routes[REFRESH] = failure.answer;
      server.routes[X] = expired;

      const err = await settle(apiClient.get(apiPath`/api/v1/x`));

      // Sent with the token held, refused, and its own refresh — held off by
      // the failure of the one before it — is not sent.
      failure.check(err);
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(1);
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
    });
  });
});

describe("a refresh that fails with a body that is no error envelope", () => {
  // JSON that parses but is not {error, message}: what a proxy or a WAF in
  // front of the server may send. The failure still has to be a
  // RefreshFailedError of the refresh's status, whose text is the status text
  // (there is no server text to give), and still has to begin the back-off —
  // `null` used to throw a TypeError out of building the error, which left
  // refreshTokens before it recorded anything, so the next request refreshed
  // again at once.
  it.each([
    ["JSON null", "null"],
    ["a JSON string", '"oops"'],
    ["a JSON number", "42"],
    ["a JSON array", "[]"],
    ["an object with no message", '{"error":"service_unavailable"}'],
    ["an object whose message is not a string", '{"error":"x","message":7}'],
  ])(
    "is a RefreshFailedError of its status, and the next refresh waits, for %s as the body",
    async (_name, body) => {
      storeTokens(authResponse(ADMIN));
      const epoch = currentSessionEpoch();
      server.routes[X] = expiresAfter(Infinity);
      server.routes[REFRESH] = () =>
        new Response(body, {
          status: 503,
          statusText: "Service Unavailable",
        });

      const first = await settle(apiClient.get(apiPath`/api/v1/x`));

      renewalFailed(503, "HTTP 503: Service Unavailable")(first);
      expect(onFailure).not.toHaveBeenCalled();
      expect(currentSessionEpoch()).toBe(epoch);

      // The back-off was recorded from it: the next request is turned away at
      // once, with the same error, and nothing is sent.
      expect(await settle(apiClient.get(apiPath`/api/v1/x`))).toBe(first);
      expect(server.times(REFRESH)).toBe(1);

      // And it is a back-off, not a stop: the refresh is tried again when it is
      // over.
      elapse(5_000);
      await settle(apiClient.get(apiPath`/api/v1/x`));
      expect(server.times(REFRESH)).toBe(2);
    },
  );
});

describe.each([401, 403])("a refresh the server answers %i", (status) => {
  const refused = () =>
    json(
      { error: "unauthorized", message: "Invalid or expired refresh token" },
      status,
    );

  it("ends the session once, however many requests were waiting on the refresh", async () => {
    storeTokens(authResponse(ADMIN));
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;
    server.routes[X] = expired;
    const requests = [1, 2, 3].map(() =>
      settle(apiClient.get(apiPath`/api/v1/x`)),
    );
    await vi.waitFor(() => {
      expect(server.times(X)).toBe(3);
      expect(server.times(REFRESH)).toBe(1);
    });
    const epoch = currentSessionEpoch();

    held.resolve(refused());

    for (const err of await Promise.all(requests)) {
      expect(err).toMatchObject({
        name: "ApiClientError",
        status: 401,
        message: "Session expired",
      });
    }
    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(getStoredUser()).toBeNull();
    expect(currentSessionEpoch()).toBe(epoch + 1);
    expect(server.times(REFRESH)).toBe(1);
    // And nobody is signed in now: the next request does not try the cookie.
    await settle(apiClient.get(apiPath`/api/v1/x`));
    expect(server.times(REFRESH)).toBe(1);
  });

  it("ends the session when it is the proactive refresh that was refused: the request goes out as nobody, and meets its 401", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    server.routes[REFRESH] = refused;
    server.routes[X] = (init) =>
      callerOf(init) === "" ? expired() : json({ owner: callerOf(init) });

    const err = await settle(apiClient.get(apiPath`/api/v1/x`));

    expect(err).toMatchObject({
      name: "ApiClientError",
      status: 401,
      message: "Session expired",
    });
    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(X)).toBe(1);
    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(getStoredUser()).toBeNull();
  });

  it("ends the session when it is the cookie of a fresh page that was refused", async () => {
    const client = await pageLoad();
    server.routes[REFRESH] = refused;
    server.routes[X] = () =>
      json({ error: "unauthorized", message: "no session" }, 401);

    const err = await settle(client.apiClient.get(apiPath`/api/v1/x`));

    expect(err).toMatchObject({ name: "ApiClientError", status: 401 });
    expect(server.times(REFRESH)).toBe(1);
    expect(onFailure).toHaveBeenCalledTimes(1);
  });
});

describe("requests that need a refresh together", () => {
  it("share one refresh, and all fail with its failure", async () => {
    const client = await pageLoad();
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    const requests = [1, 2, 3].map(() =>
      settle(client.apiClient.get(apiPath`/api/v1/x`)),
    );
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    await flush();

    held.resolve(
      json({ error: "service_unavailable", message: "Unavailable" }, 503),
    );
    const [first, second, third] = await Promise.all(requests);

    renewalFailed(503, "HTTP 503: Unavailable")(first);
    expect(second).toBe(first);
    expect(third).toBe(first);
    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(X)).toBe(0);

    // The same three, after the back-off, share one refresh again.
    elapse(5_000);
    const again = deferred<Response>();
    server.routes[REFRESH] = () => again.promise;
    const later = [1, 2, 3].map(() =>
      settle(client.apiClient.get(apiPath`/api/v1/x`)),
    );
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(2);
    });
    await flush();
    again.resolve(json(authResponse(ADMIN)));

    expect(await Promise.all(later)).toEqual(["sent", "sent", "sent"]);
    expect(server.times(REFRESH)).toBe(2);
    expect(server.times(X)).toBe(3);
  });
});

describe("how long a failed refresh holds the next one off", () => {
  /** X refuses every token, and a refresh is answered `answer` unless the test says otherwise. */
  function backingOffFrom(answer: Route) {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(Infinity);
    server.routes[REFRESH] = answer;
  }

  /** Whether a request that needs a refresh sends one now. */
  async function refreshIsSent(): Promise<boolean> {
    const before = server.times(REFRESH);
    await settle(apiClient.get(apiPath`/api/v1/x`));
    return server.times(REFRESH) > before;
  }

  const tooMany = (retryAfter: string | null): Route => {
    return () =>
      new Response(
        JSON.stringify({
          error: "too_many_requests",
          message: "Too Many Requests",
        }),
        {
          status: 429,
          headers: {
            "Content-Type": "application/json",
            ...(retryAfter === null ? {} : { "Retry-After": retryAfter }),
          },
        },
      );
  };

  it("is 5 s after a failure with nothing to say about it, not a moment less", async () => {
    backingOffFrom(() => json({ error: "x", message: "down" }, 503));
    await settle(apiClient.get(apiPath`/api/v1/x`)); // fails at t = 0

    elapse(4_999);
    expect(await refreshIsSent()).toBe(false);
    elapse(1);
    expect(await refreshIsSent()).toBe(true);
  });

  it("is not stretched by the requests it turns away", async () => {
    backingOffFrom(() => json({ error: "x", message: "down" }, 503));
    await settle(apiClient.get(apiPath`/api/v1/x`)); // fails at t = 0

    elapse(3_000);
    expect(await refreshIsSent()).toBe(false); // failed fast, at t = 3 s
    elapse(2_000);
    // t = 5 s: counted from the failure, not from the request that was turned away.
    expect(await refreshIsSent()).toBe(true);
  });

  it("follows a Retry-After given in seconds, within 5 s and 60 s", async () => {
    // [header, how long the next refresh is held off, in ms]
    const cases: [string | null, number][] = [
      [null, 5_000],
      ["0", 5_000], // a server that says "now" is not asked in a loop
      ["2", 5_000],
      ["42", 42_000],
      ["3600", 60_000], // what something in front of the server may say
      ["soon", 5_000], // unreadable: as if it were not there
    ];
    for (const [retryAfter, holdsOffFor] of cases) {
      clearTokens();
      clock = 0;
      backingOffFrom(tooMany(retryAfter));
      await settle(apiClient.get(apiPath`/api/v1/x`));

      elapse(holdsOffFor - 1);
      expect(await refreshIsSent(), `${String(retryAfter)}: just before`).toBe(
        false,
      );
      elapse(1);
      expect(await refreshIsSent(), `${String(retryAfter)}: at the end`).toBe(
        true,
      );
    }
  });

  it("follows a Retry-After given as an HTTP date, and is not made longer by one in the past", async () => {
    const inThirtySeconds = new Date(Date.now() + 30_000).toUTCString();
    backingOffFrom(tooMany(inThirtySeconds));
    await settle(apiClient.get(apiPath`/api/v1/x`));

    // Dates have whole seconds: the wait is between 29 s and 30 s.
    elapse(28_000);
    expect(await refreshIsSent()).toBe(false);
    elapse(3_000);
    expect(await refreshIsSent()).toBe(true);

    clearTokens();
    clock = 0;
    const aMinuteAgo = new Date(Date.now() - 60_000).toUTCString();
    backingOffFrom(tooMany(aMinuteAgo));
    await settle(apiClient.get(apiPath`/api/v1/x`));

    elapse(5_000);
    expect(await refreshIsSent()).toBe(true);
  });

  it("follows the Retry-After of any failed answer that carries one, a 503 for a restart included", async () => {
    backingOffFrom(
      () =>
        new Response("{}", { status: 503, headers: { "Retry-After": "30" } }),
    );
    await settle(apiClient.get(apiPath`/api/v1/x`));

    elapse(29_999);
    expect(await refreshIsSent()).toBe(false);
    elapse(1);
    expect(await refreshIsSent()).toBe(true);
  });

  // Tabs and users behind one address fail together, and would all try again
  // together; up to a second on the 5 s spreads them. Math.random is pinned, so
  // the jitter is whatever the test says it is.
  it.each([
    [0, 5_000],
    [0.5, 5_500],
    [0.999, 5_999],
  ])(
    "adds up to a second to the 5 s: Math.random() = %d holds the next refresh off for %d ms",
    async (draw, holdsOffFor) => {
      random = draw;
      backingOffFrom(() => json({ error: "x", message: "down" }, 503));
      await settle(apiClient.get(apiPath`/api/v1/x`));

      elapse(holdsOffFor - 1);
      expect(await refreshIsSent()).toBe(false);
      elapse(1);
      expect(await refreshIsSent()).toBe(true);
    },
  );

  it("leaves a Retry-After as the server gave it, with no jitter on top", async () => {
    random = 0.999;
    backingOffFrom(tooMany("42"));
    await settle(apiClient.get(apiPath`/api/v1/x`));

    elapse(41_999);
    expect(await refreshIsSent()).toBe(false);
    elapse(1);
    expect(await refreshIsSent()).toBe(true);
  });

  it("holds a Retry-After shorter than the floor to the floor, its jitter included", async () => {
    random = 0.5;
    backingOffFrom(tooMany("5"));
    await settle(apiClient.get(apiPath`/api/v1/x`));

    elapse(5_499);
    expect(await refreshIsSent()).toBe(false);
    elapse(1);
    expect(await refreshIsSent()).toBe(true);
  });
});

describe("a token that is held, and past its expiry, when its refresh cannot be made", () => {
  // The expiry is the server's and the clock that judges it this browser's, so a
  // token that merely LOOKS expired is sent — a clock that runs ahead would
  // otherwise fail every request for as long as the refresh does. Past the
  // allowance (5 minutes) it is expired for real, and the refresh is the only
  // way to a token. The margins are 10 s either side of it, for the second or
  // two a test takes.
  const REFRESH_FAILS = [
    ["a 503", () => json({ error: "x", message: "down" }, 503)],
    ["the network failing", () => Promise.reject(NETWORK_ERROR)],
  ] satisfies [string, Route][];

  it.each(REFRESH_FAILS)(
    "is still sent while it is past its expiry by less than the allowance, for %s",
    async (_name, answer) => {
      storeTokens(authResponse(ADMIN, { expiresIn: -290 }));
      server.routes[REFRESH] = answer;
      server.routes[X] = (init) => json({ owner: callerOf(init) });

      expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
        owner: ADMIN.id,
      });
      expect(server.times(REFRESH)).toBe(1);
      expect(onFailure).not.toHaveBeenCalled();
    },
  );

  it.each(REFRESH_FAILS)(
    "is not sent past the allowance: the request fails at once with the refresh's failure, for %s",
    async (_name, answer) => {
      storeTokens(authResponse(ADMIN, { expiresIn: -310 }));
      server.routes[REFRESH] = answer;
      server.routes[X] = (init) => json({ owner: callerOf(init) });

      const first = await settle(apiClient.get(apiPath`/api/v1/x`));

      // Nothing went out to be refused: that is the 401 stream this prevents.
      expect(first).not.toBe("sent");
      expect(server.times(X)).toBe(0);
      expect(server.times(REFRESH)).toBe(1);
      // Nor does the next one, inside the back-off: the same failure, at once.
      expect(await settle(apiClient.get(apiPath`/api/v1/x`))).toBe(first);
      expect(server.times(X)).toBe(0);
      expect(server.times(REFRESH)).toBe(1);
      // The session is as alive as it was.
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
    },
  );

  it("control: past the allowance, with the refresh made, the request goes out under the new token", async () => {
    storeTokens(authResponse(ADMIN, { expiresIn: -310 }));
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: ADMIN.id,
    });
    expect(server.times(REFRESH)).toBe(1);
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });
});

describe("the back-off belongs to the session whose refresh failed", () => {
  /** The admin's request fails a refresh, and so begins a back-off. */
  async function theAdminsRefreshFails() {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(2); // the admin's, then the viewer's first try
    server.routes[REFRESH] = () => json({ error: "x", message: "down" }, 503);
    await settle(apiClient.get(apiPath`/api/v1/x`));
    expect(server.times(REFRESH)).toBe(1);
    // No time has passed: the admin's session is still inside its back-off.
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
  }

  it.each([
    [
      "someone signing in over the live session",
      () => {
        storeTokens(authResponse(VIEWER));
      },
    ],
    [
      "a sign-out and the next user's sign-in",
      () => {
        clearTokens();
        storeTokens(authResponse(VIEWER));
      },
    ],
  ])(
    "is not waited out by the session that follows: %s",
    async (_name, changeHands) => {
      await theAdminsRefreshFails();

      changeHands();

      // Their token is refused too, and their refresh goes out at once.
      expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
        owner: VIEWER.id,
      });
      expect(server.times(REFRESH)).toBe(2);
    },
  );

  it("is not begun by a failure that arrives after the session ended, which is that session's and not the current one's: the network failing", async () => {
    storeTokens(authResponse(ADMIN));
    const held = deferred<Response>();
    let refreshes = 0;
    server.routes[REFRESH] = () =>
      ++refreshes === 1 ? held.promise : json(authResponse(VIEWER));
    server.routes[X] = expiresAfter(2); // the admin's request, the viewer's first try
    const admins = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });

    clearTokens();
    storeTokens(authResponse(VIEWER));
    held.reject(NETWORK_ERROR);

    expect(await admins).toBeInstanceOf(StaleSessionError);
    expect(onFailure).not.toHaveBeenCalled();
    // The viewer's refresh is not held off by it.
    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: VIEWER.id,
    });
    expect(server.times(REFRESH)).toBe(2);
  });

  it("is not begun by a failure that arrives after the session ended, either: an error body still being read", async () => {
    storeTokens(authResponse(ADMIN));
    // The headers of a 503 are in, and its body is not.
    const body = deferred<unknown>();
    let refreshes = 0;
    server.routes[REFRESH] = () =>
      ++refreshes === 1
        ? ({
            ok: false,
            status: 503,
            statusText: "Service Unavailable",
            headers: new Headers(),
            json: () => body.promise,
          } as unknown as Response)
        : json(authResponse(VIEWER));
    server.routes[X] = expiresAfter(2);
    const admins = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    await flush(); // the headers are in; the body is awaited

    clearTokens();
    storeTokens(authResponse(VIEWER));
    body.resolve({ error: "x", message: "down" });

    expect(await admins).toBeInstanceOf(StaleSessionError);
    expect(onFailure).not.toHaveBeenCalled();
    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: VIEWER.id,
    });
    expect(server.times(REFRESH)).toBe(2);
  });
});

describe("a refresh failure, as the rest of the SPA reads an error", () => {
  /** A request refused, whose refresh then fails as `answer`. */
  async function failsWith(answer: Route): Promise<unknown> {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expired;
    server.routes[REFRESH] = answer;
    return settle(apiClient.get(apiPath`/api/v1/x`));
  }

  it.each([
    [
      "a 429",
      () =>
        json({ error: "too_many_requests", message: "Too Many Requests" }, 429),
      "The session could not be renewed (HTTP 429: Too Many Requests)",
    ],
    [
      "a 503",
      () => json({ error: "service_unavailable", message: "Unavailable" }, 503),
      "The session could not be renewed (HTTP 503: Unavailable)",
    ],
    // Over HTTP/2 a proxy's 502 has neither a body nor a status text.
    [
      "a bare 502",
      () => new Response(null, { status: 502 }),
      "The session could not be renewed (HTTP 502)",
    ],
    ["the network failing", () => Promise.reject(NETWORK_ERROR), ""],
  ] satisfies [string, Route, string][])(
    "%s is retried once, as any 429, 5xx or network failure is, and described by its own reason",
    async (_name, answer, described) => {
      const err = await failsWith(answer);

      expect(retryUnlessClientError(0, err as Error)).toBe(true);
      expect(retryUnlessClientError(1, err as Error)).toBe(false);
      expect(describeError(err)).toBe(described);
    },
  );

  it("control: the server refusing the session is the one that is not retried, and reads as an expired session", async () => {
    const err = await failsWith(() => json({}, 401));

    expect(retryUnlessClientError(0, err as Error)).toBe(false);
    expect(describeError(err)).toBe("Session expired");
  });
});

describe("the other ways a request leaves the SPA", () => {
  it("apiFetch is not sent as nobody when no token is held and the refresh fails", async () => {
    const client = await pageLoad();
    server.routes[REFRESH] = () =>
      json({ error: "service_unavailable", message: "Unavailable" }, 503);
    server.routes[X] = () => json({ ok: true });

    await expect(
      client.apiFetch(apiPath`/api/v1/x`, { credentials: "same-origin" }),
    ).rejects.toMatchObject({ name: "RefreshFailedError", status: 503 });

    expect(server.times(X)).toBe(0);
    expect(onFailure).not.toHaveBeenCalled();
  });

  it("apiFetch sends a token that is about to expire when its refresh fails", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    server.routes[REFRESH] = () =>
      json({ error: "service_unavailable", message: "Unavailable" }, 503);
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    const res = await apiFetch(apiPath`/api/v1/x`, {
      credentials: "same-origin",
    });

    expect(await res.json()).toEqual({ owner: ADMIN.id });
    expect(server.times(REFRESH)).toBe(1);
    expect(onFailure).not.toHaveBeenCalled();
  });
});

describe("a refresh answered 200 with something that is not session-shaped", () => {
  // Each of these is JSON the server never sends for a refresh: a proxy's, an
  // object that has a user and nothing else. The first is the one that was
  // stored as a session — a token of `undefined`, permissions nothing could
  // call .includes on.
  const good = authResponse(ADMIN);
  const NOT_SESSIONS: [string, string][] = [
    ["a user with an id and nothing else", '{"user":{"id":"x"}}'],
    ["an empty object", "{}"],
    ["JSON null", "null"],
    ["a JSON array", "[]"],
    [
      "a user with no id",
      JSON.stringify({ ...good, user: { email: "admin@example.com" } }),
    ],
    [
      "a user id that is not a string",
      JSON.stringify({ ...good, user: { id: 7 } }),
    ],
    ["no user", JSON.stringify({ ...good, user: undefined })],
    ["an empty access token", JSON.stringify({ ...good, access_token: "" })],
    ["no access token", JSON.stringify({ ...good, access_token: undefined })],
    [
      "an access token that is not a string",
      JSON.stringify({ ...good, access_token: 5 }),
    ],
    [
      "an expiry that is a string",
      JSON.stringify({ ...good, expires_at: "soon" }),
    ],
    ["no expiry", JSON.stringify({ ...good, expires_at: undefined })],
    [
      // JSON.stringify cannot write one; the parser reads 1e999 as Infinity.
      "an expiry that is not finite",
      JSON.stringify({ ...good, expires_at: 1 }).replace(
        '"expires_at":1',
        '"expires_at":1e999',
      ),
    ],
    [
      "permissions that are an object",
      JSON.stringify({ ...good, permissions: {} }),
    ],
    [
      "permissions that are a string",
      JSON.stringify({ ...good, permissions: "view:cluster" }),
    ],
    [
      "permissions that are a number",
      JSON.stringify({ ...good, permissions: 7 }),
    ],
  ];

  it.each(NOT_SESSIONS)(
    "is a failed refresh, not a session, for %s: nothing is stored and the next refresh waits",
    async (_name, body) => {
      storeTokens(authResponse(ADMIN));
      const epoch = currentSessionEpoch();
      server.routes[X] = expiresAfter(Infinity);
      server.routes[REFRESH] = () => new Response(body, { status: 200 });

      const first = await settle(apiClient.get(apiPath`/api/v1/x`));

      // The fixed words, with none of the parser's or of the TypeError's.
      renewalFailed(200, NOT_A_SESSION)(first);
      expect(onRefresh).not.toHaveBeenCalled();
      expect(onFailure).not.toHaveBeenCalled();
      expect(currentSessionEpoch()).toBe(epoch);
      // The user the session held is still the one stored: no half of an answer
      // replaced it.
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
      // And it began the back-off like any failure of a refresh.
      expect(await settle(apiClient.get(apiPath`/api/v1/x`))).toBe(first);
      expect(server.times(REFRESH)).toBe(1);
    },
  );

  // The server sends permissions as a list and never as null (loadPerms), but
  // an answer that has none, or null, is not one that is wrong about anything
  // else: refused, it would fail every refresh for good with nobody signed out.
  // It is a session with no permissions, which is the cautious reading — the
  // pages offer nothing until an answer says otherwise.
  it.each([
    ["null", JSON.stringify({ ...good, permissions: null })],
    ["missing", JSON.stringify({ ...good, permissions: undefined })],
  ])(
    "is a session, with no permissions, when its permissions are %s",
    async (_name, body) => {
      storeTokens(authResponse(ADMIN, { permissions: ["view:cluster"] }));
      server.routes[X] = expiresAfter(1);
      server.routes[REFRESH] = () => new Response(body, { status: 200 });

      expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
        owner: ADMIN.id,
      });

      expect(onFailure).not.toHaveBeenCalled();
      expect(server.times(REFRESH)).toBe(1);
      // Told as a list, whatever it was sent as: what it is told to takes
      // .includes of it.
      expect(onRefresh).toHaveBeenCalledTimes(1);
      expect(onRefresh.mock.calls[0]?.[0].permissions).toEqual([]);
    },
  );

  it("control: permissions that are a list are passed on as they are", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(1);
    server.routes[REFRESH] = () =>
      json(
        authResponse(ADMIN, { permissions: ["view:cluster", "manage:user"] }),
      );

    await apiClient.get(apiPath`/api/v1/x`);

    expect(onRefresh.mock.calls[0]?.[0].permissions).toEqual([
      "view:cluster",
      "manage:user",
    ]);
  });

  it("control: a session-shaped answer is a session, and is stored", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(1);
    server.routes[REFRESH] = () =>
      new Response(JSON.stringify(authResponse(ADMIN)), { status: 200 });

    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: ADMIN.id,
    });
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(server.times(REFRESH)).toBe(1);
  });
});

describe("a refresh that fails after the session has changed hands", () => {
  /**
   * The session changes hands in the few microtasks between a refresh's failure
   * being judged and anyone seeing it. failed() reads the clock once,
   * synchronously, after it has judged the failure and before the rejection's
   * reactions run, so the first read queues the change — which runs before them.
   */
  function whenTheFailureIsJudged(change: () => void) {
    let fired = false;
    vi.spyOn(performance, "now").mockImplementation(() => {
      if (!fired) {
        fired = true;
        queueMicrotask(change);
      }
      return clock;
    });
  }

  const down = () => json({ error: "x", message: "down" }, 503);

  it("does not send the ended session's request under the next user's token: a token held, about to expire", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    server.routes[REFRESH] = down;
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    whenTheFailureIsJudged(() => {
      storeTokens(authResponse(VIEWER));
    });

    const outcome = await settle(apiClient.get(apiPath`/api/v1/x`));

    // Control, in the sibling test of FAILURES: with no hook the same request is
    // sent with the token held.
    expect(outcome).toBeInstanceOf(StaleSessionError);
    expect(server.times(X)).toBe(0);
  });

  it("does not fail the ended session's request with a failure that is not its own: no token held", async () => {
    const client = await pageLoad();
    server.routes[REFRESH] = down;
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    whenTheFailureIsJudged(() => {
      client.storeTokens(authResponse(VIEWER));
    });

    const outcome = await settle(client.apiClient.get(apiPath`/api/v1/x`));

    expect(outcome).toBeInstanceOf(client.StaleSessionError);
    expect(server.times(X)).toBe(0);
  });

  // The session may also END in that gap, with a sign-out, and nobody sign in:
  // nothing is held then, and a request that waited on the ended session's
  // refresh is told it is stale — not the failure of a refresh that is no
  // longer anybody's.
  it("is told it is stale, not the failure, when the session is signed out as the failure is judged: a token held, about to expire", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    server.routes[REFRESH] = down;
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    whenTheFailureIsJudged(() => {
      clearTokens();
    });

    const outcome = await settle(apiClient.get(apiPath`/api/v1/x`));

    expect(outcome).toBeInstanceOf(StaleSessionError);
    expect(server.times(X)).toBe(0);
  });

  it("is told it is stale, not the failure, when the session is signed out as the failure is judged: no token held", async () => {
    const client = await pageLoad();
    server.routes[REFRESH] = down;
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    whenTheFailureIsJudged(() => {
      client.clearTokens();
    });

    const outcome = await settle(client.apiClient.get(apiPath`/api/v1/x`));

    expect(outcome).toBeInstanceOf(client.StaleSessionError);
    expect(server.times(X)).toBe(0);
  });

  it("does not fail the ended session's request with a failure that is not its own: its 401 refreshed", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(Infinity);
    server.routes[REFRESH] = down;
    whenTheFailureIsJudged(() => {
      storeTokens(authResponse(VIEWER));
    });

    const outcome = await settle(apiClient.get(apiPath`/api/v1/x`));

    expect(outcome).toBeInstanceOf(StaleSessionError);
    expect(server.times(X)).toBe(1); // refused once, and not replayed
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
  });

  describe("when the refresh is answered for someone else and the callback that was told throws", () => {
    // Another tab signed the viewer in on the shared cookie, so the answer is
    // theirs: refreshTokens begins their session (storeTokens, a new epoch) and
    // only then tells onAuthRefresh, which here has a bug. The refresh rejects
    // with that bug, long after the epoch moved.
    beforeEach(() => {
      setAuthRefreshCallback(() => {
        throw new Error("a listener with a bug");
      });
      server.routes[REFRESH] = () => json(authResponse(VIEWER));
    });

    it("does not send the admin's request under either user's token: a token held, about to expire", async () => {
      storeTokens(authResponse(ADMIN, nearExpiry));
      server.routes[X] = (init) => json({ owner: callerOf(init) });

      const outcome = await settle(apiClient.get(apiPath`/api/v1/x`));

      expect(outcome).toBeInstanceOf(StaleSessionError);
      expect(server.times(X)).toBe(0);
      expect(getStoredUser()).toMatchObject({ id: VIEWER.id }); // theirs began
    });

    it("tells the admin's request it is stale and not what the callback threw: its 401 refreshed", async () => {
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(1);

      const outcome = await settle(apiClient.get(apiPath`/api/v1/x`));

      expect(outcome).toBeInstanceOf(StaleSessionError);
      expect(server.times(X)).toBe(1); // not replayed as anyone
    });
  });
});

describe("a refresh that rotated the token and then failed", () => {
  // The refresh succeeded — the session's token is now a newer one — and a
  // listener of onAuthRefresh then threw, so the refresh rejects. The request
  // that waited on it is the same session's, and goes out with what is held
  // NOW. The token it was held with when it began is superseded.
  const ROTATED = `${ADMIN.id}-2`; // what callerOf makes of the token below
  const rotated = (): AuthResponse => ({
    ...authResponse(ADMIN),
    access_token: `token-${ADMIN.id}-2`,
  });

  beforeEach(() => {
    setAuthRefreshCallback(() => {
      throw new Error("a listener with a bug");
    });
    server.routes[REFRESH] = () => json(rotated());
    server.routes[X] = (init) => json({ owner: callerOf(init) });
  });

  it("is sent with the newer token: a token held, about to expire", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));

    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: ROTATED,
    });
    expect(server.times(REFRESH)).toBe(1);
  });

  it("is sent with the newer token: a token held, past its expiry", async () => {
    storeTokens(authResponse(ADMIN, { expiresIn: -10 }));

    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: ROTATED,
    });
    expect(server.times(REFRESH)).toBe(1);
  });

  it("control: with no listener to fail, the same request is sent with the newer token", async () => {
    setAuthRefreshCallback(onRefresh);
    storeTokens(authResponse(ADMIN, nearExpiry));

    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: ROTATED,
    });
  });
});

describe("the back-off is counted from the failure, not from when the refresh began", () => {
  it("a refresh that takes 20 s to fail still holds the next one off for 5 s, and not a moment more", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(Infinity);
    const held = deferred<Response>();
    let refreshes = 0;
    server.routes[REFRESH] = () =>
      ++refreshes === 1
        ? held.promise
        : json({ error: "x", message: "down" }, 503);
    const first = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    elapse(20_000); // the refresh is slow...
    held.resolve(json({ error: "x", message: "down" }, 503)); // ...and then fails
    await first;

    elapse(4_999); // inside the 5 s that follow the FAILURE
    await settle(apiClient.get(apiPath`/api/v1/x`));
    expect(server.times(REFRESH)).toBe(1);

    elapse(1); // and over with them
    await settle(apiClient.get(apiPath`/api/v1/x`));
    expect(server.times(REFRESH)).toBe(2);
  });
});

describe("a request refused for a token another request's refresh has replaced", () => {
  /** What the server issues a refresh: the admin's session, a token of its own each time. */
  const rotatedTo = (n: number): AuthResponse => ({
    ...authResponse(ADMIN),
    access_token: `token-${ADMIN.id}-${String(n)}`,
  });
  const ROTATED = `${ADMIN.id}-2`; // what callerOf makes of the second token

  /** X holds the answer to every request sent with the first token until the test releases it. */
  function holdsTheFirstTokensAnswers(isRefused: () => boolean = () => false) {
    const gates: ReturnType<typeof deferred<Response>>[] = [];
    server.routes[X] = (init) => {
      if (callerOf(init) === ROTATED) {
        return isRefused() ? expired() : json({ owner: callerOf(init) });
      }
      const gate = deferred<Response>();
      gates.push(gate);
      return gate.promise;
    };
    return gates;
  }

  it("is replayed with the token held, and no second refresh is made for it", async () => {
    storeTokens(authResponse(ADMIN));
    const epoch = currentSessionEpoch();
    const gates = holdsTheFirstTokensAnswers();
    server.routes[REFRESH] = () => json(rotatedTo(2));
    const first = settle(apiClient.get(apiPath`/api/v1/x`));
    const second = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(gates).toHaveLength(2);
    });

    // The first is refused, refreshes, and is replayed with the new token ...
    gates[0]?.resolve(expired());
    expect(await first).toBe("sent");
    expect(server.times(REFRESH)).toBe(1);
    // ... and the second is refused AFTER that, for the token that has just
    // been replaced: a refresh of its own would rotate it again for nothing.
    gates[1]?.resolve(expired());
    expect(await second).toBe("sent");

    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(X)).toBe(4); // each sent twice
    expect(currentSessionEpoch()).toBe(epoch);
    expect(onFailure).not.toHaveBeenCalled();
  });

  it("is replayed once: if the token held is refused as well, that is the answer, and nothing refreshes again", async () => {
    storeTokens(authResponse(ADMIN));
    let refuseTheNewTokenToo = false;
    const gates = holdsTheFirstTokensAnswers(() => refuseTheNewTokenToo);
    server.routes[REFRESH] = () => json(rotatedTo(2));
    const first = settle(apiClient.get(apiPath`/api/v1/x`));
    const second = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(gates).toHaveLength(2);
    });
    gates[0]?.resolve(expired());
    expect(await first).toBe("sent");

    refuseTheNewTokenToo = true;
    gates[1]?.resolve(expired());

    expect(await second).toMatchObject({
      name: "ApiClientError",
      status: 401,
    });
    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(X)).toBe(4);
    // A request refused is not a session refused: it stands.
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
  });

  it("control: refused for the token that is still the one held, it refreshes, as ever", async () => {
    storeTokens(authResponse(ADMIN));
    const gates = holdsTheFirstTokensAnswers();
    server.routes[REFRESH] = () => json(rotatedTo(2));
    const only = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(gates).toHaveLength(1);
    });

    gates[0]?.resolve(expired());

    expect(await only).toBe("sent");
    expect(server.times(REFRESH)).toBe(1);
  });
});

describe("through the auth store: what the user is left with", () => {
  const LOGIN = "POST /api/v1/auth/login";

  function signedOutState() {
    useAuthStore.setState({
      user: null,
      permissions: [],
      isAuthenticated: false,
      isLoading: false,
      isInitialized: false,
      totpPending: false,
      totpPendingToken: null,
      isLoggingOut: false,
    });
  }

  beforeEach(async () => {
    emptyPerSessionStores();
    signedOutState();
    // No session cookie yet. Registers the forced-logout and refresh callbacks,
    // as main.tsx does at boot, in place of this file's mocks.
    server.routes[REFRESH] = () => json({}, 401);
    await useAuthStore.getState().initialize();
    server.routes[LOGIN] = () => json(authResponse(ADMIN));
    await useAuthStore
      .getState()
      .login({ email: ADMIN.email, password: "example-password" });
    // What a user has open: console tabs, a dismissed health issue, ...
    for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
    server.routes[X] = expired;
  });

  afterEach(() => {
    emptyPerSessionStores();
    signedOutState();
  });

  it.each([
    ["a 503", () => json({ error: "x", message: "down" }, 503)],
    ["a 429", () => json({ error: "x", message: "slow down" }, 429)],
    ["the network failing", () => Promise.reject(NETWORK_ERROR)],
  ] satisfies [string, Route][])(
    "a refresh that fails as %s leaves them signed in, with their console tabs and dismissed issues",
    async (_name, answer) => {
      // Before this, a server restart or a proxy's hiccup was a sign-out, and
      // the end of a session wipes what the user had open (stores/session-reset.ts).
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
        expect(probe.holdsData(), file).toBe(true);
      }
      server.routes[REFRESH] = answer;

      await settle(apiClient.get(apiPath`/api/v1/x`));

      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(useAuthStore.getState().user).toMatchObject({ id: ADMIN.id });
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
      for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
        expect(probe.holdsData(), file).toBe(true);
      }
    },
  );

  it("control: a refresh the server refuses signs them out, and what they had open goes", async () => {
    server.routes[REFRESH] = () => json({}, 401);

    await settle(apiClient.get(apiPath`/api/v1/x`));

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(getStoredUser()).toBeNull();
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(false);
    }
  });
});
