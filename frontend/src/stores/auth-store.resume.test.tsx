import { beforeEach, describe, expect, it, vi } from "vitest";
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
  json,
  type Route,
} from "@/test/fake-server";
import { installFakeLocks } from "@/test/fake-lock-manager";
import {
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import {
  LOCK,
  REFRESH,
  answers,
  down,
  fakeTimers,
  go,
  server,
  superseded,
  tooMany,
  installAuthStoreHarness,
} from "@/test/api-client-harness";
import { clearTokens, currentSessionEpoch } from "@/lib/api-client";
import type { User } from "@/types/api";
import { useAuthStore } from "./auth-store";

/**
 * The boot resume (initialize() with a stored user) refreshes off the cookie as
 * every refresh is sent (resumeSession, lib/api-client.ts): under the tabs' lock
 * where there is one, asked once more when another tab's refresh won the race for
 * the cookie. A resume that could not look (429, 5xx, no network, no session in
 * the answer) is asked again while the spinner stays up (isInitialized false);
 * only a REFUSED cookie ends it at once, and that takes nexara_user, which every
 * tab shares, so every other tab's next reload lands on the login page.
 */

const { signInAs } = installAuthStoreHarness({
  store: useAuthStore,
  reset: emptyPerSessionStores,
  boot: false,
  random: true,
});

// A stored user: this page is resuming a session.
beforeEach(() => {
  localStorage.setItem("nexara_user", JSON.stringify(ADMIN));
});

const good = () => json(authResponse(ADMIN, { permissions: ["view:cluster"] }));
const refused = (status: number): Route => {
  return () =>
    json({ error: "unauthorized", message: "Invalid or expired" }, status);
};

/** What the page rehydrated for the stored user: console tabs, dismissed issues, ... */
function somethingWasPersisted() {
  for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
}
function storesHold(held: boolean) {
  for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
    expect(probe.holdsData(), file).toBe(held);
  }
}
/** The stores and the stored user, which every tab of the browser shares. */
function nothingWasWiped() {
  storesHold(true);
  expect(localStorage.getItem("nexara_user")).not.toBeNull();
}
function everythingWasWiped() {
  storesHold(false);
  expect(localStorage.getItem("nexara_user")).toBeNull();
}

describe("a resume at boot that the server says another tab won", () => {
  beforeEach(fakeTimers); // the second ask waits on a timer

  it("asks once more, and signs the user in", async () => {
    server.routes[REFRESH] = answers(superseded, good);

    const booting = useAuthStore.getState().initialize();
    await go();
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
  // resume has an answer it will act on. Math.random is pinned at 0, so the waits
  // are 1 s, 2 s and 4 s exactly; the second ask of a superseded refresh (250 ms)
  // is inside an attempt.
  beforeEach(fakeTimers);

  it("a refresh that lost its race twice, then the next attempt: signed in, and nothing wiped", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = answers(superseded, superseded, good);

    const booting = useAuthStore.getState().initialize();
    await go();
    await go(250); // the attempt's own second ask, lost as well
    expect(server.times(REFRESH)).toBe(2);
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

  it.each<[string, Route]>([
    ["a 503", down],
    ["a 429", tooMany()],
    ["the network failing", () => Promise.reject(new TypeError("Failed"))],
    ["an answer that is no session", () => json({ user: { id: "x" } })],
  ])(
    "%s, then the session: signed in, nothing wiped, and the spinner was up while it waited",
    async (_name, failure) => {
      somethingWasPersisted();
      server.routes[REFRESH] = answers(failure, good);

      const booting = useAuthStore.getState().initialize();
      await go();
      expect(server.times(REFRESH)).toBe(1);
      expect(useAuthStore.getState().isInitialized).toBe(false);
      expect(useAuthStore.getState().isLoading).toBe(true);

      await go(1_000);
      await booting;

      expect(server.times(REFRESH)).toBe(2);
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(useAuthStore.getState().isInitialized).toBe(true);
      nothingWasWiped();
    },
  );

  it.each([
    [401, 0],
    [403, 0],
    [401, 1],
    [403, 1],
  ])(
    "a %i after %i failed attempts ends the session there: no attempt after it, and it ends once",
    async (status, failures) => {
      somethingWasPersisted();
      const earlier = Array.from({ length: failures }, () => down);
      server.routes[REFRESH] = answers(...earlier, refused(status));
      const before = currentSessionEpoch();

      const booting = useAuthStore.getState().initialize();
      await go();
      if (failures > 0) {
        expect(useAuthStore.getState().isInitialized).toBe(false);
        await go(failures * 1_000);
      }
      await booting;

      expect(useAuthStore.getState().isAuthenticated).toBe(false);
      expect(useAuthStore.getState().isInitialized).toBe(true);
      everythingWasWiped();
      expect(currentSessionEpoch()).toBe(before + 1); // ended once
      await go(60_000);
      expect(server.times(REFRESH)).toBe(failures + 1);
    },
  );

  it("fails every attempt: four, 1 s, 2 s and 4 s apart, and then the session ends, once", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = down;
    const before = currentSessionEpoch();

    const booting = useAuthStore.getState().initialize();
    await go();
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
    await go();
    await go(500); // longer than the 250-500 ms of a second ask: none is made
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

    const booting = useAuthStore.getState().initialize();
    await go();
    expect(server.times(REFRESH)).toBe(1);
    await signInAs(VIEWER, { permissions: ["view:cluster"] });

    await go(1_000); // the wait ends, and the resume finds the session is not its own
    await booting;

    const state = useAuthStore.getState();
    expect(server.times(REFRESH)).toBe(1); // nothing was sent for it
    expect(state.user?.id).toBe(VIEWER.id);
    expect(state.isAuthenticated).toBe(true);
    expect(state.isInitialized).toBe(true);
    expect(state.isLoading).toBe(false);
    expect(
      (JSON.parse(localStorage.getItem("nexara_user") ?? "{}") as User).id,
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

    const booting = useAuthStore.getState().initialize();
    await flush();
    expect(lock.waiting).toBe(1);
    await signInAs(VIEWER);

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
  // stores were rehydrated for the STORED user, and adoptIdentity, which sees
  // nobody held, resets nothing: whoever comes out of the spinner would have
  // them, unless initialize() forgets them first.
  beforeEach(fakeTimers);

  it.each<[string, User, "waits" | "answers late"]>([
    [
      "forgets the stored user's data, and does not send a second attempt, when the resume stops at the wait's end",
      VIEWER,
      "waits",
    ],
    [
      "forgets the stored user's data, and does not apply the answer over theirs, when it lands after the sign-in",
      VIEWER,
      "answers late",
    ],
    [
      "control: the stored user signing in keeps what was rehydrated for them",
      ADMIN,
      "waits",
    ],
    [
      "control: the same, when the answer lands after the stored user signed in",
      ADMIN,
      "answers late",
    ],
  ])("%s", async (_name, who, resumeEnds) => {
    somethingWasPersisted();
    const held = deferred<Response>();
    server.routes[REFRESH] = resumeEnds === "waits" ? down : () => held.promise;

    const booting = useAuthStore.getState().initialize();
    await go(); // the first attempt has failed and waits to ask again, or is still out
    await signInAs(who, { permissions: ["view:cluster"] });
    // Nothing has told the stores yet: what was rehydrated is still there, so
    // that what is gone below is gone because of the fix.
    storesHold(true);
    expect(useAuthStore.getState().isInitialized).toBe(false);

    if (resumeEnds === "waits") await go(1_000);
    else held.resolve(good()); // the stored user's session, answered late
    await booting;

    storesHold(who.id === ADMIN.id);
    expect(useAuthStore.getState().user?.id).toBe(who.id);
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(useAuthStore.getState().isInitialized).toBe(true);
    if (resumeEnds === "waits") expect(server.times(REFRESH)).toBe(1);
  });

  it.each<[string, object, User | null]>([
    [
      "forgets it too when the session is signed out meanwhile and nobody signs in",
      ADMIN,
      null,
    ],
    // A corrupt value in localStorage: a user without an id. Compared with nobody
    // signed in, whose id is just as missing, it came out equal, and what was
    // rehydrated for it was kept for whoever signed in next.
    [
      "forgets it when the stored user has no id and nobody has signed in: two missing ids are not one user",
      { email: ADMIN.email },
      null,
    ],
    [
      "forgets it too when the stored user has no id and someone else signs in",
      { email: ADMIN.email },
      VIEWER,
    ],
  ])("%s", async (_name, stored, signsIn) => {
    localStorage.setItem("nexara_user", JSON.stringify(stored));
    somethingWasPersisted();
    server.routes[REFRESH] = down;

    const booting = useAuthStore.getState().initialize();
    await go();
    if (signsIn === null) {
      clearTokens(); // the session ended while the resume waits
    } else {
      await signInAs(signsIn, { permissions: ["view:cluster"] });
      storesHold(true);
    }
    await go(1_000);
    await booting;

    storesHold(false);
    expect(useAuthStore.getState().user?.id).toBe(signsIn?.id);
    expect(useAuthStore.getState().isAuthenticated).toBe(signsIn !== null);
    expect(useAuthStore.getState().isInitialized).toBe(true);
  });

  // The claim the fix rests on: nothing of the new user's is lost, because the
  // authenticated tree is not mounted until initialize() has finished, and so has
  // written nothing yet.
  it("keeps the authenticated tree out until the data is gone, and then mounts it on empty stores", async () => {
    somethingWasPersisted();
    server.routes[REFRESH] = down;
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
      await go();
    });
    await act(async () => {
      await signInAs(VIEWER, { permissions: ["view:cluster"] });
    });

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
  beforeEach(fakeTimers);

  it("stops at once, and the page is released, with no wait to sit through", async () => {
    const attempt = deferred<Response>();
    server.routes[REFRESH] = () => attempt.promise;

    const booting = useAuthStore.getState().initialize();
    await go();
    expect(server.times(REFRESH)).toBe(1); // the first attempt is out
    await signInAs(VIEWER);
    expect(useAuthStore.getState().isInitialized).toBe(false);

    // It fails, now that someone has signed in: the next attempt would only
    // notice, a wait later, and the signed-in user would sit on the spinner.
    attempt.resolve(down());
    await go();
    await booting;

    expect(useAuthStore.getState().isInitialized).toBe(true);
    expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
    expect(server.times(REFRESH)).toBe(1);
    expect(vi.getTimerCount()).toBe(0); // no wait was ever started
  });
});
