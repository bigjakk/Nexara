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
  deferred,
  installFakeServer,
  json,
  VIEWER,
  type FakeServer,
} from "@/test/fake-server";
import {
  ApiClientError,
  apiClient,
  clearTokens,
  currentSessionEpoch,
  getStoredUser,
  sessionScope,
  setAuthFailureCallback,
  setAuthRefreshCallback,
  StaleSessionError,
  storeTokens,
} from "./api-client";
import { apiPath } from "./api-path";

/**
 * Which session a refresh, and a request, belongs to. The access token and the
 * refresh in flight are module state; nothing tied an answer to the session
 * that asked for it, so a refresh that landed after Sign out signed the user
 * back in, and one that landed after the next user had signed in replaced
 * their token (lib/api-client.ts, the session epoch).
 *
 * A session "ends" with clearTokens() and "begins" with storeTokens(); a
 * refresh rotates the tokens of the session it belongs to and is neither.
 */

const REFRESH = "POST /api/v1/auth/refresh";
const X = "GET /api/v1/x";
const Y = "GET /api/v1/y";

let server: FakeServer;
let onFailure: Mock<() => void>;
let onRefresh: Mock<(res: AuthResponse) => void>;

/** A token that is about to expire, so the next request refreshes it first. */
const nearExpiry = { expiresIn: 30 };

beforeEach(() => {
  localStorage.clear();
  clearTokens();
  server = installFakeServer();
  onFailure = vi.fn<() => void>();
  onRefresh = vi.fn<(res: AuthResponse) => void>();
  setAuthFailureCallback(onFailure);
  setAuthRefreshCallback(onRefresh);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  clearTokens();
  localStorage.clear();
});

/** What a request settles as: the error it failed with, or "sent". */
function settle(request: Promise<unknown>) {
  return request.then(
    () => "sent",
    (err: unknown) => err,
  );
}

/**
 * The api-client as a page load finds it, before auth-store's initialize() has
 * run: nobody signed in, and no session has ended in it yet. clearTokens() is
 * not that — it records that a session ended, which is what stops a later
 * request from resuming one off the refresh cookie, and what initialize() does
 * for a page with no stored user (auth-store.session.test.tsx) — so a test of
 * what a fresh page does gets a copy of the module of its own.
 */
async function pageLoad() {
  vi.resetModules();
  const client = await import("./api-client");
  client.setAuthFailureCallback(onFailure);
  client.setAuthRefreshCallback(onRefresh);
  return client;
}

describe("the session epoch", () => {
  it("is bumped when a session begins and when it ends, and not by a rotation of its tokens", async () => {
    const before = currentSessionEpoch();
    storeTokens(authResponse(ADMIN, nearExpiry));
    const began = currentSessionEpoch();
    expect(began).toBeGreaterThan(before);

    // A rotation: the token is about to expire, so the next request refreshes
    // it (the same user, a new token) before it goes.
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = () => json({ ok: true });
    await apiClient.get(apiPath`/api/v1/x`);
    expect(server.times(REFRESH)).toBe(1);
    expect(currentSessionEpoch()).toBe(began);

    clearTokens();
    expect(currentSessionEpoch()).toBeGreaterThan(began);
  });
});

describe("a refresh answered after the session that asked for it ended", () => {
  /** A request waiting on a refresh that the test holds back. */
  function requestWaitingOnARefresh(
    held: ReturnType<typeof deferred<Response>>,
  ) {
    storeTokens(authResponse(ADMIN, nearExpiry));
    server.routes[REFRESH] = () => held.promise;
    server.routes[X] = () => json({ ok: true });
    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    return request;
  }

  it("is dropped: nothing is stored, no callback runs, and the request waiting on it is not sent", async () => {
    const held = deferred<Response>();
    const request = requestWaitingOnARefresh(held);
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });

    clearTokens(); // the session ends while the refresh is out
    held.resolve(json(authResponse(ADMIN)));

    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(getStoredUser()).toBeNull();
    expect(onRefresh).not.toHaveBeenCalled();
    expect(onFailure).not.toHaveBeenCalled();
    // The request belonged to the ended session: it is not sent for whoever
    // is signed in by now.
    expect(server.times(X)).toBe(0);
  });

  it("does not replace the tokens of the user who signed in since", async () => {
    const held = deferred<Response>();
    const request = requestWaitingOnARefresh(held);
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    clearTokens();
    storeTokens(authResponse(VIEWER)); // the next user signs in

    held.resolve(json(authResponse(ADMIN))); // the answer the first one's refresh got
    await request;

    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
    expect(onRefresh).not.toHaveBeenCalled();
    server.routes[Y] = (init) => json({ owner: callerOf(init) });
    expect(await apiClient.get(apiPath`/api/v1/y`)).toEqual({
      owner: VIEWER.id,
    });
  });

  it("does not end the session of the user who signed in since, when it FAILS", async () => {
    const held = deferred<Response>();
    const request = requestWaitingOnARefresh(held);
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    clearTokens();
    storeTokens(authResponse(VIEWER));

    held.resolve(json({}, 401)); // the ended session's refresh token was refused
    expect(await request).toBeInstanceOf(StaleSessionError);

    // Not the next user's refresh failing: their tokens and session stand.
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
    server.routes[Y] = (init) => json({ owner: callerOf(init) });
    expect(await apiClient.get(apiPath`/api/v1/y`)).toEqual({
      owner: VIEWER.id,
    });
  });

  it("does not end the session of the user who signed in since, when the network FAILS", async () => {
    const held = deferred<Response>();
    const request = requestWaitingOnARefresh(held);
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    clearTokens();
    storeTokens(authResponse(VIEWER));

    held.reject(new TypeError("Failed to fetch"));

    // Reported as what it is — the ended session's — not as a failure of the
    // session now current.
    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
  });

  it("control: the same refresh, answered while its session is still current, rotates the tokens", async () => {
    const held = deferred<Response>();
    const request = requestWaitingOnARefresh(held);
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });

    held.resolve(json(authResponse(ADMIN, { permissions: ["view:cluster"] })));

    expect(await request).toBe("sent");
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(server.times(X)).toBe(1);
    expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
  });
});

describe("a refresh that was joined", () => {
  it("is not forgotten by the one that ended first, so a newer session's refresh is still shared", async () => {
    // The first session's refresh, held.
    storeTokens(authResponse(ADMIN, nearExpiry));
    const first = deferred<Response>();
    const second = deferred<Response>();
    let refreshes = 0;
    server.routes[REFRESH] = () =>
      ++refreshes === 1 ? first.promise : second.promise;
    server.routes[X] = () => json({ ok: true });
    server.routes[Y] = () => json({ ok: true });
    const ended = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });

    // It ends, and the next user's token is about to expire too: their own
    // request starts a refresh of its own.
    clearTokens();
    storeTokens(authResponse(VIEWER, nearExpiry));
    const viewers = settle(apiClient.get(apiPath`/api/v1/y`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(2);
    });

    // The first one's refresh settles (stale) ...
    first.resolve(json(authResponse(ADMIN)));
    expect(await ended).toBeInstanceOf(StaleSessionError);
    // ... and must not have cleared the second's from under it: a request
    // made now joins it instead of starting a third.
    const joined = settle(apiClient.get(apiPath`/api/v1/y`));
    await new Promise((r) => setTimeout(r, 20));
    expect(server.times(REFRESH)).toBe(2);

    second.resolve(json(authResponse(VIEWER)));
    expect(await viewers).toBe("sent");
    expect(await joined).toBe("sent");
    expect(server.times(REFRESH)).toBe(2);
  });
});

describe("a request whose session ended while it was in flight", () => {
  it("is not retried as the user who signed in since, when its 401 arrives", async () => {
    storeTokens(authResponse(ADMIN));
    const answer = deferred<Response>();
    server.routes[X] = () => answer.promise;
    // The next user's cookie would refresh fine: unguarded, the 401 below would
    // be answered by a refresh and the request replayed under their token.
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(X)).toBe(1);
    });

    clearTokens();
    storeTokens(authResponse(VIEWER));
    answer.resolve(json({ error: "unauthorized", message: "expired" }, 401));

    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(server.times(REFRESH)).toBe(0);
    expect(server.times(X)).toBe(1); // never replayed
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
  });

  it("control: a 401 for the session that is still current is refreshed and retried", async () => {
    storeTokens(authResponse(ADMIN));
    let reads = 0;
    server.routes[X] = (init) =>
      ++reads === 1
        ? json({ error: "unauthorized", message: "expired" }, 401)
        : json({ owner: callerOf(init) });
    server.routes[REFRESH] = () => json(authResponse(ADMIN));

    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: ADMIN.id,
    });
    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(X)).toBe(2);
  });
});

describe("a refresh whose answer is only partly in when its session ends", () => {
  /** A response whose headers are in and whose body is held back. */
  function responseWithHeldBody(body: Promise<unknown>): Response {
    return { ok: true, status: 200, json: () => body } as unknown as Response;
  }

  it("is dropped when its body arrives after the session ended", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const body = deferred<unknown>();
    server.routes[REFRESH] = () => responseWithHeldBody(body.promise);
    server.routes[X] = () => json({ ok: true });
    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    await new Promise((r) => setTimeout(r, 5)); // the headers are in; json() is awaited

    clearTokens();
    body.resolve(authResponse(ADMIN));

    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(getStoredUser()).toBeNull();
    expect(onRefresh).not.toHaveBeenCalled();
    expect(server.times(X)).toBe(0);
  });

  it("is reported as stale, not as a parse error, when its unreadable body arrives after the session ended", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const body = deferred<unknown>();
    server.routes[REFRESH] = () => responseWithHeldBody(body.promise);
    server.routes[X] = () => json({ ok: true });
    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    await new Promise((r) => setTimeout(r, 5));

    clearTokens();
    storeTokens(authResponse(VIEWER));
    body.reject(new SyntaxError("Unexpected end of JSON input"));

    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(onFailure).not.toHaveBeenCalled();
  });
});

describe("a refresh that belongs to a session that was replaced, not ended", () => {
  it("is stale when someone signs in over it with no sign-out between, and is not joined by the new session", async () => {
    // A's refresh is out; then the SSO callback signs the viewer in over the
    // live session — storeTokens with no clearTokens before it.
    storeTokens(authResponse(ADMIN, nearExpiry));
    const held = deferred<Response>();
    let refreshes = 0;
    server.routes[REFRESH] = () =>
      ++refreshes === 1 ? held.promise : json(authResponse(VIEWER));
    server.routes[X] = () => json({ ok: true });
    server.routes[Y] = (init) => json({ owner: callerOf(init) });
    const first = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });

    storeTokens(authResponse(VIEWER, nearExpiry));
    // The viewer's next request starts a refresh of its own rather than joining
    // the admin's, which would hand it the admin's answer.
    const next = settle(apiClient.get(apiPath`/api/v1/y`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(2);
    });
    expect(await next).toBe("sent");

    held.resolve(json(authResponse(ADMIN))); // the admin's answer, late
    expect(await first).toBeInstanceOf(StaleSessionError);
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
    expect(onRefresh).toHaveBeenCalledTimes(1); // the viewer's own
    expect(await apiClient.get(apiPath`/api/v1/y`)).toEqual({
      owner: VIEWER.id,
    });
  });

  it("is not joined, nor replaced by a refresh of its own, by a request made after the session ended with nobody signed in", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const held = deferred<Response>();
    let refreshes = 0;
    server.routes[REFRESH] = () =>
      ++refreshes === 1 ? held.promise : json({}, 401);
    server.routes[X] = () => json({ ok: true });
    server.routes[Y] = () =>
      json({ error: "unauthorized", message: "no session" }, 401);
    const first = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });

    clearTokens();
    const later = await settle(apiClient.get(apiPath`/api/v1/y`));
    // Nobody is signed in: it neither waits on the refresh that belongs to the
    // ended session nor starts one of its own (see "signed out" below).
    expect(later).toBeInstanceOf(ApiClientError); // an expired session
    expect(server.times(REFRESH)).toBe(1);

    held.resolve(json(authResponse(ADMIN)));
    expect(await first).toBeInstanceOf(StaleSessionError);
    expect(getStoredUser()).toBeNull();
  });
});

describe("a request that finds nobody signed in on a fresh page", () => {
  it("tries the refresh cookie, and fails as an expired session, not a stale one: its own failed refresh ended nothing it belonged to", async () => {
    const client = await pageLoad();
    server.routes[REFRESH] = () => json({}, 401);
    server.routes[X] = () =>
      json({ error: "unauthorized", message: "no session" }, 401);

    const err = await settle(client.apiClient.get(apiPath`/api/v1/x`));

    expect(err).toMatchObject({ name: "ApiClientError", status: 401 });
    // Its own refresh was refused, which ended the session it was resuming:
    // the 401 that follows has no second refresh to try.
    expect(server.times(REFRESH)).toBe(1);
    expect(onFailure).toHaveBeenCalled();
  });

  it("resumes the session off the refresh cookie, the one thing a fresh page can do with nobody signed in", async () => {
    const client = await pageLoad();
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    expect(await client.apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: ADMIN.id,
    });
    expect(server.times(REFRESH)).toBe(1);
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it("is not replayed for the user who signs in while it waits for that refresh", async () => {
    // The refresh cookie is the previous page's: it is held, and the next user
    // signs in before it answers. What the request waited for is not theirs.
    const client = await pageLoad();
    const held = deferred<Response>();
    let refreshes = 0;
    server.routes[REFRESH] = () =>
      ++refreshes === 1 ? held.promise : json(authResponse(VIEWER));
    const ACTION = "POST /api/v1/vms/x/action";
    server.routes[ACTION] = (init) =>
      callerOf(init) === ""
        ? json({ error: "unauthorized", message: "no token" }, 401)
        : json({ ranAs: callerOf(init) });
    const request = settle(
      client.apiClient.post(apiPath`/api/v1/vms/x/action`, {}),
    );
    await vi.waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });

    client.storeTokens(authResponse(VIEWER)); // the next user signs in
    held.resolve(json({}, 401));

    expect(await request).toBeInstanceOf(client.StaleSessionError);
    expect(server.times(ACTION)).toBe(0); // not sent, as nobody and not as them
  });
});

describe("a session that ended here", () => {
  // The refresh cookie outlives it when the server could not be told (the
  // logout request failed): nothing signed in here may use it to sign
  // someone back in.
  function sessionEndedButTheCookieStillRefreshes() {
    storeTokens(authResponse(ADMIN));
    clearTokens();
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = () =>
      json({ error: "unauthorized", message: "no session" }, 401);
  }

  it("is not resumed by a request made after it ended: not before it is sent, not after its 401", async () => {
    sessionEndedButTheCookieStillRefreshes();

    const err = await settle(apiClient.get(apiPath`/api/v1/x`));

    expect(err).toMatchObject({ name: "ApiClientError", status: 401 });
    // No refresh at all, so nothing was stored or reported for anyone.
    expect(server.times(REFRESH)).toBe(0);
    expect(onRefresh).not.toHaveBeenCalled();
    expect(getStoredUser()).toBeNull();
    // It was sent without a token (a public path may still answer it), once, and
    // is not retried: its 401 is the answer.
    expect(server.times(X)).toBe(1);
    // That is no session failing: nobody has one to end.
    expect(onFailure).not.toHaveBeenCalled();
  });

  it("is not resumed by the other requests that find nobody signed in either", async () => {
    sessionEndedButTheCookieStillRefreshes();

    const errs = await Promise.all(
      [1, 2, 3].map(() => settle(apiClient.get(apiPath`/api/v1/x`))),
    );

    for (const err of errs) expect(err).toBeInstanceOf(ApiClientError);
    expect(server.times(REFRESH)).toBe(0);
  });

  it("control: a session that begins afterwards refreshes as any other does, when it expires and when it rotates", async () => {
    sessionEndedButTheCookieStillRefreshes();
    storeTokens(authResponse(VIEWER)); // the next user signs in

    // Refused once (their token expired server-side), refreshed, and replayed.
    let reads = 0;
    server.routes[X] = (init) =>
      ++reads === 1
        ? json({ error: "unauthorized", message: "expired" }, 401)
        : json({ owner: callerOf(init) });
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: VIEWER.id,
    });
    expect(server.times(REFRESH)).toBe(1);

    // About to expire, so the next request rotates it first.
    storeTokens(authResponse(VIEWER, nearExpiry));
    await apiClient.get(apiPath`/api/v1/x`);
    expect(server.times(REFRESH)).toBe(2);
  });
});

describe("a refresh that names a different user than the one whose token it replaces", () => {
  // Another tab signed someone else in on the shared refresh cookie: this tab's
  // next refresh is answered for them. That is not a rotation of the session
  // this tab held — it begins one — and what was waiting on the refresh for
  // the old one is not theirs to be sent as.
  it("begins a new session, as a sign-in does: the epoch moves", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const before = currentSessionEpoch();
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    await settle(apiClient.get(apiPath`/api/v1/x`));

    expect(server.times(REFRESH)).toBe(1);
    expect(currentSessionEpoch()).toBeGreaterThan(before);
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it("does not send the request that waited on it as them", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    // The admin's request, held for a refresh that came back as the viewer.
    expect(await settle(apiClient.get(apiPath`/api/v1/x`))).toBeInstanceOf(
      StaleSessionError,
    );

    expect(server.times(X)).toBe(0);
    // The viewer's own request, made after it, goes out under their token.
    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: VIEWER.id,
    });
  });

  it("does not replay the request whose 401 started it as them", async () => {
    storeTokens(authResponse(ADMIN));
    let reads = 0;
    server.routes[X] = (init) =>
      ++reads === 1
        ? json({ error: "unauthorized", message: "expired" }, 401)
        : json({ owner: callerOf(init) });
    server.routes[REFRESH] = () => json(authResponse(VIEWER));

    expect(await settle(apiClient.get(apiPath`/api/v1/x`))).toBeInstanceOf(
      StaleSessionError,
    );

    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(X)).toBe(1); // sent for the admin, never replayed
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
    expect(onFailure).not.toHaveBeenCalled();
  });

  it("control: the same user's refresh is a rotation: the waiting request goes out as them", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const before = currentSessionEpoch();
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: ADMIN.id,
    });
    expect(currentSessionEpoch()).toBe(before);
  });
});

describe("a request whose retry was in flight when its session ended", () => {
  /** The admin's read finds its token refused, is refreshed, and its retry is held. */
  async function theRetryIsOut() {
    storeTokens(authResponse(ADMIN));
    const retry = deferred<Response>();
    let reads = 0;
    server.routes[X] = () =>
      ++reads === 1
        ? json({ error: "unauthorized", message: "expired" }, 401)
        : retry.promise;
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    await vi.waitFor(() => {
      expect(server.times(X)).toBe(2);
    });
    return { retry, request };
  }

  it("is reported as stale when the retry fails: the user who signed in since is not signed out", async () => {
    const { retry, request } = await theRetryIsOut();

    clearTokens();
    storeTokens(authResponse(VIEWER)); // the session changes hands
    retry.reject(new TypeError("Failed to fetch"));

    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
    // Their session stands: their next request goes out under their token.
    server.routes[Y] = (init) => json({ owner: callerOf(init) });
    expect(await apiClient.get(apiPath`/api/v1/y`)).toEqual({
      owner: VIEWER.id,
    });
  });

  it("control: a retry that fails in a session that is still current is an expired session", async () => {
    const { retry, request } = await theRetryIsOut();

    retry.reject(new TypeError("Failed to fetch"));

    expect(await request).toMatchObject({
      name: "ApiClientError",
      status: 401,
    });
    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(getStoredUser()).toBeNull();
  });
});

describe("a sign-in while localStorage refuses the cached user", () => {
  // The cached user only seeds the render after a reload. A full quota that
  // refused it must not fail the sign-in half way: storeTokens() writes the
  // token in memory and moves the epoch before it reaches localStorage, so a
  // throw there left a token behind for a session the caller then never applied.
  it("completes: the session is signed in, in memory, and what could not be cached is only the reload's", async () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("quota exceeded", "QuotaExceededError");
    });

    expect(() => {
      storeTokens(authResponse(ADMIN));
    }).not.toThrow();

    server.routes[X] = (init) => json({ owner: callerOf(init) });
    expect(await apiClient.get(apiPath`/api/v1/x`)).toEqual({
      owner: ADMIN.id,
    });
    expect(getStoredUser()).toBeNull();
  });
});

describe("sessionScope", () => {
  it("says the session it was taken in has ended once it has, and not when its tokens rotate", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const ended = sessionScope();
    expect(ended()).toBe(false);

    // A rotation: the same user, a new token.
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = () => json({ ok: true });
    await apiClient.get(apiPath`/api/v1/x`);
    expect(server.times(REFRESH)).toBe(1);
    expect(ended()).toBe(false);

    clearTokens();
    expect(ended()).toBe(true);
    // And it stays ended: even the same user signing in again is another session.
    storeTokens(authResponse(ADMIN));
    expect(ended()).toBe(true);
  });

  it("says so when it is first asked after the session ended, too", () => {
    storeTokens(authResponse(ADMIN));
    const ended = sessionScope();

    clearTokens();

    expect(ended()).toBe(true);
  });

  it("says a session that was replaced, with no sign-out between, has ended", () => {
    storeTokens(authResponse(ADMIN));
    const ended = sessionScope();

    storeTokens(authResponse(VIEWER));

    expect(ended()).toBe(true);
  });

  it("is judged against the session it was taken in, not the one before", () => {
    storeTokens(authResponse(ADMIN));
    clearTokens();
    storeTokens(authResponse(VIEWER));

    expect(sessionScope()()).toBe(false);
  });
});
