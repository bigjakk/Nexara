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
  installFakeServer,
  json,
  VIEWER,
  type FakeServer,
  type Route,
} from "@/test/fake-server";
import {
  apiClient,
  clearTokens,
  currentSessionEpoch,
  getStoredUser,
  resumeSession,
  setAuthFailureCallback,
  setAuthRefreshCallback,
  StaleSessionError,
  storeTokens,
} from "./api-client";
import { apiPath } from "./api-path";

/**
 * A refresh the server answers 409 "refresh_superseded": another tab's refresh,
 * made moments earlier, won the race for the cookie, and the server left this
 * one's in place because the jar holds, or is about to hold, the winner's newer
 * one (lib/api-client.ts, postRefresh). It is asked again once, after a short
 * jittered wait — inside the same lock hold, with the same epoch checks and
 * the same rules for whatever the second answer is — and if that is superseded
 * too it is a failed refresh like any other, never a sign-out.
 *
 * The wait is a setTimeout, which these tests fake, and only that: the jitter
 * is Math.random, which they pin (0 is 250 ms, and each 0.001 more is a quarter
 * of a millisecond).
 */

const REFRESH = "POST /api/v1/auth/refresh";
const X = "GET /api/v1/x";

let server: FakeServer;
let onFailure: Mock<() => void>;
let onRefresh: Mock<(res: AuthResponse) => void>;
let clock = 0;
let random = 0;

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
  // Only the timer: promises and the rest of the platform run as they are.
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  clearTokens();
  localStorage.clear();
});

/** Lets the fake timers run for `ms`, and what is waiting on them and on the fake server with it. */
async function go(ms: number) {
  await vi.advanceTimersByTimeAsync(ms);
}

/** Lets everything that is ready run, without moving the clock. */
async function pump() {
  for (let i = 0; i < 5; i++) await go(0);
}

/** What a request settles as: the error it failed with, or "sent". */
function settle(request: Promise<unknown>) {
  return request.then(
    () => "sent",
    (err: unknown) => err,
  );
}

const superseded = () =>
  json(
    {
      error: "refresh_superseded",
      message: "the refresh token was superseded by a newer one",
    },
    409,
  );
const expired = () => json({ error: "unauthorized", message: "expired" }, 401);
const session = () => json(authResponse(ADMIN));

/** The refresh answers, in order; the last one repeats. */
function answers(...list: Route[]): Route {
  let n = 0;
  return (init) => {
    const answer = list[Math.min(n, list.length - 1)];
    n++;
    if (answer === undefined) throw new Error("no answer to give");
    return answer(init);
  };
}

/** X refuses its first `refusals` requests as an expired token, then answers as the caller. */
function expiresAfter(refusals: number): Route {
  let reads = 0;
  return (init) =>
    ++reads <= refusals ? expired() : json({ owner: callerOf(init) });
}

describe("a refresh answered 409 refresh_superseded", () => {
  it("is asked once more, after the wait, and the request that needed it goes through", async () => {
    storeTokens(authResponse(ADMIN));
    const epoch = currentSessionEpoch();
    server.routes[X] = expiresAfter(1);
    server.routes[REFRESH] = answers(superseded, session);

    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    await pump();
    expect(server.times(REFRESH)).toBe(1); // the first answer is in: superseded

    // Pinned at 0, the wait is 250 ms: not a moment before it ...
    await go(249);
    expect(server.times(REFRESH)).toBe(1);
    await go(1);
    expect(server.times(REFRESH)).toBe(2);

    expect(await request).toBe("sent");
    expect(server.times(X)).toBe(2); // refused once, replayed under the new token
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
    expect(currentSessionEpoch()).toBe(epoch);
  });

  // A timer takes whole milliseconds, so the draw that is nearly 1 waits 499.
  it.each([
    [0, 250],
    [0.5, 375],
    [0.999, 499],
  ])(
    "waits 250 ms and up to 250 more: Math.random() = %d is %d ms",
    async (draw, waits) => {
      random = draw;
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(1);
      server.routes[REFRESH] = answers(superseded, session);

      const request = settle(apiClient.get(apiPath`/api/v1/x`));
      await pump();
      expect(server.times(REFRESH)).toBe(1);

      await go(waits - 1);
      expect(server.times(REFRESH)).toBe(1);
      await go(1);
      expect(server.times(REFRESH)).toBe(2);
      expect(await request).toBe("sent");
    },
  );

  it("asked once more is asked once: three requests waiting on it share the two sends", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(3);
    server.routes[REFRESH] = answers(superseded, session);

    const requests = [1, 2, 3].map(() =>
      settle(apiClient.get(apiPath`/api/v1/x`)),
    );
    await pump();
    await go(250);

    expect(await Promise.all(requests)).toEqual(["sent", "sent", "sent"]);
    expect(server.times(REFRESH)).toBe(2);
  });

  it("is a failed refresh like any other when the second answer is superseded too: the session stands, and the next refresh waits", async () => {
    storeTokens(authResponse(ADMIN));
    const epoch = currentSessionEpoch();
    server.routes[X] = expiresAfter(Infinity);
    server.routes[REFRESH] = answers(superseded); // every time

    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    await pump();
    await go(250);
    const first = await request;

    // A RefreshFailedError, not an ApiClientError, carrying the 409 and the
    // server's own words; never a sign-out.
    expect(first).toMatchObject({
      name: "RefreshFailedError",
      status: 409,
      message:
        "The session could not be renewed (HTTP 409: the refresh token was superseded by a newer one)",
    });
    expect(server.times(REFRESH)).toBe(2);
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
    expect(currentSessionEpoch()).toBe(epoch);

    // The back-off was recorded from the second answer: the next request is
    // turned away at once, sends nothing, and does not wait to be.
    expect(await settle(apiClient.get(apiPath`/api/v1/x`))).toBe(first);
    expect(server.times(REFRESH)).toBe(2);

    // Over, it asks again — and, superseded again, with the one retry again.
    clock += 5_000;
    const later = settle(apiClient.get(apiPath`/api/v1/x`));
    await pump();
    expect(server.times(REFRESH)).toBe(3);
    await go(250);
    await later;
    expect(server.times(REFRESH)).toBe(4);
  });

  describe("whatever the second answer is, it is handled as any answer would be", () => {
    const CASES: [string, Route, (err: unknown) => void][] = [
      [
        "a 503",
        () => json({ error: "x", message: "down" }, 503),
        (err) => {
          expect(err).toMatchObject({
            name: "RefreshFailedError",
            status: 503,
            message: "The session could not be renewed (HTTP 503: down)",
          });
        },
      ],
      [
        "a body that is no session",
        () => json({ version: "dev" }),
        (err) => {
          expect(err).toMatchObject({
            name: "RefreshFailedError",
            status: 200,
            message:
              "The session could not be renewed (the server's answer was not a session)",
          });
        },
      ],
    ];

    it.each(CASES)(
      "%s is a failed refresh, and begins the back-off",
      async (_name, second, check) => {
        storeTokens(authResponse(ADMIN));
        server.routes[X] = expiresAfter(Infinity);
        server.routes[REFRESH] = answers(superseded, second);

        const request = settle(apiClient.get(apiPath`/api/v1/x`));
        await pump();
        await go(250);
        const outcome = await request;

        check(outcome);
        expect(onFailure).not.toHaveBeenCalled();
        expect(await settle(apiClient.get(apiPath`/api/v1/x`))).toBe(outcome);
        expect(server.times(REFRESH)).toBe(2);
      },
    );

    it("the network failing is the network's own error, and begins the back-off", async () => {
      const down = new TypeError("Failed to fetch");
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(Infinity);
      server.routes[REFRESH] = answers(superseded, () => Promise.reject(down));

      const request = settle(apiClient.get(apiPath`/api/v1/x`));
      await pump();
      await go(250);

      expect(await request).toBe(down);
      expect(onFailure).not.toHaveBeenCalled();
      expect(await settle(apiClient.get(apiPath`/api/v1/x`))).toBe(down);
      expect(server.times(REFRESH)).toBe(2);
    });

    it("a refusal ends the session, once", async () => {
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(Infinity);
      server.routes[REFRESH] = answers(superseded, () =>
        json({ error: "unauthorized", message: "Invalid or expired" }, 401),
      );

      const request = settle(apiClient.get(apiPath`/api/v1/x`));
      await pump();
      await go(250);

      expect(await request).toMatchObject({
        name: "ApiClientError",
        status: 401,
        message: "Session expired",
      });
      expect(onFailure).toHaveBeenCalledTimes(1);
      expect(getStoredUser()).toBeNull();
      expect(server.times(REFRESH)).toBe(2);
    });
  });

  it("is not asked again for a session that ended during the wait: nothing more is sent", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(Infinity);
    server.routes[REFRESH] = answers(superseded, session);
    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    await pump();
    expect(server.times(REFRESH)).toBe(1);

    clearTokens(); // signed out while the wait runs
    storeTokens(authResponse(VIEWER)); // and the next user is in
    await go(250);

    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(server.times(REFRESH)).toBe(1);
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
  });
});

describe("a 409 that is not a refresh_superseded", () => {
  it.each([
    [
      "another slug",
      () => json({ error: "conflict", message: "state has changed" }, 409),
      "HTTP 409: state has changed",
    ],
    [
      "no body at all",
      () => new Response(null, { status: 409, statusText: "Conflict" }),
      "HTTP 409: Conflict",
    ],
    [
      "a body that is not JSON",
      () =>
        new Response("<html>conflict</html>", {
          status: 409,
          statusText: "Conflict",
        }),
      "HTTP 409: Conflict",
    ],
    [
      "the slug on a status that is not 409",
      () =>
        json(
          { error: "refresh_superseded", message: "superseded, said a 503" },
          503,
        ),
      "HTTP 503: superseded, said a 503",
    ],
  ] satisfies [string, Route, string][])(
    "is not asked again, for %s, and is an ordinary failure with the server's own words",
    async (_name, answer, reason) => {
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(Infinity);
      server.routes[REFRESH] = answer;

      const request = settle(apiClient.get(apiPath`/api/v1/x`));
      await pump();
      await go(1_000); // far longer than any wait there is
      const outcome = await request;

      expect(outcome).toMatchObject({
        name: "RefreshFailedError",
        message: `The session could not be renewed (${reason})`,
      });
      expect(server.times(REFRESH)).toBe(1);
      expect(onFailure).not.toHaveBeenCalled();
    },
  );
});

describe("the boot resume's refresh (resumeSession)", () => {
  it("asks once more when the server says another tab won, and resolves with the session", async () => {
    server.routes[REFRESH] = answers(superseded, session);

    const resumed = resumeSession(currentSessionEpoch());
    await pump();
    expect(server.times(REFRESH)).toBe(1);
    await go(249);
    expect(server.times(REFRESH)).toBe(1);
    await go(1);

    await expect(resumed).resolves.toMatchObject({ user: { id: ADMIN.id } });
    expect(server.times(REFRESH)).toBe(2);
  });

  it("does not ask again for a 409 that is another's, and could not look: a RefreshFailedError", async () => {
    server.routes[REFRESH] = () =>
      json({ error: "conflict", message: "state has changed" }, 409);

    const resumed = settle(resumeSession(currentSessionEpoch()));
    await pump();
    await go(1_000);

    expect(await resumed).toMatchObject({
      name: "RefreshFailedError",
      status: 409,
      message: "The session could not be renewed (HTTP 409: state has changed)",
    });
    expect(server.times(REFRESH)).toBe(1);
  });

  it("rejects with the second answer when that is superseded too: could not look", async () => {
    server.routes[REFRESH] = answers(superseded);

    const resumed = settle(resumeSession(currentSessionEpoch()));
    await pump();
    await go(250);

    expect(await resumed).toMatchObject({
      name: "RefreshFailedError",
      status: 409,
    });
    expect(server.times(REFRESH)).toBe(2);
  });

  it.each([401, 403])(
    "rejects a %i, a refusal, with an ApiClientError of its status",
    async (status) => {
      server.routes[REFRESH] = () =>
        json({ error: "unauthorized", message: "Invalid or expired" }, status);

      const resumed = settle(resumeSession(currentSessionEpoch()));
      await pump();

      expect(await resumed).toMatchObject({ name: "ApiClientError", status });
      expect(server.times(REFRESH)).toBe(1);
    },
  );

  it("carries what a failure asked it to wait", async () => {
    server.routes[REFRESH] = () =>
      new Response("{}", { status: 429, headers: { "Retry-After": "3" } });

    const resumed = settle(resumeSession(currentSessionEpoch()));
    await pump();

    expect(await resumed).toMatchObject({
      name: "RefreshFailedError",
      status: 429,
      retryAfterMs: 3_000,
    });
  });

  it("rejects, as an answer that is no session, when it is none", async () => {
    server.routes[REFRESH] = () => json({ user: { id: "x" } });

    const resumed = settle(resumeSession(currentSessionEpoch()));
    await pump();

    expect(await resumed).toMatchObject({
      name: "RefreshFailedError",
      status: 200,
    });
  });

  it("is not asked again for a session that ended during the wait", async () => {
    server.routes[REFRESH] = answers(superseded, session);
    const resumed = settle(resumeSession(currentSessionEpoch()));
    await pump();
    expect(server.times(REFRESH)).toBe(1);

    storeTokens(authResponse(VIEWER)); // someone signs in meanwhile
    await go(250);

    expect(await resumed).toBeInstanceOf(StaleSessionError);
    expect(server.times(REFRESH)).toBe(1);
  });
});
