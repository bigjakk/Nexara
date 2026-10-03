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
  VIEWER,
  authResponse,
  callerOf,
  deferred,
  flush,
  installFakeServer,
  json,
  type FakeServer,
} from "@/test/fake-server";
import { installFakeLocks, removeFakeLocks } from "@/test/fake-lock-manager";
import { apiPath } from "./api-path";

/**
 * The lock the tabs of one browser take turns to refresh under (lib/api-client.ts,
 * withRefreshLock), which is an optimisation on HTTPS only: navigator.locks is
 * there in a secure context and nowhere else.
 *
 * jsdom has no navigator.locks, so these tests give it one — a lock manager as
 * a browser's, shared by every "tab" — and make a tab of a copy of the
 * api-client module (vi.resetModules), which is what a tab is: its own token,
 * epoch and refresh in flight, and a cookie, a localStorage and a lock manager
 * that all the others share.
 *
 * The server models what the lock is for. A refresh token is single-use: the
 * refresh that presents the cookie spends it, and the one that presents it
 * after is refused. The browser's jar takes the new cookie when the answer
 * lands, so a refresh SENT before then presents the spent one. Each test
 * answers the refreshes itself, in the order it chooses, which is how it tells
 * when a tab sent: by the cookie it carried.
 */

const REFRESH = "POST /api/v1/auth/refresh";
const X = "GET /api/v1/x";
const LOCK = "nexara:auth-refresh";

// --- tabs --------------------------------------------------------------------

type Client = typeof import("./api-client");

const EXPIRED = -10;
const LONG_EXPIRED = -600;

interface Tab {
  client: Client;
  onFailure: Mock<() => void>;
  onRefresh: Mock<(res: AuthResponse) => void>;
}

/**
 * A tab: its own copy of the module, signed in as the admin with a token that
 * has `expiresIn` seconds to live.
 *
 * The default, 30, is a token that is still good and about to expire: its
 * refresh is a courtesy, and does not wait for the lock (ifAvailable). A tab
 * that NEEDS its refresh — the laptop that woke with its tokens expired — has
 * a token past its expiry: `EXPIRED`, which is within the allowance a token may
 * still be sent past, so that a failure of its refresh does not hide it. And a
 * token expired by more than the allowance, `LONG_EXPIRED`, is never sent: its
 * request fails with the refresh's own failure.
 */
async function openTab(expiresIn = 30): Promise<Tab> {
  vi.resetModules();
  const client = await import("./api-client");
  const onFailure = vi.fn<() => void>();
  const onRefresh = vi.fn<(res: AuthResponse) => void>();
  client.setAuthFailureCallback(onFailure);
  client.setAuthRefreshCallback(onRefresh);
  client.storeTokens(authResponse(ADMIN, { expiresIn }));
  return { client, onFailure, onRefresh };
}

/** What a request settles as: the error it failed with, or "sent". */
function settle(request: Promise<unknown>) {
  return request.then(
    () => "sent",
    (err: unknown) => err,
  );
}

// --- the server and the jar ----------------------------------------------------

interface Held {
  presented: number;
  answer: ReturnType<typeof deferred<Response>>;
}

let server: FakeServer;
let clock = 0;

// Date.now judges a token's expiry, and is the real one unless a test moves it
// (clockOffsetMs) or pins it (pinnedNow).
const realNow = Date.now.bind(Date);
let clockOffsetMs = 0;
let pinnedNow: number | null = null;
// Runs, once, at the next time anything asks for the date: a hook into the
// middle of the client's own turn, for the test that has something else happen
// there (nothing but a clock read happens between two of its microtasks).
let onNextClockRead: (() => void) | null = null;

/**
 * The refresh the server and the jar make between them. Every refresh sent is
 * held in `held`, with the cookie it carried, until `deliver` answers it.
 */
function singleUseCookie() {
  let accepted = 1; // the cookie the server still accepts
  let jar = 1; // the cookie the browser sends
  const sent: number[] = [];
  const held: Held[] = [];
  server.routes[REFRESH] = () => {
    const entry: Held = { presented: jar, answer: deferred<Response>() };
    sent.push(entry.presented);
    held.push(entry);
    return entry.answer.promise;
  };
  return {
    /** The cookie each refresh carried, in the order they were sent. */
    sent,
    /** Answers the n-th refresh sent: a session if its cookie is still good, the spent one's 401 if not. */
    deliver(n: number) {
      const entry = held[n];
      if (entry === undefined) throw new Error(`no refresh #${String(n)} sent`);
      if (entry.presented === accepted) {
        accepted += 1;
        jar = accepted; // Set-Cookie lands with the response
        entry.answer.resolve(json(authResponse(ADMIN)));
      } else {
        entry.answer.resolve(
          json({ error: "unauthorized", message: "spent" }, 401),
        );
      }
    },
  };
}

beforeEach(() => {
  localStorage.clear();
  server = installFakeServer();
  server.routes[X] = (init) => json({ owner: callerOf(init) });
  clock = 0;
  clockOffsetMs = 0;
  pinnedNow = null;
  onNextClockRead = null;
  vi.spyOn(performance, "now").mockImplementation(() => clock);
  vi.spyOn(Math, "random").mockReturnValue(0);
  vi.spyOn(Date, "now").mockImplementation(() => {
    const hook = onNextClockRead;
    onNextClockRead = null;
    hook?.();
    return pinnedNow ?? realNow() + clockOffsetMs;
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  removeFakeLocks();
  localStorage.clear();
});

describe("two tabs that refresh at the same moment", () => {
  it("take turns: the second sends the cookie the first was given, only after the first has settled", async () => {
    const lock = installFakeLocks();
    const cookie = singleUseCookie();
    const tabA = await openTab(EXPIRED);
    const tabB = await openTab(EXPIRED);

    const a = settle(tabA.client.apiClient.get(apiPath`/api/v1/x`));
    const b = settle(tabB.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();

    // Only the first is out. The second is waiting for the lock, with its
    // refresh unsent — the cookie it would carry is the one the first spends.
    expect(cookie.sent).toEqual([1]);
    expect(lock.waiting).toBe(1);

    cookie.deliver(0);
    expect(await a).toBe("sent");
    await flush();

    // It carried the cookie the first was given, so it is good.
    expect(cookie.sent).toEqual([1, 2]);
    cookie.deliver(1);
    expect(await b).toBe("sent");

    expect(tabA.onFailure).not.toHaveBeenCalled();
    expect(tabB.onFailure).not.toHaveBeenCalled();
    expect(lock.granted).toEqual([LOCK, LOCK]);
  });

  it("control: without the API they race as they always did, and the one that lost is signed out", async () => {
    // jsdom has no navigator.locks, as a plain-HTTP page has none: nothing to wait for.
    expect("locks" in navigator).toBe(false);
    const cookie = singleUseCookie();
    const tabA = await openTab();
    const tabB = await openTab();

    const a = settle(tabA.client.apiClient.get(apiPath`/api/v1/x`));
    const b = settle(tabB.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();

    // Both are out at once, with the same cookie.
    expect(cookie.sent).toEqual([1, 1]);
    cookie.deliver(0);
    cookie.deliver(1);
    await Promise.all([a, b]);

    expect(tabA.onFailure).not.toHaveBeenCalled();
    expect(tabB.onFailure).toHaveBeenCalledTimes(1); // the loser
  });

  it("do not send a second refresh for a failure: the lock is released when the refresh fails, and the failure is the refresh's own", async () => {
    const lock = installFakeLocks();
    server.routes[REFRESH] = () => json({ error: "x", message: "down" }, 503);
    const tab = await openTab(LONG_EXPIRED);

    const outcome = await settle(tab.client.apiClient.get(apiPath`/api/v1/x`));

    // A refresh that ran under the lock and failed was not a lock that was
    // never got: it is not run again without it.
    expect(outcome).toMatchObject({
      name: "RefreshFailedError",
      status: 503,
    });
    expect(server.times(REFRESH)).toBe(1);
    expect(lock.granted).toEqual([LOCK]);
    expect(lock.waiting).toBe(0);
  });
});

describe("a tab that waits for the lock", () => {
  it("fails at once, without waiting, when its last refresh failed lately", async () => {
    const lock = installFakeLocks();
    // The server is down for B's refresh; then A's refresh goes out and is
    // never answered, so A holds the lock.
    let serverIsDown = true;
    server.routes[REFRESH] = () =>
      serverIsDown
        ? json({ error: "x", message: "down" }, 503)
        : new Promise<Response>(() => undefined);
    const tabA = await openTab();
    const tabB = await openTab(LONG_EXPIRED);
    const failure = await settle(tabB.client.apiClient.get(apiPath`/api/v1/x`));
    expect(failure).toMatchObject({ name: "RefreshFailedError", status: 503 });
    serverIsDown = false;
    void settle(tabA.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();
    expect(server.times(REFRESH)).toBe(2); // B's, and A's, which is out

    // B asks again, inside its back-off. The lock is not free, and it is not
    // waited for: the answer is the failure it already has.
    const outcome = await Promise.race([
      settle(tabB.client.apiClient.get(apiPath`/api/v1/x`)),
      flush().then(() => "still waiting"),
    ]);

    expect(outcome).not.toBe("still waiting");
    expect(outcome).toBe(failure);
    expect(lock.waiting).toBe(0);
    expect(server.times(REFRESH)).toBe(2);
  });

  it("sends nothing when the session ended while it waited", async () => {
    const lock = installFakeLocks();
    const cookie = singleUseCookie();
    const tabA = await openTab();
    const tabB = await openTab(EXPIRED);
    void settle(tabA.client.apiClient.get(apiPath`/api/v1/x`));
    const b = settle(tabB.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();
    expect(cookie.sent).toEqual([1]);
    expect(lock.waiting).toBe(1);

    // B is signed out and the viewer signs in, while B waits ...
    tabB.client.clearTokens();
    tabB.client.storeTokens(authResponse(VIEWER));
    // ... and A's refresh lands, handing B the lock.
    cookie.deliver(0);

    expect(await b).toBeInstanceOf(tabB.client.StaleSessionError);
    expect(cookie.sent).toEqual([1]); // B's was never sent
    expect(tabB.onFailure).not.toHaveBeenCalled();
    expect(lock.waiting).toBe(0);
  });

  describe("for longer than it is willing to", () => {
    // Only the timer is faked, so that the wait can be run out — and only once
    // the tabs are open: loading a copy of the module is not for fake timers.
    function fakeTheTimer() {
      vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    }

    async function go(ms: number) {
      await vi.advanceTimersByTimeAsync(ms);
    }

    it("goes ahead without the lock after 15 s, as it would have without the API", async () => {
      const lock = installFakeLocks();
      const cookie = singleUseCookie();
      const tabA = await openTab();
      const tabB = await openTab(EXPIRED);
      fakeTheTimer();
      // A holds the lock through a refresh that never answers.
      void settle(tabA.client.apiClient.get(apiPath`/api/v1/x`));
      const b = settle(tabB.client.apiClient.get(apiPath`/api/v1/x`));
      await go(0);
      expect(cookie.sent).toEqual([1]);

      await go(14_999);
      expect(cookie.sent).toEqual([1]); // still waiting
      expect(lock.waiting).toBe(1);

      await go(1);
      // The wait is over: B sends its own, without the lock, and gets on.
      expect(cookie.sent).toEqual([1, 1]);
      expect(lock.waiting).toBe(0);
      cookie.deliver(1);
      expect(await b).toBe("sent");
      expect(tabB.onFailure).not.toHaveBeenCalled();
    });

    it("sends nothing when the session ended while it waited, whichever way the wait ended", async () => {
      const lock = installFakeLocks();
      const cookie = singleUseCookie();
      const tabA = await openTab();
      const tabB = await openTab(EXPIRED);
      fakeTheTimer();
      void settle(tabA.client.apiClient.get(apiPath`/api/v1/x`));
      const b = settle(tabB.client.apiClient.get(apiPath`/api/v1/x`));
      await go(0);
      expect(lock.waiting).toBe(1);

      tabB.client.clearTokens();
      tabB.client.storeTokens(authResponse(VIEWER));
      await go(15_000); // the wait runs out with the lock still A's

      expect(await b).toBeInstanceOf(tabB.client.StaleSessionError);
      expect(cookie.sent).toEqual([1]);
      expect(tabB.onFailure).not.toHaveBeenCalled();
    });

    it("holds the lock through a superseded refresh's second ask, so that no other tab sends in between", async () => {
      const lock = installFakeLocks();
      const sent: string[] = [];
      let answers = 0;
      server.routes[REFRESH] = () => {
        sent.push(`refresh #${String(++answers)}`);
        return answers === 1
          ? json(
              { error: "refresh_superseded", message: "a newer refresh won" },
              409,
            )
          : json(authResponse(ADMIN));
      };
      const tabA = await openTab();
      const tabB = await openTab(EXPIRED);
      fakeTheTimer();

      const a = settle(tabA.client.apiClient.get(apiPath`/api/v1/x`));
      const b = settle(tabB.client.apiClient.get(apiPath`/api/v1/x`));
      await go(0);
      // A's first refresh was superseded, and A is waiting out the 250 ms before
      // it asks again. B is not sent in that gap.
      expect(sent).toEqual(["refresh #1"]);
      expect(lock.waiting).toBe(1);

      await go(250);
      expect(await a).toBe("sent");
      await go(0);
      expect(await b).toBe("sent");

      // A's two, then B's: never B's between them.
      expect(sent).toEqual(["refresh #1", "refresh #2", "refresh #3"]);
      expect(lock.granted).toEqual([LOCK, LOCK]);
    });
  });
});

describe("a refresh made ahead of a token that is still good", () => {
  // A token with seconds to live needs no refresh to be sent; the refresh is a
  // courtesy to the request. So it does not wait for the lock — a tab whose
  // refresh hangs while it holds it would stall every request of every other
  // tab, Sign out included, for the whole wait — and asks for it with
  // ifAvailable: free, it refreshes as ever; taken, it is skipped, and the token
  // held goes out. One that is NEEDED — no token, or one past its expiry — still
  // waits.
  const LOGOUT = "POST /api/v1/auth/logout";

  /** Another tab's refresh, out and not answered: it holds the lock until the test answers it. */
  async function anotherTabIsRefreshing() {
    const lock = installFakeLocks();
    const cookie = singleUseCookie();
    const holder = await openTab();
    void settle(holder.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();
    expect(cookie.sent).toEqual([1]);
    return { lock, cookie, holder };
  }

  it("is skipped while another tab holds the lock: the request goes out at once, with the token held, and nothing waits", async () => {
    const { lock, cookie } = await anotherTabIsRefreshing();
    const tab = await openTab();

    const outcome = await Promise.race([
      tab.client.apiClient.get(apiPath`/api/v1/x`),
      flush().then(() => "stalled"),
    ]);

    // Sent, and as the admin: with the token it held. No refresh of its own, and
    // no request for the lock left queued behind the other tab's.
    expect(outcome).toEqual({ owner: ADMIN.id });
    expect(cookie.sent).toEqual([1]);
    expect(lock.skipped).toBe(1);
    expect(lock.waiting).toBe(0);
    expect(tab.onRefresh).not.toHaveBeenCalled();
    expect(tab.onFailure).not.toHaveBeenCalled();
  });

  it("is not held up by another tab for Sign out either", async () => {
    const { lock, cookie } = await anotherTabIsRefreshing();
    const tab = await openTab();
    server.routes[LOGOUT] = () => new Response(null, { status: 204 });

    const outcome = await Promise.race([
      settle(tab.client.apiClient.post(apiPath`/api/v1/auth/logout`, {})),
      flush().then(() => "stalled"),
    ]);

    expect(outcome).toBe("sent");
    expect(server.times(LOGOUT)).toBe(1);
    expect(cookie.sent).toEqual([1]);
    expect(lock.waiting).toBe(0);
  });

  it("is made on a later request, once the other tab is done", async () => {
    const { lock, cookie } = await anotherTabIsRefreshing();
    const tab = await openTab();
    await tab.client.apiClient.get(apiPath`/api/v1/x`); // skipped, and sent
    expect(cookie.sent).toEqual([1]);

    cookie.deliver(0); // the other tab's refresh lands: the lock is free
    await flush();
    const later = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();

    // Free, it refreshes as ever, carrying the cookie the other tab was given.
    expect(cookie.sent).toEqual([1, 2]);
    cookie.deliver(1);
    expect(await later).toBe("sent");
    expect(tab.onRefresh).toHaveBeenCalledTimes(1);
    expect(lock.skipped).toBe(1);
  });

  it("is made as ever when the lock is free", async () => {
    const lock = installFakeLocks();
    const cookie = singleUseCookie();
    const tab = await openTab();

    const request = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();
    expect(cookie.sent).toEqual([1]);
    cookie.deliver(0);

    expect(await request).toBe("sent");
    expect(tab.onRefresh).toHaveBeenCalledTimes(1);
    expect(lock.granted).toEqual([LOCK]);
    expect(lock.skipped).toBe(0);
  });

  it("is shared by the requests made while it is out: they are held for it, and one refresh is sent for them all", async () => {
    const lock = installFakeLocks();
    const cookie = singleUseCookie();
    const tab = await openTab();

    const first = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    const second = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();

    expect(cookie.sent).toEqual([1]);
    expect(server.times(X)).toBe(0); // both are held for it
    expect(lock.skipped).toBe(0);

    cookie.deliver(0);
    expect(await first).toBe("sent");
    expect(await second).toBe("sent");
    expect(lock.granted).toEqual([LOCK]);
    expect(tab.onRefresh).toHaveBeenCalledTimes(1);
  });

  it("still waits, for a token that is past its expiry: that refresh is needed", async () => {
    const { lock, cookie } = await anotherTabIsRefreshing();
    const tab = await openTab(EXPIRED);

    const request = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();

    expect(lock.waiting).toBe(1);
    expect(lock.skipped).toBe(0);
    expect(server.times(X)).toBe(0); // the request is held for it

    cookie.deliver(0);
    await flush();
    expect(cookie.sent).toEqual([1, 2]);
    cookie.deliver(1);
    expect(await request).toBe("sent");
  });

  it("still waits, when no token is held: that refresh is needed too", async () => {
    const { lock, cookie } = await anotherTabIsRefreshing();
    // A page that has just loaded: nobody signed in, and nothing ended.
    vi.resetModules();
    const fresh = await import("./api-client");

    const request = settle(fresh.apiClient.get(apiPath`/api/v1/x`));
    await flush();

    expect(lock.waiting).toBe(1);
    expect(lock.skipped).toBe(0);
    expect(server.times(X)).toBe(0);

    cookie.deliver(0);
    await flush();
    expect(cookie.sent).toEqual([1, 2]);
    cookie.deliver(1);
    expect(await request).toBe("sent");
  });

  it("is a courtesy only while the token has life left, by this clock, to the second", async () => {
    await anotherTabIsRefreshing();
    pinnedNow = 1_800_000_000_000;
    const expiresNextSecond = await openTab(1);
    const expiresNow = await openTab(0);

    // One second to live: still good, and so not waited for.
    expect(
      await Promise.race([
        expiresNextSecond.client.apiClient.get(apiPath`/api/v1/x`),
        flush().then(() => "stalled"),
      ]),
    ).toEqual({ owner: ADMIN.id });

    // None left: past its expiry, and so waited for.
    const waiting = Promise.race([
      expiresNow.client.apiClient.get(apiPath`/api/v1/x`),
      flush().then(() => "stalled"),
    ]);
    expect(await waiting).toBe("stalled");
  });

  describe("when others in the tab were waiting on it", () => {
    // The refresh in flight is shared by every request of the tab. A courtesy
    // refresh that is skipped tells all that joined it; the ones that merely
    // wanted it to be made carry on with the token they hold, and one that needs
    // a refresh makes its own, which waits for the lock. The lock manager's
    // answer is held back, as a browser's is not instant, so that the second
    // request can join before it.
    it("makes its own, for a request that needed one", async () => {
      const { lock, cookie } = await anotherTabIsRefreshing();
      const tab = await openTab();
      lock.pauseDecisions();

      const courtesy = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();
      clockOffsetMs += 100_000; // the token now looks expired by 70 s
      const needed = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();
      lock.resumeDecisions(); // the lock is taken: the courtesy refresh is skipped
      await flush();

      expect(await courtesy).toBe("sent"); // went with the token it held
      expect(lock.skipped).toBe(1);
      expect(cookie.sent).toEqual([1]);
      // The other did not take the skip for an answer: it asks for the lock,
      // and waits like any refresh that is needed.
      expect(lock.waiting).toBe(1);

      cookie.deliver(0);
      await flush();
      expect(cookie.sent).toEqual([1, 2]);
      cookie.deliver(1);
      expect(await needed).toBe("sent");
      expect(tab.onRefresh).toHaveBeenCalledTimes(1);
    });

    it("makes one between them, for requests that needed one together", async () => {
      const { lock, cookie } = await anotherTabIsRefreshing();
      const tab = await openTab();
      lock.pauseDecisions();
      const courtesy = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();
      clockOffsetMs += 100_000; // the token now looks expired by 70 s
      const neededFirst = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      const neededSecond = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();
      lock.resumeDecisions(); // the courtesy refresh is skipped, for all three
      await flush();

      expect(await courtesy).toBe("sent");
      // The first of the others made a refresh of its own, and the second took
      // that one: one request for the lock, not two.
      expect(lock.waiting).toBe(1);

      cookie.deliver(0);
      await flush();
      expect(cookie.sent).toEqual([1, 2]);
      cookie.deliver(1);
      expect(await neededFirst).toBe("sent");
      expect(await neededSecond).toBe("sent");
      expect(tab.onRefresh).toHaveBeenCalledTimes(1);
    });

    it("sends no refresh for a session that began meanwhile: a request that needed one is told it is stale", async () => {
      const { lock, cookie } = await anotherTabIsRefreshing();
      const tab = await openTab();
      lock.pauseDecisions();
      const courtesy = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();
      clockOffsetMs += 100_000; // the token now looks expired by 70 s
      const needed = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();

      tab.client.storeTokens(authResponse(VIEWER)); // the session changes hands
      lock.resumeDecisions(); // and only now is the courtesy refresh skipped
      await flush();

      // The refresh the viewer's session needs is not this request's to make:
      // none is asked of the lock, and none is sent.
      expect(lock.waiting).toBe(0);
      expect(cookie.sent).toEqual([1]);
      expect(
        await Promise.race([needed, flush().then(() => "still waiting")]),
      ).toBeInstanceOf(tab.client.StaleSessionError);
      await courtesy;
    });

    it("makes its own, for a request that was refused for its token", async () => {
      const { lock, cookie } = await anotherTabIsRefreshing();
      const tab = await openTab(3_600);
      // The first request this tab sends is held, and refused when the test says.
      const refused = deferred<Response>();
      let first = true;
      server.routes[X] = (init) => {
        if (first) {
          first = false;
          return refused.promise;
        }
        return json({ owner: callerOf(init) });
      };
      const inFlight = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();
      expect(server.times(X)).toBe(1);

      clockOffsetMs += 3_550_000; // 50 s to live: the next request refreshes ahead
      lock.pauseDecisions();
      const courtesy = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();
      refused.resolve(json({ error: "unauthorized", message: "expired" }, 401));
      await flush(); // its 401 joins the courtesy refresh, which is waiting to hear
      lock.resumeDecisions();
      await flush();

      expect(await courtesy).toBe("sent");
      expect(lock.skipped).toBe(1);
      expect(lock.waiting).toBe(1); // the refused request asked for its own

      cookie.deliver(0);
      await flush();
      expect(cookie.sent).toEqual([1, 2]);
      cookie.deliver(1);
      expect(await inFlight).toBe("sent"); // replayed under the new token
      expect(tab.onRefresh).toHaveBeenCalledTimes(1);
    });

    it("makes its own though another request begins a courtesy refresh between the skip and its hearing of it", async () => {
      // The skip reaches the callers that joined the refresh one after another,
      // each in a turn of its own, and a request that runs in one of those turns
      // can begin a refresh ahead of its own token. It is the courtesy request's
      // own turn that is used here, which reads the clock to see whether its
      // token has expired for real. What the request that needs a refresh finds
      // in flight when its turn comes is that refresh, not a free field — and it
      // must not take it for its own: it would be skipped too.
      const { lock, cookie } = await anotherTabIsRefreshing();
      const tab = await openTab(3_600);
      const refused = deferred<Response>();
      let first = true;
      server.routes[X] = (init) => {
        if (first) {
          first = false;
          return refused.promise;
        }
        return json({ owner: callerOf(init) });
      };
      const inFlight = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();
      expect(server.times(X)).toBe(1);

      clockOffsetMs += 3_550_000; // 50 s to live: the next request refreshes ahead
      lock.pauseDecisions();
      const courtesy = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();
      refused.resolve(json({ error: "unauthorized", message: "expired" }, 401));
      await flush(); // its 401 joins the courtesy refresh, which is waiting to hear

      let another: Promise<unknown> | undefined;
      onNextClockRead = () => {
        // The first look at the clock after the skip is the courtesy request's.
        another = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      };
      lock.resumeDecisions();
      await flush();

      expect(another).toBeDefined();
      expect(lock.skipped).toBe(2); // the courtesy refresh, and the one begun in the gap
      expect(lock.waiting).toBe(1); // and the refused request asked for its own
      expect(await courtesy).toBe("sent");
      expect(await another).toBe("sent");

      cookie.deliver(0);
      await flush();
      expect(cookie.sent).toEqual([1, 2]);
      cookie.deliver(1);
      expect(await inFlight).toBe("sent"); // replayed under the new token
      expect(tab.onRefresh).toHaveBeenCalledTimes(1);
    });
  });

  describe("when what is in flight is a refresh that is needed", () => {
    // A request that was refused for its token needs a refresh, and waits for
    // the lock for it — up to 15 s behind another tab whose refresh hangs. A
    // request made meanwhile whose token has not expired has no need of it, and
    // does not wait for it: it goes out with the token it holds, as it would if
    // the lock were taken.
    async function aRefusedRequestIsWaitingForTheLock() {
      const holder = await anotherTabIsRefreshing();
      const tab = await openTab(3_600);
      const refused = deferred<Response>();
      let first = true;
      server.routes[X] = (init) => {
        if (first) {
          first = false;
          return refused.promise;
        }
        return json({ owner: callerOf(init) });
      };
      const refusedRequest = settle(
        tab.client.apiClient.get(apiPath`/api/v1/x`),
      );
      await flush();
      expect(server.times(X)).toBe(1);

      clockOffsetMs += 3_550_000; // 50 s to live: a refresh made ahead of it is a courtesy
      refused.resolve(json({ error: "unauthorized", message: "expired" }, 401));
      await flush();
      expect(holder.lock.waiting).toBe(1); // needed, and waiting behind the other tab's
      return { ...holder, tab, refusedRequest };
    }

    it("is not joined by a request whose token is still good, which goes out at once with the token held", async () => {
      const { lock, cookie, tab, refusedRequest } =
        await aRefusedRequestIsWaitingForTheLock();

      const outcome = await Promise.race([
        settle(tab.client.apiClient.get(apiPath`/api/v1/x`)),
        flush().then(() => "stalled"),
      ]);

      expect(outcome).toBe("sent"); // not held up behind the wait
      expect(lock.waiting).toBe(1); // the one refresh that is waiting, and no other
      expect(lock.skipped).toBe(0); // the lock was not asked, so it did not skip anything
      expect(cookie.sent).toEqual([1]);

      // The refresh that was needed is still made, when the lock is free, and the
      // refused request is replayed under what it brings.
      cookie.deliver(0);
      await flush();
      expect(cookie.sent).toEqual([1, 2]);
      cookie.deliver(1);
      expect(await refusedRequest).toBe("sent");
      expect(tab.onRefresh).toHaveBeenCalledTimes(1);
    });

    it("control: a request that needs a refresh joins it, and is sent the one it brings", async () => {
      const { lock, cookie, tab, refusedRequest } =
        await aRefusedRequestIsWaitingForTheLock();
      clockOffsetMs += 100_000; // the token has expired by 70 s: a refresh is needed now
      const needed = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
      await flush();

      expect(lock.waiting).toBe(1); // it joined: no second request for the lock
      expect(server.times(X)).toBe(1); // and is held for it

      cookie.deliver(0);
      await flush();
      cookie.deliver(1);
      expect(await needed).toBe("sent");
      expect(await refusedRequest).toBe("sent");
      expect(cookie.sent).toEqual([1, 2]); // one refresh for the two
      expect(tab.onRefresh).toHaveBeenCalledTimes(1);
    });
  });
});

describe("a courtesy refresh whose skip is a long time coming", () => {
  // The lock manager answers a request after a round trip, and a tab that is
  // frozen meanwhile can be thawed minutes later, the answer arriving then.
  async function theAnswerIsHeld() {
    const holder = await anotherTabIsHoldingTheLock();
    const tab = await openTab();
    holder.lock.pauseDecisions();
    const request = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();
    return { ...holder, tab, request };
  }

  async function anotherTabIsHoldingTheLock() {
    const lock = installFakeLocks();
    const cookie = singleUseCookie();
    const holder = await openTab();
    void settle(holder.client.apiClient.get(apiPath`/api/v1/x`));
    await flush();
    expect(cookie.sent).toEqual([1]);
    return { lock, cookie };
  }

  it("sends the token held when the skip was only a little late: it is still good enough to be sent", async () => {
    const { lock, request } = await theAnswerIsHeld();
    clockOffsetMs += 100_000; // expired by 70 s: inside the allowance

    lock.resumeDecisions();

    expect(await request).toBe("sent");
    expect(lock.waiting).toBe(0);
  });

  it("does not send a token that has expired for real in the meantime: the refresh is needed now, and waits for the lock", async () => {
    const { lock, cookie, request } = await theAnswerIsHeld();
    clockOffsetMs += 400_000; // expired by 370 s: past the allowance

    lock.resumeDecisions();
    await flush();

    expect(server.times(X)).toBe(0); // held for the refresh, not sent with it
    expect(lock.waiting).toBe(1);
    cookie.deliver(0); // the other tab's refresh lands
    await flush();
    expect(cookie.sent).toEqual([1, 2]);
    cookie.deliver(1);
    expect(await request).toBe("sent");
  });
});

describe("the timer that would give up the wait for the lock", () => {
  // A wait is a 15 s timer, pending while a tab waits for the lock and not a
  // moment longer: not once the lock is granted, while the refresh it was
  // waiting for runs, and not after, with the lock or without the API, nor when
  // the browser would not hand the lock over. A courtesy refresh waits for
  // nothing and sets none. What counts is what is pending WHILE a refresh is in
  // flight: one cleared when it settles cannot be told from one never set.
  const down = () => json({ error: "x", message: "down" }, 503);

  async function go(ms = 0) {
    await vi.advanceTimersByTimeAsync(ms);
  }
  async function settleAll() {
    for (let i = 0; i < 5; i++) await go();
  }

  it("is pending while a tab waits, and is gone once it holds the lock, with its refresh still out", async () => {
    installFakeLocks();
    const cookie = singleUseCookie();
    const holder = await openTab(); // a courtesy refresh: waits for nothing
    const waiter = await openTab(EXPIRED); // needs its refresh: waits
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    void settle(holder.client.apiClient.get(apiPath`/api/v1/x`));
    await settleAll();
    expect(cookie.sent).toEqual([1]);
    expect(vi.getTimerCount()).toBe(0); // the holder set none

    const request = settle(waiter.client.apiClient.get(apiPath`/api/v1/x`));
    await settleAll();
    expect(vi.getTimerCount()).toBe(1); // the waiter's

    cookie.deliver(0); // the holder's refresh lands, and the waiter has the lock
    await settleAll();
    expect(cookie.sent).toEqual([1, 2]); // its own refresh is out
    expect(vi.getTimerCount()).toBe(0);

    cookie.deliver(1);
    await settleAll();
    expect(await request).toBe("sent");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("is never set by a courtesy refresh", async () => {
    installFakeLocks();
    const cookie = singleUseCookie();
    const tab = await openTab();
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });

    const request = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await settleAll();
    expect(cookie.sent).toEqual([1]); // the refresh is out
    expect(vi.getTimerCount()).toBe(0);

    cookie.deliver(0);
    await settleAll();
    expect(await request).toBe("sent");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("is not set by a courtesy refresh that is still waiting to hear from the lock manager either", async () => {
    const lock = installFakeLocks();
    const cookie = singleUseCookie();
    const tab = await openTab();
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    lock.pauseDecisions();

    const request = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await settleAll();
    // Nothing has been asked of the lock yet, as far as the refresh knows, and
    // there is no wait it could be giving up.
    expect(cookie.sent).toEqual([]);
    expect(vi.getTimerCount()).toBe(0);

    lock.resumeDecisions();
    await settleAll();
    cookie.deliver(0);
    await settleAll();
    expect(await request).toBe("sent");
  });

  it("is gone while the refresh runs without the API, and after", async () => {
    server.routes[REFRESH] = () => new Promise<Response>(() => undefined);
    const tab = await openTab(EXPIRED);
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });

    void settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await settleAll();

    // The refresh is out and unanswered, and nothing ticks behind it.
    expect(server.times(REFRESH)).toBe(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("is gone while the refresh runs when the browser would not hand over the lock", async () => {
    Object.defineProperty(navigator, "locks", {
      configurable: true,
      value: {
        request: () => Promise.reject(new DOMException("no", "SecurityError")),
      },
    });
    server.routes[REFRESH] = () => new Promise<Response>(() => undefined);
    const tab = await openTab(EXPIRED);
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });

    void settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await settleAll();

    expect(server.times(REFRESH)).toBe(1); // gone ahead without it
    expect(vi.getTimerCount()).toBe(0);
  });

  it("is gone after a refresh that failed under the lock", async () => {
    installFakeLocks();
    server.routes[REFRESH] = down;
    const tab = await openTab(LONG_EXPIRED);
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });

    const request = settle(tab.client.apiClient.get(apiPath`/api/v1/x`));
    await settleAll();

    expect(await request).toMatchObject({ name: "RefreshFailedError" });
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe("when the browser will not give the lock", () => {
  it("goes ahead without it: a lock manager that refuses is no reason to fail a refresh", async () => {
    Object.defineProperty(navigator, "locks", {
      configurable: true,
      value: {
        request: () => Promise.reject(new DOMException("no", "SecurityError")),
      },
    });
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    const tab = await openTab();

    expect(await settle(tab.client.apiClient.get(apiPath`/api/v1/x`))).toBe(
      "sent",
    );
    expect(server.times(REFRESH)).toBe(1);
  });
});
