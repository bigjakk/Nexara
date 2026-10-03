import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { useState } from "react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { ProtectedRoute } from "@/components/auth/ProtectedRoute";
import {
  ADMIN,
  VIEWER,
  authResponse,
  deferred,
  flush,
  installFakeServer,
  json,
  type FakeServer,
  type Route,
} from "@/test/fake-server";
import { installFakeLocks, removeFakeLocks } from "@/test/fake-lock-manager";
import {
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import { clearTokens, currentSessionEpoch } from "@/lib/api-client";
import { useAuthStore } from "./auth-store";

/**
 * The boot resume — initialize() with a stored user, which refreshes off the
 * cookie to find out whether the session is still there — sends its refresh the
 * way every refresh is sent (resumeSession, lib/api-client.ts): under the lock
 * the tabs of one browser take turns on, where there is one, and asked once more
 * when the server says another tab's refresh won the race for the cookie.
 * Several tabs restored at browser start resume on one cookie at one instant;
 * on a plain-HTTP origin there is no lock, and the second ask is what keeps
 * them from signing each other out.
 *
 * And a resume that could not look — it lost that race twice, or met a 429, a
 * 5xx, no network, an answer that is no session — is asked again, a few times,
 * while the spinner stays up (isInitialized is false), before the session is
 * ended: only a cookie the server REFUSED (401, 403) ends it at once. A session
 * ended at boot takes nexara_user, which every tab shares, out of localStorage,
 * and the next reload of every other tab comes up at the login page
 * (lib/api-client.ts, resumeSessionPatiently, has the schedule and its tests).
 * What a resume that is refused, or that fails every time, does to the store is
 * what it always did (auth-store.session.test.tsx).
 */

const REFRESH = "POST /api/v1/auth/refresh";
const LOGIN = "POST /api/v1/auth/login";
const LOCK = "nexara:auth-refresh";

let server: FakeServer;

const superseded = () =>
  json(
    {
      error: "refresh_superseded",
      message: "the refresh token was superseded by a newer one",
    },
    409,
  );

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

beforeEach(() => {
  localStorage.clear();
  clearTokens();
  emptyPerSessionStores();
  signedOutState();
  server = installFakeServer();
  // A stored user: this page is resuming a session.
  localStorage.setItem("nexara_user", JSON.stringify(ADMIN));
  vi.spyOn(Math, "random").mockReturnValue(0);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  removeFakeLocks();
  clearTokens();
  localStorage.clear();
  emptyPerSessionStores();
  signedOutState();
});

describe("a resume at boot that the server says another tab won", () => {
  beforeEach(() => {
    // Only the timer, which the second ask waits on.
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  });

  async function go(ms: number) {
    await vi.advanceTimersByTimeAsync(ms);
  }

  it("asks once more, and signs the user in", async () => {
    server.routes[REFRESH] = answers(superseded, () =>
      json(authResponse(ADMIN, { permissions: ["view:cluster"] })),
    );

    const booting = useAuthStore.getState().initialize();
    await go(0);
    expect(server.times(REFRESH)).toBe(1);
    await go(249);
    expect(server.times(REFRESH)).toBe(1); // the wait is not over
    await go(1);
    await booting;

    const state = useAuthStore.getState();
    expect(server.times(REFRESH)).toBe(2);
    expect(state.isAuthenticated).toBe(true);
    expect(state.user?.id).toBe(ADMIN.id);
    expect(state.permissions).toEqual(["view:cluster"]);
    expect(state.isInitialized).toBe(true);
    // And the stored user, which a losing tab's clearTokens would have taken
    // from every tab, is still there.
    expect(localStorage.getItem("nexara_user")).not.toBeNull();
  });
});

describe("a resume at boot that could not look", () => {
  // The spinner stays up through the retries: isInitialized is false until the
  // resume has an answer it will act on. Math.random is pinned at 0, so the
  // waits are 1 s, 2 s and 4 s exactly; the second ask of a superseded refresh
  // (250 ms) is inside an attempt.
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  });

  async function go(ms = 0) {
    await vi.advanceTimersByTimeAsync(ms);
  }

  const down = () => json({ error: "x", message: "down" }, 503);
  const good = () =>
    json(authResponse(ADMIN, { permissions: ["view:cluster"] }));
  const refused = (status: number): Route => {
    return () =>
      json({ error: "unauthorized", message: "Invalid or expired" }, status);
  };

  /** What the page rehydrated for the stored user: console tabs, dismissed issues, ... */
  function somethingWasPersisted() {
    for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
  }
  function nothingWasWiped() {
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(true);
    }
    // The stored user, which every tab of the browser shares.
    expect(localStorage.getItem("nexara_user")).not.toBeNull();
  }
  function everythingWasWiped() {
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(false);
    }
    expect(localStorage.getItem("nexara_user")).toBeNull();
  }

  it("a refresh that lost its race twice, then the next attempt: signed in, and nothing wiped", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = answers(superseded, superseded, good);

    const booting = useAuthStore.getState().initialize();
    await go(0);
    await go(250); // the attempt's own second ask, lost as well
    expect(server.times(REFRESH)).toBe(2);
    // The attempt failed, and the page is still waiting.
    expect(useAuthStore.getState().isInitialized).toBe(false);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(localStorage.getItem("nexara_user")).not.toBeNull();

    await go(999);
    expect(server.times(REFRESH)).toBe(2); // the wait is not over
    await go(1);
    await booting;

    const state = useAuthStore.getState();
    expect(server.times(REFRESH)).toBe(3);
    expect(state.isAuthenticated).toBe(true);
    expect(state.user?.id).toBe(ADMIN.id);
    expect(state.permissions).toEqual(["view:cluster"]);
    expect(state.isInitialized).toBe(true);
    nothingWasWiped();
  });

  it("a 503, then the session: signed in, and the spinner was up while it waited", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = answers(down, good);

    const booting = useAuthStore.getState().initialize();
    await go(0);
    expect(server.times(REFRESH)).toBe(1);
    expect(useAuthStore.getState().isInitialized).toBe(false);
    expect(useAuthStore.getState().isLoading).toBe(true);

    await go(1_000);
    await booting;

    expect(server.times(REFRESH)).toBe(2);
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(useAuthStore.getState().isInitialized).toBe(true);
    nothingWasWiped();
  });

  it.each([
    ["a 429", () => json({ error: "x", message: "slow down" }, 429)],
    ["the network failing", () => Promise.reject(new TypeError("Failed"))],
    ["an answer that is no session", () => json({ user: { id: "x" } })],
  ] satisfies [string, Route][])(
    "%s, then the session: signed in, and nothing wiped",
    async (_name, failure) => {
      somethingWasPersisted();
      server.routes[REFRESH] = answers(failure, good);

      const booting = useAuthStore.getState().initialize();
      await go(0);
      await go(1_000);
      await booting;

      expect(server.times(REFRESH)).toBe(2);
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      nothingWasWiped();
    },
  );

  it.each([401, 403])(
    "a %i ends the session at once: no further attempt, and it ends once",
    async (status) => {
      somethingWasPersisted();
      server.routes[REFRESH] = answers(refused(status));
      const before = currentSessionEpoch();

      const booting = useAuthStore.getState().initialize();
      await go(0);
      await booting;

      const state = useAuthStore.getState();
      expect(state.isAuthenticated).toBe(false);
      expect(state.isInitialized).toBe(true);
      everythingWasWiped();
      expect(currentSessionEpoch()).toBe(before + 1); // ended once
      await go(60_000);
      expect(server.times(REFRESH)).toBe(1);
    },
  );

  it.each([401, 403])(
    "a %i at a later attempt ends it there: no attempt after it",
    async (status) => {
      somethingWasPersisted();
      server.routes[REFRESH] = answers(down, refused(status));
      const before = currentSessionEpoch();

      const booting = useAuthStore.getState().initialize();
      await go(0);
      expect(useAuthStore.getState().isInitialized).toBe(false);
      await go(1_000);
      await booting;

      expect(useAuthStore.getState().isAuthenticated).toBe(false);
      expect(useAuthStore.getState().isInitialized).toBe(true);
      everythingWasWiped();
      expect(currentSessionEpoch()).toBe(before + 1);
      await go(60_000);
      expect(server.times(REFRESH)).toBe(2);
    },
  );

  it("fails every attempt: four, 1 s, 2 s and 4 s apart, and then the session ends — once", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = answers(down);
    const before = currentSessionEpoch();

    const booting = useAuthStore.getState().initialize();
    await go(0);
    expect(server.times(REFRESH)).toBe(1);
    await go(1_000);
    expect(server.times(REFRESH)).toBe(2);
    await go(2_000);
    expect(server.times(REFRESH)).toBe(3);
    // Waiting for the fourth, with the page still on its spinner and nothing
    // ended yet.
    expect(useAuthStore.getState().isInitialized).toBe(false);
    nothingWasWiped();
    await go(3_999);
    expect(server.times(REFRESH)).toBe(3);
    await go(1);
    await booting;

    // Today's end of a session, after the last attempt.
    const state = useAuthStore.getState();
    expect(server.times(REFRESH)).toBe(4);
    expect(state.isAuthenticated).toBe(false);
    expect(state.user).toBeNull();
    expect(state.isInitialized).toBe(true);
    expect(state.isLoading).toBe(false);
    everythingWasWiped();
    expect(currentSessionEpoch()).toBe(before + 1);
    await go(60_000);
    expect(server.times(REFRESH)).toBe(4);
  });

  it("a 409 that is not a superseded refresh is not asked a second time within an attempt, only by the schedule", async () => {
    server.routes[REFRESH] = () =>
      json({ error: "conflict", message: "state has changed" }, 409);

    const booting = useAuthStore.getState().initialize();
    await go(0);
    await go(500); // longer than the 250–500 ms of a second ask: none is made
    expect(server.times(REFRESH)).toBe(1);
    await go(500);
    expect(server.times(REFRESH)).toBe(2); // the schedule's, at 1 s
    await go(2_000);
    await go(4_000);
    await booting;

    expect(server.times(REFRESH)).toBe(4);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });

  it("stops, and applies nothing over them, when someone signs in during the wait", async () => {
    server.routes[REFRESH] = answers(down, good);
    server.routes[LOGIN] = () =>
      json(authResponse(VIEWER, { permissions: ["view:cluster"] }));

    const booting = useAuthStore.getState().initialize();
    await go(0);
    expect(server.times(REFRESH)).toBe(1);
    await useAuthStore
      .getState()
      .login({ email: VIEWER.email, password: "example-password" });

    await go(1_000); // the wait ends, and the resume finds the session is not its own
    await booting;

    const state = useAuthStore.getState();
    expect(server.times(REFRESH)).toBe(1); // nothing was sent for it
    expect(state.user?.id).toBe(VIEWER.id);
    expect(state.isAuthenticated).toBe(true);
    expect(state.isInitialized).toBe(true);
    expect(state.isLoading).toBe(false);
    expect(
      (
        JSON.parse(localStorage.getItem("nexara_user") ?? "{}") as {
          id: string;
        }
      ).id,
    ).toBe(VIEWER.id);
  });
});

describe("a resume at boot, where the browser has locks", () => {
  /** What another tab does: holds the refresh lock until the test lets go. */
  function anotherTabIsRefreshing() {
    const lock = installFakeLocks();
    const release = deferred<undefined>();
    void lock.request(LOCK, {}, () => release.promise);
    return { lock, release };
  }

  it("waits for the tab that is refreshing, and then resumes on the cookie it was given", async () => {
    const { lock, release } = anotherTabIsRefreshing();
    server.routes[REFRESH] = () => json(authResponse(ADMIN));

    const booting = useAuthStore.getState().initialize();
    await flush();

    // Nothing is sent while the other tab holds the lock.
    expect(server.times(REFRESH)).toBe(0);
    expect(lock.waiting).toBe(1);
    expect(useAuthStore.getState().isInitialized).toBe(false);

    release.resolve(undefined);
    await booting;

    expect(server.times(REFRESH)).toBe(1);
    expect(lock.granted).toEqual([LOCK, LOCK]);
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(useAuthStore.getState().isInitialized).toBe(true);
  });

  it("sends nothing, and leaves the user who signed in meanwhile alone, when the session changed while it waited", async () => {
    const { lock, release } = anotherTabIsRefreshing();
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[LOGIN] = () => json(authResponse(VIEWER));

    const booting = useAuthStore.getState().initialize();
    await flush();
    expect(lock.waiting).toBe(1);
    // The login page does not wait for isInitialized: someone signs in.
    await useAuthStore
      .getState()
      .login({ email: VIEWER.email, password: "example-password" });

    release.resolve(undefined);
    await booting;

    expect(server.times(REFRESH)).toBe(0); // the stored user's resume was never sent
    const state = useAuthStore.getState();
    expect(state.user?.id).toBe(VIEWER.id);
    expect(state.isAuthenticated).toBe(true);
    expect(state.isInitialized).toBe(true);
  });
});

describe("a different user who signs in while the boot resume is retrying", () => {
  // The login page does not wait for isInitialized, so someone can sign in as
  // another user while the stored user's session is still being resumed. The
  // stores were rehydrated for the STORED user — console tabs, dismissed
  // issues — and adoptIdentity, which sees nobody held while the resume has not
  // set the user, resets nothing. Whoever comes out of the spinner would have
  // them, unless initialize() forgets them first.
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  });

  async function go(ms = 0) {
    await vi.advanceTimersByTimeAsync(ms);
  }

  const down = () => json({ error: "x", message: "down" }, 503);
  const good = () =>
    json(authResponse(ADMIN, { permissions: ["view:cluster"] }));

  function somethingWasPersisted() {
    for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
  }
  function storesHold(held: boolean) {
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(held);
    }
  }
  async function signsInAs(user: typeof ADMIN) {
    server.routes[LOGIN] = () =>
      json(authResponse(user, { permissions: ["view:cluster"] }));
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  }

  it("comes out of the spinner with none of the stored user's data, when the resume stops at the wait's end", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = answers(down);

    const booting = useAuthStore.getState().initialize();
    await go(0); // the first attempt failed, and the resume is waiting to ask again
    await signsInAs(VIEWER);
    // Nothing has told the stores yet: what was rehydrated is still there, so
    // that what is gone below is gone because of the fix.
    storesHold(true);
    expect(useAuthStore.getState().isInitialized).toBe(false);

    await go(1_000);
    await booting;

    storesHold(false);
    const state = useAuthStore.getState();
    expect(state.user?.id).toBe(VIEWER.id);
    expect(state.isAuthenticated).toBe(true);
    expect(state.isInitialized).toBe(true);
    expect(server.times(REFRESH)).toBe(1); // no second attempt for a session that is not theirs
  });

  it("comes out of the spinner with none of the stored user's data, when the answer lands after the sign-in", async () => {
    somethingWasPersisted();
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;

    const booting = useAuthStore.getState().initialize();
    await go(0);
    await signsInAs(VIEWER);
    storesHold(true);

    held.resolve(good()); // the stored user's session, answered late
    await booting;

    storesHold(false);
    const state = useAuthStore.getState();
    expect(state.user?.id).toBe(VIEWER.id); // not applied over theirs
    expect(state.isInitialized).toBe(true);
  });

  it("control: the stored user signing in keeps what was rehydrated for them", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = answers(down);

    const booting = useAuthStore.getState().initialize();
    await go(0);
    await signsInAs(ADMIN);
    await go(1_000);
    await booting;

    storesHold(true);
    expect(useAuthStore.getState().user?.id).toBe(ADMIN.id);
    expect(useAuthStore.getState().isInitialized).toBe(true);
  });

  it("control: the same, when the answer lands after the stored user signed in", async () => {
    somethingWasPersisted();
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;

    const booting = useAuthStore.getState().initialize();
    await go(0);
    await signsInAs(ADMIN);
    held.resolve(good());
    await booting;

    storesHold(true);
    expect(useAuthStore.getState().isInitialized).toBe(true);
  });

  it("forgets it too when the session is signed out meanwhile and nobody signs in", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = answers(down);

    const booting = useAuthStore.getState().initialize();
    await go(0);
    clearTokens(); // the session ended while the resume waits
    await go(1_000);
    await booting;

    storesHold(false);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().isInitialized).toBe(true);
  });

  it("forgets it when the stored user has no id and nobody has signed in: two missing ids are not one user", async () => {
    // A corrupt value in localStorage: a user without an id. Compared with
    // nobody signed in — whose id is just as missing — it came out equal, and
    // what was rehydrated for it was kept for whoever signed in next.
    localStorage.setItem("nexara_user", JSON.stringify({ email: ADMIN.email }));
    somethingWasPersisted();
    server.routes[REFRESH] = answers(down);

    const booting = useAuthStore.getState().initialize();
    await go(0);
    clearTokens(); // the session ended while the resume waits
    await go(1_000);
    await booting;

    storesHold(false);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().isInitialized).toBe(true);
  });

  it("forgets it too when the stored user has no id and someone else signs in", async () => {
    localStorage.setItem("nexara_user", JSON.stringify({ email: ADMIN.email }));
    somethingWasPersisted();
    server.routes[REFRESH] = answers(down);

    const booting = useAuthStore.getState().initialize();
    await go(0);
    await signsInAs(VIEWER);
    storesHold(true);
    await go(1_000);
    await booting;

    storesHold(false);
    expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
  });

  // The claim the fix rests on: nothing of the new user's is lost, because the
  // authenticated tree is not mounted until initialize() has finished, and so
  // has written nothing yet.
  it("keeps the authenticated tree out until the data is gone, and then mounts it on empty stores", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = answers(down);
    const atMount: boolean[][] = [];
    function Page() {
      // What the stores hold at the first render of the authenticated tree.
      useState(() => {
        atMount.push(
          Object.values(PER_SESSION_STORES).map((p) => p.holdsData()),
        );
        return null;
      });
      return <p>the page</p>;
    }
    const router = createMemoryRouter(
      [
        { path: "/login", element: <p>login page</p> },
        {
          element: <ProtectedRoute />,
          children: [{ path: "/", element: <Page /> }],
        },
      ],
      { initialEntries: ["/"] },
    );
    render(<RouterProvider router={router} />);

    const booting = useAuthStore.getState().initialize();
    await act(async () => {
      await go(0);
    });
    await act(async () => {
      await signsInAs(VIEWER);
    });

    // Signed in, and the page still waits: the spinner, no tree, nothing mounted.
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(screen.queryByText("the page")).toBeNull();
    expect(atMount).toEqual([]);

    await act(async () => {
      await go(1_000);
      await booting;
    });

    expect(screen.getByText("the page")).toBeInTheDocument();
    expect(atMount).toHaveLength(1);
    expect(atMount[0]?.every((held) => !held)).toBe(true); // mounted on empty stores
  });
});

describe("a boot resume whose session changes hands during a failing attempt", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  });

  async function go(ms = 0) {
    await vi.advanceTimersByTimeAsync(ms);
  }

  it("stops at once, and the page is released, with no wait to sit through", async () => {
    const attempt = deferred<Response>();
    server.routes[REFRESH] = () => attempt.promise;
    server.routes[LOGIN] = () => json(authResponse(VIEWER));

    const booting = useAuthStore.getState().initialize();
    await go(0);
    expect(server.times(REFRESH)).toBe(1); // the first attempt is out
    await useAuthStore
      .getState()
      .login({ email: VIEWER.email, password: "example-password" });
    expect(useAuthStore.getState().isInitialized).toBe(false);

    // It fails, now that someone has signed in: the next attempt would only
    // notice, a wait later, and the signed-in user would sit on the spinner.
    attempt.resolve(json({ error: "x", message: "down" }, 503));
    await go(0);
    await booting;

    expect(useAuthStore.getState().isInitialized).toBe(true);
    expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
    expect(server.times(REFRESH)).toBe(1);
    expect(vi.getTimerCount()).toBe(0); // no wait was ever started
  });
});
