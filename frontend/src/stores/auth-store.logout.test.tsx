import { beforeEach, describe, expect, expectTypeOf, it, vi } from "vitest";
import {
  ADMIN,
  VIEWER,
  authResponse,
  callerOf,
  deferred,
  flush,
  json,
} from "@/test/fake-server";
import { installFakeLocks } from "@/test/fake-lock-manager";
import {
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import {
  LOCK,
  LOGOUT,
  LOGOUT_ALL,
  REFRESH,
  X,
  down,
  getX,
  orStalled,
  server,
  installAuthStoreHarness,
} from "@/test/api-client-harness";
import {
  apiClient,
  getStoredUser,
  signOutRequest,
  storeTokens,
} from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useAuthStore } from "./auth-store";

/**
 * Sign out has to reach the server. /auth/logout (authOptional, internal/api/
 * router.go) revokes with the refresh cookie; the access token only lets the
 * server check that the session is the caller's, which it skips for an expired
 * one. So logout() sends it with the token held, as it is, and no refresh: sent
 * as a request is, a long-expired token with a failing refresh meant it was never
 * sent and the session stayed alive. Whether it is sent depends on whose cookie
 * is in the jar, and what a sign-out ends is the session it was for.
 */

const { signInAs } = installAuthStoreHarness({
  store: useAuthStore,
  reset: emptyPerSessionStores,
});

/** What the logout request carried: whose token, or "" for none; undefined until it was sent. */
let carried: string | undefined;

beforeEach(() => {
  carried = undefined;
  server.routes[LOGOUT] = (init) => {
    carried = callerOf(init);
    return new Response(null, { status: 204 });
  };
});

/** The tab is signed out here, by the user, holding nothing: a request goes out as nobody. */
async function thisTabIsSignedOut() {
  const state = useAuthStore.getState();
  expect(state.isAuthenticated).toBe(false);
  expect(state.user).toBeNull();
  expect(state.signedOutByUser).toBe(true);
  expect(state.isLoggingOut).toBe(false);
  server.routes[X] = (init) => json({ owner: callerOf(init) });
  expect(await getX()).toEqual({ owner: "" });
}

describe("Sign out", () => {
  it.each<[string, number, () => Response | Promise<Response>]>([
    [
      "when the token expired long ago and the refresh is failing",
      -600, // by more than the allowance a token is sent past
      down,
    ],
    [
      "carrying a token that is still valid, so the server's check of the session's owner runs",
      3_600,
      () => json(authResponse(ADMIN)),
    ],
    [
      "carrying a token about to expire, without refreshing it first",
      30,
      () => json(authResponse(ADMIN)),
    ],
  ])(
    "reaches the server once, with the token held and no refresh, %s",
    async (_name, expiresIn, refresh) => {
      await signInAs(ADMIN, { expiresIn });
      server.routes[REFRESH] = refresh;

      await useAuthStore.getState().logout();

      expect(server.times(LOGOUT)).toBe(1);
      expect(server.times(REFRESH)).toBe(0); // none was tried
      // The token held, as it is: an expired one is not authenticated by the
      // server, which then revokes by the cookie alone.
      expect(carried).toBe(ADMIN.id);
      const state = useAuthStore.getState();
      expect(state.isAuthenticated).toBe(false);
      expect(state.signedOutByUser).toBe(true);
      expect(localStorage.getItem("nexara_user")).toBeNull();
    },
  );

  it("is sent during a back-off from a refresh that failed, and does not end it or ask again", async () => {
    await signInAs(ADMIN, { expiresIn: -600 });
    server.routes[REFRESH] = down;
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    await expect(getX()).rejects.toMatchObject({ name: "RefreshFailedError" });
    expect(server.times(REFRESH)).toBe(1);

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
    expect(server.times(REFRESH)).toBe(1);
    expect(carried).toBe(ADMIN.id);
  });

  it("does not wait for the lock another tab holds, though the token has expired", async () => {
    const lock = installFakeLocks();
    void lock.request(LOCK, {}, () => deferred<undefined>().promise);
    await signInAs(ADMIN, { expiresIn: -10 }); // a refresh that is needed would queue for it

    const outcome = await orStalled(useAuthStore.getState().logout());

    expect(outcome).toBeUndefined(); // logout() resolved: it did not stall
    expect(server.times(LOGOUT)).toBe(1);
    expect(lock.waiting).toBe(0);
    expect(server.times(REFRESH)).toBe(0);
  });

  it("is sent with no token when none is held", async () => {
    // Signed in as far as the store knows, with nothing in the client: a page
    // whose token is gone.
    useAuthStore.setState({
      user: ADMIN,
      isAuthenticated: true,
      isInitialized: true,
    });

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
    expect(carried).toBe("");
    expect(server.times(REFRESH)).toBe(0);
  });

  it("still signs out here when the server cannot be reached", async () => {
    await signInAs(ADMIN);
    server.routes[LOGOUT] = () => Promise.reject(new TypeError("Failed"));

    await useAuthStore.getState().logout();

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().isLoggingOut).toBe(false);
  });

  it("sign out everywhere is left on the normal path, which needs a token it can resolve: authRequired, it has nothing to revoke with otherwise", async () => {
    await signInAs(ADMIN, { expiresIn: -600 });
    server.routes[REFRESH] = down;
    server.routes[LOGOUT_ALL] = () => new Response(null, { status: 204 });

    await useAuthStore.getState().logoutAll();

    // It tried to refresh, and went no further; the user is signed out here anyway.
    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(LOGOUT_ALL)).toBe(0);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });
});

describe("signOutRequest", () => {
  const PROBE = "POST /api/v1/probe";

  it("is not refreshed after a 401, and ends nothing", async () => {
    storeTokens(authResponse(ADMIN, { expiresIn: 3_600 }));
    server.routes[LOGOUT] = () =>
      json({ error: "unauthorized", message: "expired" }, 401);
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    const before = useAuthStore.getState().isAuthenticated;

    await expect(signOutRequest()).rejects.toMatchObject({
      name: "ApiClientError",
      status: 401,
    });

    expect(server.times(LOGOUT)).toBe(1); // not replayed
    expect(server.times(REFRESH)).toBe(0);
    expect(useAuthStore.getState().isAuthenticated).toBe(before);
  });

  it("control: an ordinary request refused with a 401 does refresh and replay", async () => {
    storeTokens(authResponse(ADMIN, { expiresIn: 3_600 }));
    let answered = 0;
    server.routes[PROBE] = (init) =>
      ++answered === 1
        ? json({ error: "unauthorized", message: "expired" }, 401)
        : json({ owner: callerOf(init) });
    server.routes[REFRESH] = () => json(authResponse(ADMIN));

    await apiClient.post(apiPath`/api/v1/probe`, {});

    expect(server.times(PROBE)).toBe(2);
    expect(server.times(REFRESH)).toBe(1);
  });

  it("is a function of its own, with nothing to choose: apiClient offers no way to send with the token held, and no method of it takes a mode", () => {
    // Sending with the token held and no refresh is safe for a request that the
    // cookie authenticates and the token only corroborates: Sign out's, no other's.
    expect(apiClient).not.toHaveProperty("postWithHeldToken");
    for (const [name, method] of Object.entries(apiClient)) {
      // A path, and a body at most: a third argument would be a mode.
      expect(method.length, name).toBeLessThanOrEqual(2);
    }
    // And for the compiler, which tsc -b asks: no path to aim it at either.
    expectTypeOf(signOutRequest).parameters.toEqualTypeOf<[]>();
    expectTypeOf(apiClient).not.toHaveProperty("postWithHeldToken");
  });
});

describe("Sign out when another tab has signed someone else in", () => {
  // The cookie in the jar is theirs, and nexara_user, which every tab shares,
  // names them. This tab's own session was orphaned when theirs replaced the
  // cookie. /auth/logout revokes with the cookie, and for a token that has expired
  // the server cannot check whose it is: a request would end their session. So
  // none is sent, and this tab is signed out here, and only here: their record,
  // which a reload of their tab resumes off, is left as it is.
  const record = (user: object) => {
    localStorage.setItem("nexara_user", JSON.stringify(user));
  };

  it.each<[string, number, object, boolean]>([
    ["though the token held expired long ago", -600, VIEWER, true],
    [
      "though the token held is still valid: the cookie is theirs all the same",
      3_600,
      VIEWER,
      true,
    ],
    [
      // It names no one: no session's record to leave.
      "when what is stored cannot be told from this tab's user: a value with no id",
      -600,
      { email: "x" },
      false,
    ],
  ])(
    "sends nothing, %s, and signs this tab out",
    async (_name, expiresIn, stored, theirsStays) => {
      await signInAs(ADMIN, { expiresIn });
      record(stored);

      await useAuthStore.getState().logout();

      expect(server.times(LOGOUT)).toBe(0);
      expect(server.times(REFRESH)).toBe(0);
      await thisTabIsSignedOut();
      if (theirsStays) expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
      else expect(getStoredUser()).toBeNull();
    },
  );

  it("sends nothing when this tab holds no user and one is stored: the cookie is not its own", async () => {
    record(ADMIN);
    expect(useAuthStore.getState().user).toBeNull();

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(0);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });

  it("leaves their record where it is, so that a reload of their tab resumes off the cookie", async () => {
    await signInAs(ADMIN, { expiresIn: -600 });
    record(VIEWER);

    await useAuthStore.getState().logout();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });

    // A reload of their tab: a fresh module graph, which boots with whatever is
    // stored. With no record it would send no refresh and land on the login page,
    // though the cookie is good.
    vi.resetModules();
    server.routes[REFRESH] = () =>
      json(authResponse(VIEWER, { permissions: ["view:cluster"] }));
    const reloaded = await import("./auth-store");
    await reloaded.useAuthStore.getState().initialize();

    expect(server.times(REFRESH)).toBe(1);
    const state = reloaded.useAuthStore.getState();
    expect(state.isAuthenticated).toBe(true);
    expect(state.user?.id).toBe(VIEWER.id);
  });

  it.each<[string, number, () => void]>([
    [
      "the stored user is this tab's own, as it is for a token that expired long ago",
      -600,
      () => {
        expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
      },
    ],
    [
      "the stored user is this tab's own under another email: users are compared by id, and the record is rewritten at every refresh",
      -600,
      () => {
        record({
          ...ADMIN,
          email: "renamed@example.com",
          display_name: "Renamed",
        });
      },
    ],
    [
      "no user is stored, because another tab's session ended: that cookie may still be this tab's",
      3_600,
      () => {
        localStorage.removeItem("nexara_user");
      },
    ],
  ])("control: it is sent when %s", async (_name, expiresIn, arrange) => {
    await signInAs(ADMIN, { expiresIn });
    arrange();

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
    expect(carried).toBe(ADMIN.id);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });
});

describe.each([
  ["Sign out", LOGOUT, () => useAuthStore.getState().logout()],
  [
    "Sign out everywhere",
    LOGOUT_ALL,
    () => useAuthStore.getState().logoutAll(),
  ],
] as const)(
  "%s whose session changes hands while its request is out",
  (_name, route, start) => {
    // The sign-out ends the session it was for, once the server has answered, and
    // not whichever one is current by then: clearTokens() ends the current one, and
    // takes nexara_user, which another tab may be resuming with, along.
    let held: ReturnType<typeof deferred<Response>>;

    async function theRequestIsOut() {
      await signInAs(ADMIN);
      held = deferred<Response>();
      server.routes[route] = () => held.promise;
      const signingOut = start();
      await flush();
      expect(server.times(route)).toBe(1);
      // In an object: returned as it is, an async function would wait for it, and
      // the request is held until the test lets it go.
      return { signingOut };
    }

    const theViewersOwnStateIsInTheStores = () => {
      for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
    };

    async function theViewersSessionIsStillTheirs() {
      const state = useAuthStore.getState();
      expect(state.user?.id).toBe(VIEWER.id);
      expect(state.isAuthenticated).toBe(true);
      expect(state.signedOutByUser).toBe(false);
      // The flag the sign-out raised for its request is down: left up, it drops
      // every refresh's answer for the rest of the session.
      expect(state.isLoggingOut).toBe(false);
      expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
      for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
        expect(probe.holdsData(), file).toBe(true);
      }
      // And the token held is still theirs.
      server.routes[X] = (init) => json({ owner: callerOf(init) });
      expect(await getX()).toEqual({ owner: VIEWER.id });
    }

    it.each([
      [
        "signed in meanwhile",
        async () => {
          await signInAs(VIEWER);
        },
      ],
      [
        "signed in after the first had ended meanwhile, as a refresh the server refused ends one",
        async () => {
          useAuthStore.getState().clearAuth(); // the forced sign-out
          await signInAs(VIEWER);
        },
      ],
    ])("does not end the session of the user who %s", async (_name, change) => {
      const { signingOut } = await theRequestIsOut();

      await change();
      theViewersOwnStateIsInTheStores();
      held.resolve(new Response(null, { status: 204 }));
      await signingOut;

      await theViewersSessionIsStillTheirs();
    });

    it("does not end the session of the same user, who signed in again meanwhile: a new session, and the store holds a new user object", async () => {
      const { signingOut } = await theRequestIsOut();

      await signInAs(ADMIN);
      theViewersOwnStateIsInTheStores();
      held.resolve(new Response(null, { status: 204 }));
      await signingOut;

      const state = useAuthStore.getState();
      expect(state.user?.id).toBe(ADMIN.id);
      expect(state.isAuthenticated).toBe(true);
      expect(state.isLoggingOut).toBe(false);
      for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
        expect(probe.holdsData(), file).toBe(true);
      }
    });

    it("leaves a session that ended meanwhile as it ended: signed out, and by the user", async () => {
      const { signingOut } = await theRequestIsOut();

      useAuthStore.getState().clearAuth(); // what the forced sign-out does
      held.resolve(new Response(null, { status: 204 }));
      await signingOut;

      const state = useAuthStore.getState();
      expect(state.isAuthenticated).toBe(false);
      expect(state.user).toBeNull();
      expect(state.signedOutByUser).toBe(true); // the user had asked for it
      expect(state.isLoggingOut).toBe(false);
    });

    it("control: still ends the session when the store's user is replaced by an equal object meanwhile: the same session, the same epoch, nothing changed hands", async () => {
      const { signingOut } = await theRequestIsOut();

      useAuthStore.setState({ user: { ...ADMIN } }); // a profile edit applied to the store, say
      held.resolve(new Response(null, { status: 204 }));
      await signingOut;

      const state = useAuthStore.getState();
      expect(state.isAuthenticated).toBe(false);
      expect(state.user).toBeNull();
      expect(state.isLoggingOut).toBe(false);
    });

    it("control: with nothing changing hands, the session ends and what it left is forgotten", async () => {
      const { signingOut } = await theRequestIsOut();
      theViewersOwnStateIsInTheStores();

      held.resolve(new Response(null, { status: 204 }));
      await signingOut;

      const state = useAuthStore.getState();
      expect(state.isAuthenticated).toBe(false);
      expect(state.user).toBeNull();
      expect(state.signedOutByUser).toBe(true);
      expect(state.isLoggingOut).toBe(false);
      for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
        expect(probe.holdsData(), file).toBe(false);
      }
    });
  },
);

describe("Sign out everywhere whose refresh is answered for another user", () => {
  // The token has expired, so the request refreshes first, on the shared cookie
  // another tab has since signed someone else in on. The answer is theirs: a
  // session begins in this tab's client, and the store, which drops what a refresh
  // brings during a sign-out, does not follow: the tab would show the first user
  // and act as the second. The request is not sent (it must not revoke the
  // second's sessions), and the sign-out is finished.
  it("is not sent as them, and ends the tab: nobody is left in it", async () => {
    await signInAs(ADMIN, { expiresIn: -600 });
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    server.routes[LOGOUT_ALL] = () => new Response(null, { status: 204 });

    await useAuthStore.getState().logoutAll();

    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(LOGOUT_ALL)).toBe(0);
    const state = useAuthStore.getState();
    expect(state.isAuthenticated).toBe(false);
    expect(state.user).toBeNull();
    expect(state.signedOutByUser).toBe(true);
    expect(state.isLoggingOut).toBe(false);
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    expect(await getX()).toEqual({ owner: "" });
    // The record the answer wrote is this tab's now (it holds that user's token)
    // and goes with the session that ends.
    expect(getStoredUser()).toBeNull();
  });
});
