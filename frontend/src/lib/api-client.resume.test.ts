import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ADMIN,
  VIEWER,
  authResponse,
  deferred,
  installFakeServer,
  json,
  type FakeServer,
  type Route,
} from "@/test/fake-server";
import { installFakeLocks, removeFakeLocks } from "@/test/fake-lock-manager";
import {
  clearTokens,
  currentSessionEpoch,
  resumeSessionPatiently,
  StaleSessionError,
  storeTokens,
} from "./api-client";

/**
 * The boot resume that does not give up on its first failure
 * (lib/api-client.ts, resumeSessionPatiently): resumeSession, asked again up to
 * four times in all while the page waits on its spinner, with 1 s, 2 s and 4 s
 * between the attempts — each with up to a second of jitter on top, or the
 * Retry-After of the failure if that is longer, and no wait more than 8 s.
 * Only a cookie the server REFUSED (401, 403) ends it at once, and so does a
 * session that changed hands meanwhile; every other failure is "could not
 * look" and is asked again until the attempts run out.
 *
 * The waits are setTimeouts, which these tests fake, and only those; the
 * jitter is Math.random, which they pin (0, unless a test says otherwise).
 */

const REFRESH = "POST /api/v1/auth/refresh";

let server: FakeServer;
let random = 0;

beforeEach(() => {
  localStorage.clear();
  clearTokens();
  server = installFakeServer();
  random = 0;
  vi.spyOn(Math, "random").mockImplementation(() => random);
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  removeFakeLocks();
  clearTokens();
  localStorage.clear();
});

async function go(ms = 0) {
  await vi.advanceTimersByTimeAsync(ms);
}

/** Lets everything that is ready run, without moving the clock. */
async function pump() {
  for (let i = 0; i < 5; i++) await go();
}

function settle(request: Promise<unknown>) {
  return request.then(
    () => "resumed",
    (err: unknown) => err,
  );
}

const down = () => json({ error: "x", message: "down" }, 503);
const session = () => json(authResponse(ADMIN));
const superseded = () =>
  json({ error: "refresh_superseded", message: "a newer refresh won" }, 409);

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

/** Starts a resume. `elapse` moves the clock, and lets whatever is waiting on it run. */
function resume() {
  return {
    outcome: settle(resumeSessionPatiently(currentSessionEpoch())),
    elapse: (ms: number) => go(ms),
  };
}

describe("a resume that could not look", () => {
  it("is asked again after 1 s, and resolves with the session", async () => {
    server.routes[REFRESH] = answers(down, session);
    const resumed = resume();
    await pump();
    expect(server.times(REFRESH)).toBe(1);

    await resumed.elapse(999);
    expect(server.times(REFRESH)).toBe(1); // not a moment sooner
    await resumed.elapse(1);

    expect(await resumed.outcome).toBe("resumed");
    expect(server.times(REFRESH)).toBe(2);
  });

  it("waits 1 s, then 2 s, then 4 s, and then gives up with the last failure", async () => {
    server.routes[REFRESH] = answers(down);
    const resumed = resume();
    await pump();
    expect(server.times(REFRESH)).toBe(1);

    await resumed.elapse(999);
    expect(server.times(REFRESH)).toBe(1);
    await resumed.elapse(1); // 1 s
    expect(server.times(REFRESH)).toBe(2);

    await resumed.elapse(1_999);
    expect(server.times(REFRESH)).toBe(2);
    await resumed.elapse(1); // 3 s
    expect(server.times(REFRESH)).toBe(3);

    await resumed.elapse(3_999);
    expect(server.times(REFRESH)).toBe(3);
    await resumed.elapse(1); // 7 s
    expect(server.times(REFRESH)).toBe(4);

    // The fourth is the last: its failure is the answer, and nothing follows.
    expect(await resumed.outcome).toMatchObject({
      name: "RefreshFailedError",
      status: 503,
    });
    await resumed.elapse(60_000);
    expect(server.times(REFRESH)).toBe(4);
    expect(vi.getTimerCount()).toBe(0);
  });

  // Tabs that lost together must not ask again together.
  it.each([
    [0.5, [1_500, 2_500, 4_500]],
    [0.999, [1_999, 2_999, 4_999]],
  ])(
    "adds up to a second of jitter to each wait: Math.random() = %d waits %j ms",
    async (draw, waits) => {
      random = draw;
      server.routes[REFRESH] = answers(down);
      const resumed = resume();
      await pump();

      let sent = 1;
      for (const wait of waits) {
        await resumed.elapse(wait - 1);
        expect(server.times(REFRESH)).toBe(sent);
        await resumed.elapse(1);
        sent += 1;
        expect(server.times(REFRESH)).toBe(sent);
      }
      await resumed.outcome;
    },
  );

  describe("honours a Retry-After, as long as it is within 8 s", () => {
    const tooMany = (retryAfter: string): Route => {
      return () =>
        new Response(JSON.stringify({ error: "x", message: "slow down" }), {
          status: 429,
          headers: {
            "Content-Type": "application/json",
            "Retry-After": retryAfter,
          },
        });
    };

    it.each([
      ["3", 3_000], // longer than the 1 s it would have waited
      ["0", 1_000], // nothing to honour: the schedule's own
      ["120", 8_000], // a limiter's minute is not a spinner's wait
    ])("a 429 with Retry-After: %s waits %d ms", async (retryAfter, waits) => {
      server.routes[REFRESH] = answers(tooMany(retryAfter), session);
      const resumed = resume();
      await pump();

      await resumed.elapse(waits - 1);
      expect(server.times(REFRESH)).toBe(1);
      await resumed.elapse(1);

      expect(await resumed.outcome).toBe("resumed");
      expect(server.times(REFRESH)).toBe(2);
    });

    it("a Retry-After shorter than the wait it would have made does not shorten it", async () => {
      // The second wait is 2 s; the answer asked for 1.
      server.routes[REFRESH] = answers(down, tooMany("1"), session);
      const resumed = resume();
      await pump();
      await resumed.elapse(1_000);
      expect(server.times(REFRESH)).toBe(2);

      await resumed.elapse(1_999);
      expect(server.times(REFRESH)).toBe(2);
      await resumed.elapse(1);

      expect(await resumed.outcome).toBe("resumed");
      expect(server.times(REFRESH)).toBe(3);
    });
  });

  describe("every failure that is not a refusal is asked again", () => {
    const CASES: [string, Route][] = [
      ["a 503", down],
      ["a 429", () => json({ error: "x", message: "slow down" }, 429)],
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
    ];

    it.each(CASES)("%s, then the session", async (_name, failure) => {
      server.routes[REFRESH] = answers(failure, session);
      const resumed = resume();
      await pump();

      await resumed.elapse(1_000);

      expect(await resumed.outcome).toBe("resumed");
      expect(server.times(REFRESH)).toBe(2);
    });

    it("a refresh that lost its race twice, then the session on the next attempt", async () => {
      // Each attempt asks twice of a superseded answer (250 ms apart); both lost,
      // so the attempt fails — and the next one, a second later, succeeds.
      server.routes[REFRESH] = answers(superseded, superseded, session);
      const resumed = resume();
      await pump();
      await resumed.elapse(250);
      expect(server.times(REFRESH)).toBe(2); // the attempt's own second ask
      await resumed.elapse(999);
      expect(server.times(REFRESH)).toBe(2);
      await resumed.elapse(1);

      expect(await resumed.outcome).toBe("resumed");
      expect(server.times(REFRESH)).toBe(3);
    });
  });
});

describe("a resume that is refused", () => {
  it.each([401, 403])(
    "ends at once on a %i: no further attempt, whatever time passes",
    async (status) => {
      server.routes[REFRESH] = () =>
        json({ error: "unauthorized", message: "Invalid or expired" }, status);
      const resumed = resume();
      await pump();

      expect(await resumed.outcome).toMatchObject({
        name: "ApiClientError",
        status,
      });
      await resumed.elapse(60_000);
      expect(server.times(REFRESH)).toBe(1);
    },
  );

  it.each([401, 403])(
    "ends at once on a %i at a later attempt, too",
    async (status) => {
      server.routes[REFRESH] = answers(down, () =>
        json({ error: "unauthorized", message: "Invalid or expired" }, status),
      );
      const resumed = resume();
      await pump();
      await resumed.elapse(1_000);

      expect(await resumed.outcome).toMatchObject({
        name: "ApiClientError",
        status,
      });
      await resumed.elapse(60_000);
      expect(server.times(REFRESH)).toBe(2);
    },
  );
});

describe("a resume whose session changes hands", () => {
  it("stops, and sends nothing more, when someone signs in during the wait", async () => {
    server.routes[REFRESH] = answers(down, session);
    const resumed = resume();
    await pump();
    expect(server.times(REFRESH)).toBe(1);

    storeTokens(authResponse(VIEWER)); // meanwhile
    await resumed.elapse(1_000);

    expect(await resumed.outcome).toBeInstanceOf(StaleSessionError);
    expect(server.times(REFRESH)).toBe(1);
  });

  it("stops when the session ended during the wait, too", async () => {
    server.routes[REFRESH] = answers(down, session);
    const resumed = resume();
    await pump();

    clearTokens();
    await resumed.elapse(1_000);

    expect(await resumed.outcome).toBeInstanceOf(StaleSessionError);
    expect(server.times(REFRESH)).toBe(1);
  });

  it("does not queue for the lock another tab holds, for an attempt that a sign-in during the wait made pointless", async () => {
    // The browser has locks, and another tab's refresh is out and hangs. An
    // attempt that went ahead would stand in the queue for it — up to 15 s — to
    // be told, once it had the lock, what the sign-in made known as the wait
    // ended; and the page is a spinner all that time.
    const lock = installFakeLocks();
    server.routes[REFRESH] = answers(down, session);
    const resumed = resume();
    await pump();
    expect(server.times(REFRESH)).toBe(1); // the first attempt, which failed
    void lock.request(
      "nexara:auth-refresh",
      {},
      () => deferred<undefined>().promise,
    );
    storeTokens(authResponse(VIEWER)); // someone signs in meanwhile

    await resumed.elapse(1_000); // and the wait is over

    expect(
      await Promise.race([resumed.outcome, go().then(() => "still waiting")]),
    ).toBeInstanceOf(StaleSessionError);
    expect(lock.waiting).toBe(0);
    expect(server.times(REFRESH)).toBe(1);
  });

  it("is told so, and not the failure, when the session changed hands during the last attempt", async () => {
    // The last attempt's failure is the answer, unless the page is no longer
    // the one it was asked for: initialize() treats a stale answer as another
    // session's, and the failure of the previous one's as its own.
    const last = deferred<Response>();
    server.routes[REFRESH] = answers(down, down, down, () => last.promise);
    const resumed = resume();
    await pump();
    await resumed.elapse(1_000);
    await resumed.elapse(2_000);
    await resumed.elapse(4_000);
    expect(server.times(REFRESH)).toBe(4); // the fourth, held

    storeTokens(authResponse(VIEWER));
    last.resolve(down());

    expect(await resumed.outcome).toBeInstanceOf(StaleSessionError);
  });
});

describe("a resume whose session changes hands during a failing attempt", () => {
  it("stops at once instead of sleeping, and sends nothing more", async () => {
    const attempt = deferred<Response>();
    server.routes[REFRESH] = () => attempt.promise;
    const outcome = settle(resumeSessionPatiently(currentSessionEpoch()));
    await pump();
    expect(server.times(REFRESH)).toBe(1); // the first attempt is out

    storeTokens(authResponse(VIEWER)); // someone signs in while it is
    attempt.resolve(down()); // and it fails
    await pump();

    // Settled without the clock moving: the wait that would have followed — 1
    // s and a second of jitter — was never started, for a next attempt that
    // would only have found the session is not its own.
    expect(
      await Promise.race([outcome, go().then(() => "still waiting")]),
    ).toBeInstanceOf(StaleSessionError);
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
    await resumed.elapse(1_000);

    expect(await resumed.outcome).toBe("resumed");
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

    // The first attempt failed and is waiting to ask again. The lock is free for
    // whoever else has a refresh to make.
    let other = "not granted";
    void lock.request("nexara:auth-refresh", {}, () => {
      other = "granted";
      return Promise.resolve();
    });
    await pump();
    expect(other).toBe("granted");

    await resumed.elapse(1_000);
    expect(await resumed.outcome).toBe("resumed");
    expect(lock.granted).toHaveLength(3); // the first attempt, the other tab's, the second
  });
});
