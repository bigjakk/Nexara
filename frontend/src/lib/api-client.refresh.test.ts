import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthResponse } from "@/types/api";
import {
  ADMIN,
  VIEWER,
  authResponse,
  callerOf,
  deferred,
  flush,
  json,
  type Route,
} from "@/test/fake-server";
import {
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import {
  LOGIN,
  REFRESH,
  SIGNED_OUT,
  X,
  answers,
  clockNow,
  down,
  elapse,
  expired,
  expiresAfter,
  getX,
  nearExpiry,
  onFailure,
  onRefresh,
  pageLoad,
  pinRandom,
  readX,
  server,
  tooMany,
  untilSent,
  installApiClientHarness,
  type Client,
} from "@/test/api-client-harness";
import { useAuthStore } from "@/stores/auth-store";
import * as api from "./api-client";
import {
  ApiClientError,
  apiFetch,
  clearTokens,
  currentSessionEpoch,
  getStoredUser,
  setAuthRefreshCallback,
  StaleSessionError,
  storeTokens,
} from "./api-client";
import { describeError } from "./api-error";
import { apiPath } from "./api-path";
import { retryUnlessClientError } from "./query-client";

/**
 * What ends a session, and what does not (lib/api-client.ts, refreshTokens). A
 * refresh the server answers 401 or 403 ends it. Any other failure (429, 5xx, no
 * network, an answer that is no session) says nothing about the session: the
 * request fails with the refresh's own failure (a RefreshFailedError, not an
 * ApiClientError, or the network's error) and the next refresh waits out a
 * back-off, run on a hand-moved performance.now and a pinned Math.random jitter.
 */

installApiClientHarness({ clock: true, random: true });

// One instance, so a test can tell it reached the request as it was.
const NETWORK_ERROR = new TypeError("Failed to fetch");

const NOT_A_SESSION = "the server's answer was not a session";

/** What the server issues a refresh: the admin's session, a token of its own each time. */
const rotatedTo = (n: number): AuthResponse => ({
  ...authResponse(ADMIN),
  access_token: `token-${ADMIN.id}-${String(n)}`,
});
const ROTATED = `${ADMIN.id}-2`; // what callerOf makes of the second token

/** A RefreshFailedError for `reason`: its status the refresh's, and not an ApiClientError. */
function renewalFailed(status: number, reason: string) {
  return (err: unknown) => {
    expect(err).toMatchObject({
      name: "RefreshFailedError",
      status,
      message: `The session could not be renewed (${reason})`,
    });
  };
}

/** A refresh answered `status` with the server's own {error, message}. */
const answered = (status: number, error: string, message: string) => ({
  answer: (): Response => json({ error, message }, status),
  check: renewalFailed(status, `HTTP ${String(status)}: ${message}`),
});

/** Every way a refresh can fail without the server refusing the session. */
const FAILURES: Record<
  string,
  { answer: Route; check: (err: unknown) => void }
> = {
  "a 429": answered(429, "too_many_requests", "Too Many Requests"),
  "a 503": answered(503, "service_unavailable", "Service Unavailable"),
  "a 500 from the handler itself": answered(
    500,
    "internal_server_error",
    "Failed to rotate refresh token",
  ),
  "a proxy's 502 with no JSON body": {
    answer: () =>
      new Response("<html>Bad Gateway</html>", {
        status: 502,
        statusText: "Bad Gateway",
      }),
    check: renewalFailed(502, "HTTP 502: Bad Gateway"),
  },
  // The statuses callers act on for their own requests (404 "already gone", 409
  // "someone else changed it"): the refresh's must not be taken for theirs.
  "any other status (a 404)": answered(404, "not_found", "no such route"),
  "a 409 that is not a superseded refresh": answered(
    409,
    "conflict",
    "state has changed",
  ),
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

      const err = await readX();

      // Not "Session expired", and not an ApiClientError whatever its status:
      // callers act on the status of their own request's error (a 404 is
      // "already gone"), and a refresh they never made must not trigger that.
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
      const first = await readX();

      elapse(4_999);
      const second = await readX();

      expect(second).toBe(first);
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(2);
      expect(onFailure).not.toHaveBeenCalled();
    });

    it("is asked again once the back-off is over, and the session carries on", async () => {
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(2); // the failed request, and the next one's first try
      server.routes[REFRESH] = failure.answer;
      await readX();

      elapse(5_000);
      server.routes[REFRESH] = () => json(authResponse(ADMIN));

      expect(await getX()).toEqual({ owner: ADMIN.id });
      expect(server.times(REFRESH)).toBe(2);
      expect(onRefresh).toHaveBeenCalledTimes(1);
      expect(onFailure).not.toHaveBeenCalled();
    });

    it("fails a request with no token held with that failure, instead of sending it as nobody", async () => {
      const client = await pageLoad();
      server.routes[REFRESH] = failure.answer;
      server.routes[X] = (init) => json({ owner: callerOf(init) });

      const err = await readX(client);

      failure.check(err);
      // Sent as nobody it would have met a 401 and started a second refresh.
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(0);
      expect(onFailure).not.toHaveBeenCalled();

      expect(await readX(client)).toBe(err);
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(0);

      elapse(5_000);
      server.routes[REFRESH] = () => json(authResponse(ADMIN));
      expect(await getX(client)).toEqual({ owner: ADMIN.id });
      expect(server.times(REFRESH)).toBe(2);
    });

    it("does not fail a request whose token is only about to expire: it is sent with the token held", async () => {
      storeTokens(authResponse(ADMIN, nearExpiry));
      const epoch = currentSessionEpoch();
      server.routes[REFRESH] = failure.answer;
      server.routes[X] = (init) => json({ owner: callerOf(init) });

      expect(await getX()).toEqual({ owner: ADMIN.id });
      expect(server.times(REFRESH)).toBe(1);
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
      expect(currentSessionEpoch()).toBe(epoch);

      expect(await getX()).toEqual({ owner: ADMIN.id });
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(2);

      elapse(5_000);
      server.routes[REFRESH] = () => json(authResponse(ADMIN));
      await getX();
      expect(server.times(REFRESH)).toBe(2);
      expect(onRefresh).toHaveBeenCalledTimes(1);
    });

    it("fails a request whose token had really expired with that failure, after one refresh and not two", async () => {
      storeTokens(authResponse(ADMIN, nearExpiry));
      server.routes[REFRESH] = failure.answer;
      server.routes[X] = expired;

      const err = await readX();

      // Its own refresh is held off by the failure of the first.
      failure.check(err);
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(X)).toBe(1);
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
    });
  });
});

describe("a refresh that fails with a body that is no error envelope", () => {
  // What a proxy or a WAF may send. It must still be a RefreshFailedError of the
  // refresh's status, with the status text, that begins the back-off: `null`
  // used to throw a TypeError out of building the error, which left
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
        new Response(body, { status: 503, statusText: "Service Unavailable" });

      const first = await readX();

      renewalFailed(503, "HTTP 503: Service Unavailable")(first);
      expect(onFailure).not.toHaveBeenCalled();
      expect(currentSessionEpoch()).toBe(epoch);

      expect(await readX()).toBe(first);
      expect(server.times(REFRESH)).toBe(1);

      elapse(5_000);
      await readX();
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
  const sessionExpired = {
    name: "ApiClientError",
    status: 401,
    message: "Session expired",
  };

  it("ends the session once, however many requests were waiting on the refresh", async () => {
    storeTokens(authResponse(ADMIN));
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;
    server.routes[X] = expired;
    const requests = [1, 2, 3].map(() => readX());
    await vi.waitFor(() => {
      expect(server.times(X)).toBe(3);
      expect(server.times(REFRESH)).toBe(1);
    });
    const epoch = currentSessionEpoch();

    held.resolve(refused());

    for (const err of await Promise.all(requests)) {
      expect(err).toMatchObject(sessionExpired);
    }
    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(getStoredUser()).toBeNull();
    expect(currentSessionEpoch()).toBe(epoch + 1);
    expect(server.times(REFRESH)).toBe(1);
    await readX();
    expect(server.times(REFRESH)).toBe(1);
  });

  it("ends the session when it is the proactive refresh that was refused: the request goes out as nobody, and meets its 401", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    server.routes[REFRESH] = refused;
    server.routes[X] = (init) =>
      callerOf(init) === "" ? expired() : json({ owner: callerOf(init) });

    expect(await readX()).toMatchObject(sessionExpired);
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

    expect(await readX(client)).toMatchObject({
      name: "ApiClientError",
      status: 401,
    });
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
    const requests = [1, 2, 3].map(() => readX(client));
    await untilSent(REFRESH);
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

    elapse(5_000);
    const again = deferred<Response>();
    server.routes[REFRESH] = () => again.promise;
    const later = [1, 2, 3].map(() => readX(client));
    await untilSent(REFRESH, 2);
    await flush();
    again.resolve(json(authResponse(ADMIN)));

    expect(await Promise.all(later)).toEqual(["sent", "sent", "sent"]);
    expect(server.times(REFRESH)).toBe(2);
    expect(server.times(X)).toBe(3);
  });
});

describe("how long a failed refresh holds the next one off", () => {
  /** X refuses every token, and the refresh is answered `answer`. */
  function backingOffFrom(answer: Route) {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(Infinity);
    server.routes[REFRESH] = answer;
  }

  /** Whether a request that needs a refresh sends one now. */
  async function refreshIsSent(): Promise<boolean> {
    const before = server.times(REFRESH);
    await readX();
    return server.times(REFRESH) > before;
  }

  /** Fails at t = 0, then: not sent a moment before `holdsOffFor`, sent at it. */
  async function holdsOffFor(answer: Route, ms: number) {
    backingOffFrom(answer);
    await readX();

    elapse(ms - 1);
    expect(await refreshIsSent()).toBe(false);
    elapse(1);
    expect(await refreshIsSent()).toBe(true);
  }

  // The floor is 5 s plus up to a second of jitter (tabs and users behind one
  // address fail together and would all try again together); a Retry-After
  // replaces it when longer, within 60 s, with no jitter on top.
  const aMinuteAgo = new Date(Date.now() - 60_000).toUTCString();
  it.each<[string, number, number, Route]>([
    ["nothing to say about it", 5_000, 0, down],
    ["Math.random() = 0.5, half a second of jitter", 5_500, 0.5, down],
    ["Math.random() = 0.999, the most jitter", 5_999, 0.999, down],
    ["a 429 with no Retry-After", 5_000, 0, tooMany()],
    [
      "Retry-After: 0, a server that says now is not asked in a loop",
      5_000,
      0,
      tooMany("0"),
    ],
    ["Retry-After: 2, under the floor", 5_000, 0, tooMany("2")],
    ["Retry-After: 42", 42_000, 0, tooMany("42")],
    [
      "Retry-After: 3600, what something in front may say",
      60_000,
      0,
      tooMany("3600"),
    ],
    [
      "Retry-After: soon, unreadable, as if it were not there",
      5_000,
      0,
      tooMany("soon"),
    ],
    ["a Retry-After date in the past", 5_000, 0, tooMany(aMinuteAgo)],
    [
      "a Retry-After on a 503 for a restart",
      30_000,
      0,
      () =>
        new Response("{}", { status: 503, headers: { "Retry-After": "30" } }),
    ],
    [
      "Retry-After: 42 with Math.random() = 0.999, no jitter on top",
      42_000,
      0.999,
      tooMany("42"),
    ],
    [
      "Retry-After: 5 with Math.random() = 0.5, under the floor with its jitter",
      5_500,
      0.5,
      tooMany("5"),
    ],
  ])("is held off by %s, for %i ms", async (_name, ms, draw, answer) => {
    pinRandom(draw);
    await holdsOffFor(answer, ms);
  });

  it("follows a Retry-After given as an HTTP date, to the second", async () => {
    backingOffFrom(tooMany(new Date(Date.now() + 30_000).toUTCString()));
    await readX();

    // Dates have whole seconds: the wait is between 29 s and 30 s.
    elapse(28_000);
    expect(await refreshIsSent()).toBe(false);
    elapse(3_000);
    expect(await refreshIsSent()).toBe(true);
  });

  it("is not stretched by the requests it turns away", async () => {
    backingOffFrom(down);
    await readX(); // fails at t = 0

    elapse(3_000);
    expect(await refreshIsSent()).toBe(false); // turned away fast, at t = 3 s
    elapse(2_000);
    // Counted from the failure, not from the request that was turned away.
    expect(await refreshIsSent()).toBe(true);
  });
});

describe("a token that is held, and past its expiry, when its refresh cannot be made", () => {
  // The expiry is the server's and the clock that judges it this browser's, so a
  // token that merely LOOKS expired is sent: a clock running ahead would otherwise
  // fail every request for as long as the refresh does. Past the allowance (5
  // minutes) it has expired for real, and the refresh is the only way to a token.
  const REFRESH_FAILS: [string, Route][] = [
    ["a 503", down],
    ["the network failing", () => Promise.reject(NETWORK_ERROR)],
  ];

  it.each(REFRESH_FAILS)(
    "is still sent while it is past its expiry by less than the allowance, for %s",
    async (_name, answer) => {
      storeTokens(authResponse(ADMIN, { expiresIn: -290 }));
      server.routes[REFRESH] = answer;
      server.routes[X] = (init) => json({ owner: callerOf(init) });

      expect(await getX()).toEqual({ owner: ADMIN.id });
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

      const first = await readX();

      // Nothing went out to be refused: that is the 401 stream this prevents.
      expect(first).not.toBe("sent");
      expect(server.times(X)).toBe(0);
      expect(server.times(REFRESH)).toBe(1);
      expect(await readX()).toBe(first);
      expect(server.times(X)).toBe(0);
      expect(server.times(REFRESH)).toBe(1);
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
    },
  );

  it("control: past the allowance, with the refresh made, the request goes out under the new token", async () => {
    storeTokens(authResponse(ADMIN, { expiresIn: -310 }));
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    expect(await getX()).toEqual({ owner: ADMIN.id });
    expect(server.times(REFRESH)).toBe(1);
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });
});

describe("the back-off belongs to the session whose refresh failed", () => {
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
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(2); // the admin's, then the viewer's first try
      server.routes[REFRESH] = down;
      await readX();
      expect(server.times(REFRESH)).toBe(1);
      // No time has passed: the admin's session is still inside its back-off.
      server.routes[REFRESH] = () => json(authResponse(VIEWER));

      changeHands();

      // Their token is refused too, and their refresh goes out at once.
      expect(await getX()).toEqual({ owner: VIEWER.id });
      expect(server.times(REFRESH)).toBe(2);
    },
  );

  // The first refresh is held, and fails after the session has changed hands.
  it.each<[string, () => { first: Route; fail: () => void }]>([
    [
      "the network failing",
      () => {
        const held = deferred<Response>();
        return {
          first: () => held.promise,
          fail: () => {
            held.reject(NETWORK_ERROR);
          },
        };
      },
    ],
    [
      "an error body still being read",
      () => {
        // The headers of a 503 are in, and its body is not.
        const body = deferred<unknown>();
        return {
          first: () =>
            ({
              ok: false,
              status: 503,
              statusText: "Service Unavailable",
              headers: new Headers(),
              json: () => body.promise,
            }) as unknown as Response,
          fail: () => {
            body.resolve({ error: "x", message: "down" });
          },
        };
      },
    ],
  ])(
    "is not begun by a failure that arrives after the session ended, which is that session's and not the current one's: %s",
    async (_name, hold) => {
      storeTokens(authResponse(ADMIN));
      const { first, fail } = hold();
      server.routes[REFRESH] = answers(first, () => json(authResponse(VIEWER)));
      server.routes[X] = expiresAfter(2); // the admin's request, the viewer's first try
      const admins = readX();
      await untilSent(REFRESH);
      await flush();

      clearTokens();
      storeTokens(authResponse(VIEWER));
      fail();

      expect(await admins).toBeInstanceOf(StaleSessionError);
      expect(onFailure).not.toHaveBeenCalled();
      expect(await getX()).toEqual({ owner: VIEWER.id });
      expect(server.times(REFRESH)).toBe(2);
    },
  );
});

describe("a refresh failure, as the rest of the SPA reads an error", () => {
  /** A request refused, whose refresh then fails as `answer`. */
  async function failsWith(answer: Route): Promise<unknown> {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expired;
    server.routes[REFRESH] = answer;
    return readX();
  }

  it.each<[string, Route, string]>([
    [
      "a 429",
      tooMany(),
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
  ])(
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
  const unavailable = () =>
    json({ error: "service_unavailable", message: "Unavailable" }, 503);

  it("apiFetch is not sent as nobody when no token is held and the refresh fails", async () => {
    const client = await pageLoad();
    server.routes[REFRESH] = unavailable;
    server.routes[X] = () => json({ ok: true });

    await expect(
      client.apiFetch(apiPath`/api/v1/x`, { credentials: "same-origin" }),
    ).rejects.toMatchObject({ name: "RefreshFailedError", status: 503 });

    expect(server.times(X)).toBe(0);
    expect(onFailure).not.toHaveBeenCalled();
  });

  it("apiFetch sends a token that is about to expire when its refresh fails", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    server.routes[REFRESH] = unavailable;
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
  // JSON the server never sends for a refresh: a proxy's, an object that has a
  // user and nothing else. The first is the one that was stored as a session: a
  // token of `undefined`, permissions nothing could call .includes on.
  const session200 = (over: object) =>
    JSON.stringify({ ...authResponse(ADMIN), ...over });

  it.each<[string, string]>([
    ["a user with an id and nothing else", '{"user":{"id":"x"}}'],
    ["an empty object", "{}"],
    ["JSON null", "null"],
    ["a JSON array", "[]"],
    ["a user with no id", session200({ user: { email: ADMIN.email } })],
    ["a user id that is not a string", session200({ user: { id: 7 } })],
    ["no user", session200({ user: undefined })],
    ["an empty access token", session200({ access_token: "" })],
    ["no access token", session200({ access_token: undefined })],
    ["an access token that is not a string", session200({ access_token: 5 })],
    ["an expiry that is a string", session200({ expires_at: "soon" })],
    ["no expiry", session200({ expires_at: undefined })],
    [
      // JSON.stringify cannot write one; the parser reads 1e999 as Infinity.
      "an expiry that is not finite",
      session200({ expires_at: 1 }).replace(
        '"expires_at":1',
        '"expires_at":1e999',
      ),
    ],
    ["permissions that are an object", session200({ permissions: {} })],
    [
      "permissions that are a string",
      session200({ permissions: "view:cluster" }),
    ],
    ["permissions that are a number", session200({ permissions: 7 })],
  ])(
    "is a failed refresh, not a session, for %s: nothing is stored and the next refresh waits",
    async (_name, body) => {
      storeTokens(authResponse(ADMIN));
      const epoch = currentSessionEpoch();
      server.routes[X] = expiresAfter(Infinity);
      server.routes[REFRESH] = () => new Response(body, { status: 200 });

      const first = await readX();

      // The fixed words, with none of the parser's or of the TypeError's.
      renewalFailed(200, NOT_A_SESSION)(first);
      expect(onRefresh).not.toHaveBeenCalled();
      expect(onFailure).not.toHaveBeenCalled();
      expect(currentSessionEpoch()).toBe(epoch);
      // No half of an answer replaced the user the session held.
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
      expect(await readX()).toBe(first);
      expect(server.times(REFRESH)).toBe(1);
    },
  );

  // The server sends permissions as a list and never as null (loadPerms), but an
  // answer with none, or null, is not wrong about anything else: refused, it
  // would fail every refresh for good with nobody signed out. It is a session
  // with no permissions, the cautious reading.
  it.each([
    ["null", session200({ permissions: null })],
    ["missing", session200({ permissions: undefined })],
  ])(
    "is a session, with no permissions, when its permissions are %s",
    async (_name, body) => {
      storeTokens(authResponse(ADMIN, { permissions: ["view:cluster"] }));
      server.routes[X] = expiresAfter(1);
      server.routes[REFRESH] = () => new Response(body, { status: 200 });

      expect(await getX()).toEqual({ owner: ADMIN.id });

      expect(onFailure).not.toHaveBeenCalled();
      expect(server.times(REFRESH)).toBe(1);
      // Told as a list, whatever it was sent as: what it is told to takes .includes of it.
      expect(onRefresh).toHaveBeenCalledTimes(1);
      expect(onRefresh.mock.calls[0]?.[0].permissions).toEqual([]);
    },
  );

  it("control: a session-shaped answer is a session, is stored, and its permissions are passed on as they are", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(1);
    server.routes[REFRESH] = () =>
      new Response(
        JSON.stringify(
          authResponse(ADMIN, { permissions: ["view:cluster", "manage:user"] }),
        ),
        { status: 200 },
      );

    expect(await getX()).toEqual({ owner: ADMIN.id });

    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(server.times(REFRESH)).toBe(1);
    expect(onRefresh.mock.calls[0]?.[0].permissions).toEqual([
      "view:cluster",
      "manage:user",
    ]);
  });
});

describe("a refresh that fails after the session has changed hands", () => {
  /**
   * The session changes hands in the few microtasks between a refresh's failure
   * being judged and anyone seeing it. failed() reads the clock once,
   * synchronously, after it has judged the failure and before the rejection's
   * reactions run, so the first read queues the change, which runs before them.
   */
  function whenTheFailureIsJudged(change: () => void) {
    let fired = false;
    vi.spyOn(performance, "now").mockImplementation(() => {
      if (!fired) {
        fired = true;
        queueMicrotask(change);
      }
      return clockNow();
    });
  }

  // The ended session's request is told it is stale, not the failure of a refresh
  // that is no longer anybody's, and is not sent as the next user or as nobody.
  describe.each<[string, () => Promise<Client>]>([
    [
      "a token held, about to expire",
      () => {
        storeTokens(authResponse(ADMIN, nearExpiry));
        return Promise.resolve(api);
      },
    ],
    ["no token held", pageLoad],
  ])("with %s", (_name, open) => {
    it.each<[string, (client: Client) => void]>([
      [
        "someone signs in",
        (client) => {
          client.storeTokens(authResponse(VIEWER));
        },
      ],
      [
        "the session is signed out",
        (client) => {
          client.clearTokens();
        },
      ],
    ])(
      "is stale, and not sent, when %s as the failure is judged",
      async (_n, change) => {
        const client = await open();
        server.routes[REFRESH] = down;
        server.routes[X] = (init) => json({ owner: callerOf(init) });
        whenTheFailureIsJudged(() => {
          change(client);
        });

        const outcome = await readX(client);

        expect(outcome).toBeInstanceOf(client.StaleSessionError);
        expect(server.times(X)).toBe(0);
      },
    );
  });

  it("does not fail the ended session's request with a failure that is not its own: its 401 refreshed", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(Infinity);
    server.routes[REFRESH] = down;
    whenTheFailureIsJudged(() => {
      storeTokens(authResponse(VIEWER));
    });

    expect(await readX()).toBeInstanceOf(StaleSessionError);
    expect(server.times(X)).toBe(1); // refused once, and not replayed
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
  });

  describe("when the refresh is answered for someone else and the callback that was told throws", () => {
    // Another tab signed the viewer in on the shared cookie: refreshTokens begins
    // their session (storeTokens, a new epoch) and only then tells onAuthRefresh,
    // which here has a bug. The refresh rejects with it, long after the epoch moved.
    beforeEach(() => {
      setAuthRefreshCallback(() => {
        throw new Error("a listener with a bug");
      });
      server.routes[REFRESH] = () => json(authResponse(VIEWER));
    });

    it("does not send the admin's request under either user's token: a token held, about to expire", async () => {
      storeTokens(authResponse(ADMIN, nearExpiry));
      server.routes[X] = (init) => json({ owner: callerOf(init) });

      expect(await readX()).toBeInstanceOf(StaleSessionError);
      expect(server.times(X)).toBe(0);
      expect(getStoredUser()).toMatchObject({ id: VIEWER.id }); // theirs began
    });

    it("tells the admin's request it is stale and not what the callback threw: its 401 refreshed", async () => {
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(1);

      expect(await readX()).toBeInstanceOf(StaleSessionError);
      expect(server.times(X)).toBe(1); // not replayed as anyone
    });
  });
});

describe("a refresh that rotated the token and then failed", () => {
  // The refresh succeeded, the session's token is now a newer one, and a listener
  // of onAuthRefresh then threw, so the refresh rejects. The request that waited
  // on it is the same session's: it goes out with what is held NOW, not with the
  // token it was held with when it began.
  beforeEach(() => {
    setAuthRefreshCallback(() => {
      throw new Error("a listener with a bug");
    });
    server.routes[REFRESH] = () => json(rotatedTo(2));
    server.routes[X] = (init) => json({ owner: callerOf(init) });
  });

  it.each([
    ["about to expire", nearExpiry],
    ["past its expiry", { expiresIn: -10 }],
  ])(
    "is sent with the newer token: a token held, %s",
    async (_name, expiry) => {
      storeTokens(authResponse(ADMIN, expiry));

      expect(await getX()).toEqual({ owner: ROTATED });
      expect(server.times(REFRESH)).toBe(1);
    },
  );

  it("control: with no listener to fail, the same request is sent with the newer token", async () => {
    setAuthRefreshCallback(onRefresh);
    storeTokens(authResponse(ADMIN, nearExpiry));

    expect(await getX()).toEqual({ owner: ROTATED });
  });
});

describe("the back-off is counted from the failure, not from when the refresh began", () => {
  it("a refresh that takes 20 s to fail still holds the next one off for 5 s, and not a moment more", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(Infinity);
    const held = deferred<Response>();
    server.routes[REFRESH] = answers(() => held.promise, down);
    const first = readX();
    await untilSent(REFRESH);
    elapse(20_000); // the refresh is slow...
    held.resolve(down()); // ...and then fails
    await first;

    elapse(4_999); // inside the 5 s that follow the FAILURE
    await readX();
    expect(server.times(REFRESH)).toBe(1);

    elapse(1); // and over with them
    await readX();
    expect(server.times(REFRESH)).toBe(2);
  });
});

describe("a request refused for a token another request's refresh has replaced", () => {
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

  /** Two requests sent with the first token, both held; the refresh rotates it. */
  async function twoAreHeld(isRefused?: () => boolean) {
    storeTokens(authResponse(ADMIN));
    const epoch = currentSessionEpoch();
    const gates = holdsTheFirstTokensAnswers(isRefused);
    server.routes[REFRESH] = () => json(rotatedTo(2));
    const first = readX();
    const second = readX();
    await vi.waitFor(() => {
      expect(gates).toHaveLength(2);
    });
    return { epoch, gates, first, second };
  }

  it("is replayed with the token held, and no second refresh is made for it", async () => {
    const { epoch, gates, first, second } = await twoAreHeld();

    gates[0]?.resolve(expired());
    expect(await first).toBe("sent");
    expect(server.times(REFRESH)).toBe(1);
    // The second is refused AFTER that, for the token that has just been
    // replaced: a refresh of its own would rotate it again for nothing.
    gates[1]?.resolve(expired());
    expect(await second).toBe("sent");

    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(X)).toBe(4); // each sent twice
    expect(currentSessionEpoch()).toBe(epoch);
    expect(onFailure).not.toHaveBeenCalled();
  });

  it("is replayed once: if the token held is refused as well, that is the answer, and nothing refreshes again", async () => {
    let refuseTheNewTokenToo = false;
    const { gates, first, second } = await twoAreHeld(
      () => refuseTheNewTokenToo,
    );
    gates[0]?.resolve(expired());
    expect(await first).toBe("sent");

    refuseTheNewTokenToo = true;
    gates[1]?.resolve(expired());

    expect(await second).toMatchObject({ name: "ApiClientError", status: 401 });
    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(X)).toBe(4);
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
  });

  it("control: refused for the token that is still the one held, it refreshes, as ever", async () => {
    storeTokens(authResponse(ADMIN));
    const gates = holdsTheFirstTokensAnswers();
    server.routes[REFRESH] = () => json(rotatedTo(2));
    const only = readX();
    await vi.waitFor(() => {
      expect(gates).toHaveLength(1);
    });

    gates[0]?.resolve(expired());

    expect(await only).toBe("sent");
    expect(server.times(REFRESH)).toBe(1);
  });
});

describe("through the auth store: what the user is left with", () => {
  beforeEach(async () => {
    emptyPerSessionStores();
    useAuthStore.setState(SIGNED_OUT);
    // No session cookie yet. initialize() registers the forced-logout and
    // refresh callbacks, as main.tsx does at boot, in place of the harness's mocks.
    server.routes[REFRESH] = () => json({}, 401);
    await useAuthStore.getState().initialize();
    server.routes[LOGIN] = () => json(authResponse(ADMIN));
    await useAuthStore
      .getState()
      .login({ email: ADMIN.email, password: "example-password" });
    for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
    server.routes[X] = expired;
  });

  afterEach(() => {
    emptyPerSessionStores();
    useAuthStore.setState(SIGNED_OUT);
  });

  it.each<[string, Route]>([
    ["a 503", down],
    ["a 429", () => json({ error: "x", message: "slow down" }, 429)],
    ["the network failing", () => Promise.reject(NETWORK_ERROR)],
  ])(
    "a refresh that fails as %s leaves them signed in, with their console tabs and dismissed issues",
    async (_name, answer) => {
      // A server restart or a proxy's hiccup used to be a sign-out, and the end
      // of a session wipes what the user had open (stores/session-reset.ts).
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
        expect(probe.holdsData(), file).toBe(true);
      }
      server.routes[REFRESH] = answer;

      await readX();

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

    await readX();

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(getStoredUser()).toBeNull();
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(false);
    }
  });
});
