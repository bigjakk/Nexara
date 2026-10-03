import {
  afterEach,
  beforeEach,
  describe,
  expect,
  expectTypeOf,
  it,
  vi,
} from "vitest";
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
import {
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import {
  apiClient,
  clearTokens,
  getStoredUser,
  signOutRequest,
  storeTokens,
} from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import type { User } from "@/types/api";
import { useAuthStore } from "./auth-store";

/**
 * Sign out has to reach the server. /auth/logout is authOptional
 * (internal/api/router.go): the credential it revokes with is the refresh
 * cookie, and the access token only lets the server check that the session is
 * the caller's — which it does for a token that is still valid and skips for
 * one that has expired. So logout() sends it with the token held, as it is, and
 * with no refresh: not started, not waited for, not after a 401. Sent the way a
 * request is, it had a token to resolve first, and when the token had long
 * expired and the refresh was failing it was never sent at all — the user was
 * signed out here, and the session and its cookie stayed alive on the server
 * until the refresh token's lifetime ran out.
 *
 * Whether it is sent at all is judged by whose cookie is in the jar: another
 * tab that signed someone else in has replaced it, nexara_user names them, and
 * a Sign out that sent would revoke THEIR session — the server cannot tell, for
 * a token that has expired.
 *
 * And what a sign-out ends is the session it was for. It ends it after a
 * request that takes time, and a session that began meanwhile — or ended, with
 * another's begun after it — is not its to end.
 *
 * Real store and api-client, only fetch replaced (test/fake-server.ts).
 */

const LOGIN = "POST /api/v1/auth/login";
const LOGOUT = "POST /api/v1/auth/logout";
const LOGOUT_ALL = "POST /api/v1/auth/logout-all";
const REFRESH = "POST /api/v1/auth/refresh";
const X = "GET /api/v1/x";
const LOCK = "nexara:auth-refresh";

let server: FakeServer;
/** What the logout request carried: whose token, or "" for none; undefined until it was sent. */
let carried: string | undefined;

const down = () => json({ error: "x", message: "down" }, 503);

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
    signedOutByUser: false,
  });
}

/** Signs `user` in through the store, with a token that expires in `expiresIn` seconds. */
async function signedInAs(user: User, expiresIn: number) {
  server.routes[LOGIN] = () => json(authResponse(user, { expiresIn }));
  await useAuthStore
    .getState()
    .login({ email: user.email, password: "example-password" });
}

beforeEach(async () => {
  localStorage.clear();
  clearTokens();
  emptyPerSessionStores();
  signedOutState();
  server = installFakeServer();
  carried = undefined;
  server.routes[LOGOUT] = (init) => {
    carried = callerOf(init);
    return new Response(null, { status: 204 });
  };
  // Registers the forced-logout and refresh callbacks, as main.tsx does at
  // boot. No stored user, so it returns at once.
  server.routes[REFRESH] = () => json({}, 401);
  await useAuthStore.getState().initialize();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  removeFakeLocks();
  clearTokens();
  localStorage.clear();
  emptyPerSessionStores();
  signedOutState();
});

describe("Sign out", () => {
  it("reaches the server, once, with no refresh, when the token expired long ago and the refresh is failing", async () => {
    await signedInAs(ADMIN, -600); // expired by more than the allowance a token is sent past
    server.routes[REFRESH] = down;

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
    expect(server.times(REFRESH)).toBe(0); // none was tried
    // The token held, as it is: an expired one is not authenticated by the
    // server, which then revokes by the cookie alone.
    expect(carried).toBe(ADMIN.id);
    // And the user is signed out here as well, by themselves.
    const state = useAuthStore.getState();
    expect(state.isAuthenticated).toBe(false);
    expect(state.signedOutByUser).toBe(true);
    expect(localStorage.getItem("nexara_user")).toBeNull();
  });

  it("carries the token held when it is still valid, so the server's check of the session's owner runs", async () => {
    await signedInAs(ADMIN, 3_600);

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
    expect(carried).toBe(ADMIN.id);
    expect(server.times(REFRESH)).toBe(0);
  });

  it("carries the token held when it is about to expire, and does not refresh it first", async () => {
    await signedInAs(ADMIN, 30);
    server.routes[REFRESH] = () => json(authResponse(ADMIN));

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
    expect(server.times(REFRESH)).toBe(0);
    expect(carried).toBe(ADMIN.id);
  });

  it("is sent during a back-off from a refresh that failed, and does not end it or ask again", async () => {
    await signedInAs(ADMIN, -600);
    server.routes[REFRESH] = down;
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    // A read fails with the refresh's own failure, and the next refresh waits.
    await expect(apiClient.get(apiPath`/api/v1/x`)).rejects.toMatchObject({
      name: "RefreshFailedError",
    });
    expect(server.times(REFRESH)).toBe(1);

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
    expect(server.times(REFRESH)).toBe(1);
    expect(carried).toBe(ADMIN.id);
  });

  it("does not wait for the lock another tab holds, though the token has expired", async () => {
    const lock = installFakeLocks();
    // Another tab's refresh hangs, holding the lock.
    void lock.request(LOCK, {}, () => deferred<undefined>().promise);
    await signedInAs(ADMIN, -10); // a refresh that is needed would queue for it

    const outcome = await Promise.race([
      useAuthStore.getState().logout(),
      flush().then(() => "stalled"),
    ]);

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
    await signedInAs(ADMIN, 3_600);
    server.routes[LOGOUT] = () => Promise.reject(new TypeError("Failed"));

    await useAuthStore.getState().logout();

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().isLoggingOut).toBe(false);
  });

  describe("sign out everywhere", () => {
    it("is left on the normal path, which needs a token it can resolve: authRequired, it has nothing to revoke with otherwise", async () => {
      await signedInAs(ADMIN, -600);
      server.routes[REFRESH] = down;
      server.routes[LOGOUT_ALL] = () => new Response(null, { status: 204 });

      await useAuthStore.getState().logoutAll();

      // It tried to refresh, and went no further: as before, and not this
      // change's to alter. The user is signed out here regardless.
      expect(server.times(REFRESH)).toBe(1);
      expect(server.times(LOGOUT_ALL)).toBe(0);
      expect(useAuthStore.getState().isAuthenticated).toBe(false);
    });
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
    // The way to send with the token held and no refresh is safe for a request
    // that the cookie authenticates and the token only corroborates, which is
    // Sign out's and no other's.
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
  // cookie. /auth/logout revokes with the cookie, and for a token that has
  // expired the server cannot check whose it is: a request would end their
  // session. So none is sent, and this tab is signed out here, and only here:
  // their record, which a reload of their tab resumes off, is left as it is.
  function anotherTabSignedIn(user: User) {
    localStorage.setItem("nexara_user", JSON.stringify(user));
  }

  async function thisTabIsSignedOut() {
    const state = useAuthStore.getState();
    expect(state.isAuthenticated).toBe(false);
    expect(state.user).toBeNull();
    expect(state.signedOutByUser).toBe(true);
    expect(state.isLoggingOut).toBe(false);
    // Nothing is held in the tab either: a request goes out as nobody.
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({ owner: "" });
  }

  it("sends nothing, though the token held expired long ago, and signs this tab out", async () => {
    await signedInAs(ADMIN, -600);
    anotherTabSignedIn(VIEWER);

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(0);
    expect(server.times(REFRESH)).toBe(0);
    await thisTabIsSignedOut();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id }); // theirs, still
  });

  it("leaves their record where it is, so that a reload of their tab resumes off the cookie", async () => {
    await signedInAs(ADMIN, -600);
    anotherTabSignedIn(VIEWER);

    await useAuthStore.getState().logout();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });

    // A reload of their tab: a fresh module graph, which boots with whatever is
    // stored. With no record it would send no refresh and land on the login
    // page, though the cookie is good.
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

  it("sends nothing either when the token held is still valid: the cookie is theirs all the same", async () => {
    await signedInAs(ADMIN, 3_600);
    anotherTabSignedIn(VIEWER);

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(0);
    await thisTabIsSignedOut();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
  });

  it("sends nothing when what is stored cannot be told from this tab's user: a value with no id", async () => {
    await signedInAs(ADMIN, -600);
    localStorage.setItem("nexara_user", JSON.stringify({ email: "x" }));

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(0);
    await thisTabIsSignedOut();
    expect(getStoredUser()).toBeNull(); // it names no one: no session's record to leave
  });

  it("sends nothing when this tab holds no user and one is stored: the cookie is not its own", async () => {
    anotherTabSignedIn(ADMIN);
    expect(useAuthStore.getState().user).toBeNull();

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(0);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });

  it("control: with the stored user this tab's own, it is sent, as it is for a token that expired long ago", async () => {
    await signedInAs(ADMIN, -600);
    expect(getStoredUser()).toMatchObject({ id: ADMIN.id });

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
    expect(carried).toBe(ADMIN.id);
  });

  it("control: with the stored user this tab's own under another email, it is sent: users are compared by id, and the record is rewritten at every refresh", async () => {
    await signedInAs(ADMIN, -600);
    localStorage.setItem(
      "nexara_user",
      JSON.stringify({
        ...ADMIN,
        email: "renamed@example.com",
        display_name: "Renamed",
      }),
    );

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
    expect(carried).toBe(ADMIN.id);
  });

  it("control: with no user stored — another tab's session ended — it is sent: that cookie may still be this tab's", async () => {
    await signedInAs(ADMIN, 3_600);
    localStorage.removeItem("nexara_user");

    await useAuthStore.getState().logout();

    expect(server.times(LOGOUT)).toBe(1);
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
      await signedInAs(ADMIN, 3_600);
      held = deferred<Response>();
      server.routes[route] = () => held.promise;
      const signingOut = start();
      await flush();
      expect(server.times(route)).toBe(1);
      // In an object: returned as it is, an async function would wait for it, and
      // the request is held until the test lets it go.
      return { signingOut };
    }

    function theViewersOwnStateIsInTheStores() {
      for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
    }

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
      expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
        owner: VIEWER.id,
      });
    }

    it("does not end the session of the user who signed in meanwhile", async () => {
      const { signingOut } = await theRequestIsOut();

      await signedInAs(VIEWER, 3_600);
      theViewersOwnStateIsInTheStores();
      held.resolve(new Response(null, { status: 204 }));
      await signingOut;

      await theViewersSessionIsStillTheirs();
    });

    it("does not end the session of the same user, who signed in again meanwhile: a new session, and the store holds a new user object", async () => {
      const { signingOut } = await theRequestIsOut();

      await signedInAs(ADMIN, 3_600);
      for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
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

    it("does not end a session that began after the first had ended meanwhile, as a refresh the server refused ends one", async () => {
      const { signingOut } = await theRequestIsOut();

      useAuthStore.getState().clearAuth(); // the forced sign-out
      await signedInAs(VIEWER, 3_600);
      theViewersOwnStateIsInTheStores();
      held.resolve(new Response(null, { status: 204 }));
      await signingOut;

      await theViewersSessionIsStillTheirs();
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

    it("control: still ends the session when the store's user is replaced by an equal object meanwhile — the same session, the same epoch: nothing changed hands", async () => {
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
      for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();

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
  // The token has expired, so the request refreshes first — on the cookie the
  // tabs share, which another tab has since signed someone else in on. The
  // answer is the other user's: a session begins in this tab's client, and the
  // store, which drops what a refresh brings while a sign-out is under way, does
  // not follow. Left so, the tab would show the first user and act as the second.
  // The request is not sent (it was the first user's, and it must not revoke the
  // second's sessions), and the sign-out is finished.
  it("is not sent as them, and ends the tab: nobody is left in it", async () => {
    await signedInAs(ADMIN, -600);
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
    // Neither user's token is held: a request goes out as nobody.
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({ owner: "" });
    // The record the answer wrote is this tab's now — it holds that user's token
    // — and goes with the session that ends.
    expect(getStoredUser()).toBeNull();
  });
});
