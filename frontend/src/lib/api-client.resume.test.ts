import { describe, expect, it, vi } from "vitest";
import {
  VIEWER,
  authResponse,
  deferred,
  json,
  type Route,
} from "@/test/fake-server";
import { installFakeLocks } from "@/test/fake-lock-manager";
import {
  LOCK,
  REFRESH,
  answers,
  down,
  go,
  orStalled,
  pinRandom,
  pump,
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
  resumeSessionPatiently,
  StaleSessionError,
  storeTokens,
} from "./api-client";

/**
 * The boot resume that does not give up on its first failure (lib/api-client.ts,
 * resumeSessionPatiently): resumeSession asked up to four times in all, 1 s, 2 s
 * and 4 s apart, each wait with up to 1 s of jitter or the failure's Retry-After
 * if longer, never more than 8 s. Only a cookie the server REFUSED (401, 403) or
 * a session that changed hands ends it early; any other failure is "could not
 * look" and is asked again until the attempts run out.
 */

installApiClientHarness({ random: true, timers: true });

const resume = () =>
  settle(resumeSessionPatiently(currentSessionEpoch()), "resumed");

const refusal = (status: number): Route => {
  return () =>
    json({ error: "unauthorized", message: "Invalid or expired" }, status);
};

describe("a resume that could not look", () => {
  it("is asked again after 1 s, and resolves with the session", async () => {
    server.routes[REFRESH] = answers(down, session);
    const resumed = resume();
    await pump();
    expect(server.times(REFRESH)).toBe(1);

    await go(999);
    expect(server.times(REFRESH)).toBe(1); // not a moment sooner
    await go(1);

    expect(await resumed).toBe("resumed");
    expect(server.times(REFRESH)).toBe(2);
  });

  // Tabs that lost together must not ask again together.
  it.each([
    [0, [1_000, 2_000, 4_000]],
    [0.5, [1_500, 2_500, 4_500]],
    [0.999, [1_999, 2_999, 4_999]],
  ])(
    "waits 1 s, 2 s, 4 s and up to a second more each, then gives up with the last failure: Math.random() = %d waits %j ms",
    async (draw, waits) => {
      pinRandom(draw);
      server.routes[REFRESH] = down;
      const resumed = resume();
      await pump();

      let sent = 1;
      for (const wait of waits) {
        expect(server.times(REFRESH)).toBe(sent);
        await go(wait - 1);
        expect(server.times(REFRESH)).toBe(sent); // not a moment sooner
        await go(1);
        expect(server.times(REFRESH)).toBe(++sent);
      }

      // The fourth is the last: its failure is the answer, and nothing follows.
      expect(await resumed).toMatchObject({
        name: "RefreshFailedError",
        status: 503,
      });
      await go(60_000);
      expect(server.times(REFRESH)).toBe(4);
      expect(vi.getTimerCount()).toBe(0);
    },
  );

  describe("honours a Retry-After, as long as it is within 8 s", () => {
    it.each([
      ["3", 3_000], // longer than the 1 s it would have waited
      ["0", 1_000], // nothing to honour: the schedule's own
      ["120", 8_000], // a limiter's minute is not a spinner's wait
    ])("a 429 with Retry-After: %s waits %d ms", async (retryAfter, waits) => {
      server.routes[REFRESH] = answers(tooMany(retryAfter), session);
      const resumed = resume();
      await pump();

      await go(waits - 1);
      expect(server.times(REFRESH)).toBe(1);
      await go(1);

      expect(await resumed).toBe("resumed");
      expect(server.times(REFRESH)).toBe(2);
    });

    it("a Retry-After shorter than the wait it would have made does not shorten it", async () => {
      // The second wait is 2 s; the answer asked for 1.
      server.routes[REFRESH] = answers(down, tooMany("1"), session);
      const resumed = resume();
      await pump();
      await go(1_000);
      expect(server.times(REFRESH)).toBe(2);

      await go(1_999);
      expect(server.times(REFRESH)).toBe(2);
      await go(1);

      expect(await resumed).toBe("resumed");
      expect(server.times(REFRESH)).toBe(3);
    });
  });

  describe("every failure that is not a refusal is asked again", () => {
    it.each<[string, Route]>([
      ["a 503", down],
      ["a 429", tooMany()],
      ["a 500", () => json({ error: "x", message: "broke" }, 500)],
      [
        "a 409 that is not a superseded refresh",
        () => json({ error: "conflict", message: "no" }, 409),
      ],
      [
        "the network failing",
        () => Promise.reject(new TypeError("Failed to fetch")),
      ],
      ["an answer that is no session", () => json({ user: { id: "x" } })],
      [
        "an answer that is not JSON",
        () => new Response("<html>sign in</html>", { status: 200 }),
      ],
    ])("%s, then the session", async (_name, failure) => {
      server.routes[REFRESH] = answers(failure, session);
      const resumed = resume();
      await pump();

      await go(1_000);

      expect(await resumed).toBe("resumed");
      expect(server.times(REFRESH)).toBe(2);
    });

    it("a refresh that lost its race twice, then the session on the next attempt", async () => {
      // Each attempt asks twice of a superseded answer (250 ms apart); both
      // lost, so the attempt fails, and the next one a second later succeeds.
      server.routes[REFRESH] = answers(superseded, superseded, session);
      const resumed = resume();
      await pump();
      await go(250);
      expect(server.times(REFRESH)).toBe(2); // the attempt's own second ask
      await go(999);
      expect(server.times(REFRESH)).toBe(2);
      await go(1);

      expect(await resumed).toBe("resumed");
      expect(server.times(REFRESH)).toBe(3);
    });
  });
});

describe("a resume that is refused", () => {
  it.each([
    [401, 0],
    [403, 0],
    [401, 1],
    [403, 1],
  ])(
    "ends at once on a %i after %i earlier failures: no further attempt, whatever time passes",
    async (status, failures) => {
      const earlier = Array.from({ length: failures }, () => down);
      server.routes[REFRESH] = answers(...earlier, refusal(status));
      const resumed = resume();
      await pump();
      await go(failures * 1_000);

      expect(await resumed).toMatchObject({ name: "ApiClientError", status });
      await go(60_000);
      expect(server.times(REFRESH)).toBe(failures + 1);
    },
  );
});

describe("a resume whose session changes hands", () => {
  it("stops, and sends nothing more, when someone signs in during the wait", async () => {
    server.routes[REFRESH] = answers(down, session);
    const resumed = resume();
    await pump();
    expect(server.times(REFRESH)).toBe(1);

    storeTokens(authResponse(VIEWER));
    await go(1_000);

    expect(await resumed).toBeInstanceOf(StaleSessionError);
    expect(server.times(REFRESH)).toBe(1);
  });

  it("stops when the session ended during the wait, too", async () => {
    server.routes[REFRESH] = answers(down, session);
    const resumed = resume();
    await pump();

    clearTokens();
    await go(1_000);

    expect(await resumed).toBeInstanceOf(StaleSessionError);
    expect(server.times(REFRESH)).toBe(1);
  });

  it("does not queue for the lock another tab holds, for an attempt that a sign-in during the wait made pointless", async () => {
    // An attempt that went ahead would stand in the queue for up to 15 s, the
    // page a spinner all that time, to be told what the sign-in already made known.
    const lock = installFakeLocks();
    server.routes[REFRESH] = answers(down, session);
    const resumed = resume();
    await pump();
    expect(server.times(REFRESH)).toBe(1); // the first attempt, which failed
    void lock.request(LOCK, {}, () => deferred<undefined>().promise); // another tab's refresh hangs
    storeTokens(authResponse(VIEWER));

    await go(1_000); // and the wait is over

    expect(await orStalled(resumed, go)).toBeInstanceOf(StaleSessionError);
    expect(lock.waiting).toBe(0);
    expect(server.times(REFRESH)).toBe(1);
  });

  it("is told so, and not the failure, when the session changed hands during the last attempt", async () => {
    // initialize() treats a stale answer as another session's, and the failure
    // of the previous one's as its own.
    const last = deferred<Response>();
    server.routes[REFRESH] = answers(down, down, down, () => last.promise);
    const resumed = resume();
    await pump();
    await go(1_000);
    await go(2_000);
    await go(4_000);
    expect(server.times(REFRESH)).toBe(4); // the fourth, held

    storeTokens(authResponse(VIEWER));
    last.resolve(down());

    expect(await resumed).toBeInstanceOf(StaleSessionError);
  });
});

describe("a resume whose session changes hands during a failing attempt", () => {
  it("stops at once instead of sleeping, and sends nothing more", async () => {
    const attempt = deferred<Response>();
    server.routes[REFRESH] = () => attempt.promise;
    const resumed = resume();
    await pump();
    expect(server.times(REFRESH)).toBe(1); // the first attempt is out

    storeTokens(authResponse(VIEWER)); // someone signs in while it is
    attempt.resolve(down()); // and it fails
    await pump();

    // Settled without the clock moving: the 1 s wait for a next attempt that
    // would only have found the session is not its own was never started.
    expect(await orStalled(resumed, go)).toBeInstanceOf(StaleSessionError);
    expect(vi.getTimerCount()).toBe(0);
    expect(server.times(REFRESH)).toBe(1);
  });

  it("control: with nobody signed in, the same failure is waited out and asked again", async () => {
    const attempt = deferred<Response>();
    server.routes[REFRESH] = answers(() => attempt.promise, session);
    const resumed = resume();
    await pump();

    attempt.resolve(down());
    await pump();
    expect(vi.getTimerCount()).toBe(1); // the wait
    await go(1_000);

    expect(await resumed).toBe("resumed");
    expect(server.times(REFRESH)).toBe(2);
  });
});

describe("a resume where the browser has locks", () => {
  it("takes the lock for each attempt, and does not hold it through the wait between them", async () => {
    const lock = installFakeLocks();
    server.routes[REFRESH] = answers(down, session);
    const resumed = resume();
    await pump();
    expect(lock.granted).toHaveLength(1);

    // The first attempt failed and is waiting to ask again: the lock is free.
    let other = "not granted";
    void lock.request(LOCK, {}, () => {
      other = "granted";
      return Promise.resolve();
    });
    await pump();
    expect(other).toBe("granted");

    await go(1_000);
    expect(await resumed).toBe("resumed");
    expect(lock.granted).toHaveLength(3); // the first attempt, the other tab's, the second
  });
});
