import { describe, expect, it } from "vitest";
import {
  ADMIN,
  VIEWER,
  authResponse,
  json,
  type Route,
} from "@/test/fake-server";
import {
  REFRESH,
  X,
  answers,
  elapse,
  expiresAfter,
  go,
  onFailure,
  onRefresh,
  pinRandom,
  pump,
  readX,
  server,
  session,
  settle,
  superseded,
  tooMany,
  installApiClientHarness,
} from "@/test/api-client-harness";
import {
  clearTokens,
  currentSessionEpoch,
  getStoredUser,
  resumeSession,
  StaleSessionError,
  storeTokens,
} from "./api-client";

/**
 * A refresh answered 409 "refresh_superseded": another tab's refresh won the
 * race for the cookie (lib/api-client.ts, postRefresh). It is asked once more
 * after a jittered 250-500 ms, inside the same lock hold and under the same epoch
 * checks; superseded again, it is a failed refresh like any other, never a
 * sign-out. The wait is a faked setTimeout; its jitter is the pinned Math.random.
 */

installApiClientHarness({ clock: true, random: true, timers: true });

const NETWORK = new TypeError("Failed to fetch");

describe("a refresh answered 409 refresh_superseded", () => {
  // A timer takes whole milliseconds, so the draw that is nearly 1 waits 499.
  it.each([
    [0, 250],
    [0.5, 375],
    [0.999, 499],
  ])(
    "is asked once more after 250 ms and up to 250 more, and the request goes through: Math.random() = %d waits %d ms",
    async (draw, waits) => {
      pinRandom(draw);
      storeTokens(authResponse(ADMIN));
      const epoch = currentSessionEpoch();
      server.routes[X] = expiresAfter(1);
      server.routes[REFRESH] = answers(superseded, session);

      const request = readX();
      await pump();
      expect(server.times(REFRESH)).toBe(1); // superseded

      await go(waits - 1);
      expect(server.times(REFRESH)).toBe(1);
      await go(1);
      expect(server.times(REFRESH)).toBe(2);

      expect(await request).toBe("sent");
      expect(server.times(X)).toBe(2); // refused once, replayed under the new token
      expect(onRefresh).toHaveBeenCalledTimes(1);
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
      expect(currentSessionEpoch()).toBe(epoch);
    },
  );

  it("asked once more is asked once: three requests waiting on it share the two sends", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = expiresAfter(3);
    server.routes[REFRESH] = answers(superseded, session);

    const requests = [1, 2, 3].map(() => readX());
    await pump();
    await go(250);

    expect(await Promise.all(requests)).toEqual(["sent", "sent", "sent"]);
    expect(server.times(REFRESH)).toBe(2);
  });

  it("is a failed refresh like any other when the second answer is superseded too: the session stands, and the next refresh waits", async () => {
    storeTokens(authResponse(ADMIN));
    const epoch = currentSessionEpoch();
    server.routes[X] = expiresAfter(Infinity);
    server.routes[REFRESH] = superseded; // every time

    const request = readX();
    await pump();
    await go(250);
    const first = await request;

    // A RefreshFailedError carrying the 409 and the server's words, not a sign-out.
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

    // The back-off was recorded from the second answer: nothing is sent.
    expect(await readX()).toBe(first);
    expect(server.times(REFRESH)).toBe(2);

    // Over, it asks again, and with the one retry again.
    elapse(5_000);
    const later = readX();
    await pump();
    expect(server.times(REFRESH)).toBe(3);
    await go(250);
    await later;
    expect(server.times(REFRESH)).toBe(4);
  });

  describe("whatever the second answer is, it is handled as any answer would be", () => {
    it.each<[string, Route, (err: unknown) => void]>([
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
      [
        "the network failing",
        () => Promise.reject(NETWORK),
        (err) => {
          expect(err).toBe(NETWORK);
        },
      ],
    ])(
      "%s is a failed refresh, and begins the back-off",
      async (_name, second, check) => {
        storeTokens(authResponse(ADMIN));
        server.routes[X] = expiresAfter(Infinity);
        server.routes[REFRESH] = answers(superseded, second);

        const request = readX();
        await pump();
        await go(250);
        const outcome = await request;

        check(outcome);
        expect(onFailure).not.toHaveBeenCalled();
        expect(await readX()).toBe(outcome);
        expect(server.times(REFRESH)).toBe(2);
      },
    );

    it("a refusal ends the session, once", async () => {
      storeTokens(authResponse(ADMIN));
      server.routes[X] = expiresAfter(Infinity);
      server.routes[REFRESH] = answers(superseded, () =>
        json({ error: "unauthorized", message: "Invalid or expired" }, 401),
      );

      const request = readX();
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
    const request = readX();
    await pump();
    expect(server.times(REFRESH)).toBe(1);

    clearTokens();
    storeTokens(authResponse(VIEWER)); // signed out during the wait, and the next user is in
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

      const request = readX();
      await pump();
      await go(1_000); // far longer than any wait there is

      expect(await request).toMatchObject({
        name: "RefreshFailedError",
        message: `The session could not be renewed (${reason})`,
      });
      expect(server.times(REFRESH)).toBe(1);
      expect(onFailure).not.toHaveBeenCalled();
    },
  );
});

describe("the boot resume's refresh (resumeSession)", () => {
  const resume = () => settle(resumeSession(currentSessionEpoch()));

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

  const refusal = (status: number) => () =>
    json({ error: "unauthorized", message: "Invalid or expired" }, status);

  it.each<[string, Route, number, object, number]>([
    [
      "a 409 that is another's: not asked a second time",
      () => json({ error: "conflict", message: "state has changed" }, 409),
      1_000,
      {
        name: "RefreshFailedError",
        status: 409,
        message:
          "The session could not be renewed (HTTP 409: state has changed)",
      },
      1,
    ],
    [
      "a second superseded answer: could not look",
      superseded,
      250,
      { name: "RefreshFailedError", status: 409 },
      2,
    ],
    [
      "a 401, a refusal",
      refusal(401),
      0,
      { name: "ApiClientError", status: 401 },
      1,
    ],
    [
      "a 403, a refusal",
      refusal(403),
      0,
      { name: "ApiClientError", status: 403 },
      1,
    ],
    [
      "a failure that asked it to wait",
      tooMany("3"),
      0,
      { name: "RefreshFailedError", status: 429, retryAfterMs: 3_000 },
      1,
    ],
    [
      "an answer that is no session",
      () => json({ user: { id: "x" } }),
      0,
      { name: "RefreshFailedError", status: 200 },
      1,
    ],
  ])(
    "rejects, for %s",
    async (_name, answer, advance, rejection, refreshes) => {
      server.routes[REFRESH] = answer;

      const resumed = resume();
      await pump();
      await go(advance);

      expect(await resumed).toMatchObject(rejection);
      expect(server.times(REFRESH)).toBe(refreshes);
    },
  );

  it("is not asked again for a session that ended during the wait", async () => {
    server.routes[REFRESH] = answers(superseded, session);
    const resumed = resume();
    await pump();
    expect(server.times(REFRESH)).toBe(1);

    storeTokens(authResponse(VIEWER)); // someone signs in meanwhile
    await go(250);

    expect(await resumed).toBeInstanceOf(StaleSessionError);
    expect(server.times(REFRESH)).toBe(1);
  });
});
