import { describe, expect, it, vi } from "vitest";
import {
  ADMIN,
  VIEWER,
  authResponse,
  callerOf,
  deferred,
  flush,
  json,
} from "@/test/fake-server";
import {
  REFRESH,
  X,
  Y,
  expired,
  getX,
  nearExpiry,
  onFailure,
  onRefresh,
  pageLoad,
  readX,
  server,
  settle,
  untilSent,
  installApiClientHarness,
} from "@/test/api-client-harness";
import {
  ApiClientError,
  apiClient,
  apiFetch,
  clearTokens,
  currentSessionEpoch,
  getStoredUser,
  openApiRequest,
  sessionScope,
  StaleSessionError,
  storeTokens,
} from "./api-client";
import { apiPath } from "./api-path";

/**
 * Which session a refresh, and a request, belongs to (lib/api-client.ts, the
 * session epoch): clearTokens() ends one, storeTokens() begins one, a refresh
 * rotates the tokens of its own and is neither. Without it a refresh landing
 * after Sign out signed the user back in, or replaced the next user's token.
 */

installApiClientHarness();

type Held = ReturnType<typeof deferred<Response>>;

describe("the session epoch", () => {
  it("is bumped when a session begins and when it ends, and not by a rotation of its tokens", async () => {
    const before = currentSessionEpoch();
    storeTokens(authResponse(ADMIN, nearExpiry));
    const began = currentSessionEpoch();
    expect(began).toBeGreaterThan(before);

    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = () => json({ ok: true });
    await getX();
    expect(server.times(REFRESH)).toBe(1);
    expect(currentSessionEpoch()).toBe(began);

    clearTokens();
    expect(currentSessionEpoch()).toBeGreaterThan(began);
  });
});

describe("a refresh answered after the session that asked for it ended", () => {
  /** A request waiting on a refresh that the test holds back. */
  async function requestWaitingOnARefresh() {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;
    server.routes[X] = () => json({ ok: true });
    const request = readX();
    await untilSent(REFRESH);
    return { held, request };
  }

  it("is dropped: nothing is stored, no callback runs, and the request waiting on it is not sent", async () => {
    const { held, request } = await requestWaitingOnARefresh();

    clearTokens(); // the session ends while the refresh is out
    held.resolve(json(authResponse(ADMIN)));

    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(getStoredUser()).toBeNull();
    expect(onRefresh).not.toHaveBeenCalled();
    expect(onFailure).not.toHaveBeenCalled();
    // It belonged to the ended session: not sent for whoever is in by now.
    expect(server.times(X)).toBe(0);
  });

  it.each<[string, (held: Held) => void]>([
    [
      "answered for the ended session",
      (held) => {
        held.resolve(json(authResponse(ADMIN)));
      },
    ],
    [
      "refused as a dead refresh token",
      (held) => {
        held.resolve(json({}, 401));
      },
    ],
    [
      "failed by the network",
      (held) => {
        held.reject(new TypeError("Failed to fetch"));
      },
    ],
  ])(
    "leaves the user who signed in since alone when the ended session's refresh is %s",
    async (_name, answer) => {
      const { held, request } = await requestWaitingOnARefresh();
      clearTokens();
      storeTokens(authResponse(VIEWER)); // the next user signs in

      answer(held);

      // The ended session's, and not a failure or a rotation of the current one.
      expect(await request).toBeInstanceOf(StaleSessionError);
      expect(onRefresh).not.toHaveBeenCalled();
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
      server.routes[Y] = (init) => json({ owner: callerOf(init) });
      expect(await apiClient.get(apiPath`/api/v1/y`)).toEqual({
        owner: VIEWER.id,
      });
    },
  );

  it("control: the same refresh, answered while its session is still current, rotates the tokens", async () => {
    const { held, request } = await requestWaitingOnARefresh();

    held.resolve(json(authResponse(ADMIN, { permissions: ["view:cluster"] })));

    expect(await request).toBe("sent");
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(server.times(X)).toBe(1);
    expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
  });
});

describe("a refresh that was joined", () => {
  it("is not forgotten by the one that ended first, so a newer session's refresh is still shared", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const first = deferred<Response>();
    const second = deferred<Response>();
    let refreshes = 0;
    server.routes[REFRESH] = () =>
      ++refreshes === 1 ? first.promise : second.promise;
    server.routes[X] = () => json({ ok: true });
    server.routes[Y] = () => json({ ok: true });
    const ended = readX();
    await untilSent(REFRESH);

    clearTokens();
    storeTokens(authResponse(VIEWER, nearExpiry));
    const viewers = settle(apiClient.get(apiPath`/api/v1/y`));
    await untilSent(REFRESH, 2);

    // The first one's refresh settles (stale) and must not have cleared the
    // second's from under it: a request made now joins it, not a third.
    first.resolve(json(authResponse(ADMIN)));
    expect(await ended).toBeInstanceOf(StaleSessionError);
    const joined = settle(apiClient.get(apiPath`/api/v1/y`));
    await flush();
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
    const request = readX();
    await untilSent(X);

    clearTokens();
    storeTokens(authResponse(VIEWER));
    answer.resolve(expired());

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
      ++reads === 1 ? expired() : json({ owner: callerOf(init) });
    server.routes[REFRESH] = () => json(authResponse(ADMIN));

    expect(await getX()).toEqual({ owner: ADMIN.id });
    expect(server.times(REFRESH)).toBe(1);
    expect(server.times(X)).toBe(2);
  });
});

describe("a refresh whose answer is only partly in when its session ends", () => {
  /** A refresh whose headers are in and whose body is held back. */
  async function requestWaitingOnABody() {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const body = deferred<unknown>();
    server.routes[REFRESH] = () =>
      ({
        ok: true,
        status: 200,
        json: () => body.promise,
      }) as unknown as Response;
    server.routes[X] = () => json({ ok: true });
    const request = readX();
    await untilSent(REFRESH);
    await flush(); // the headers are in; json() is awaited
    return { body, request };
  }

  it("is dropped when its body arrives after the session ended", async () => {
    const { body, request } = await requestWaitingOnABody();

    clearTokens();
    body.resolve(authResponse(ADMIN));

    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(getStoredUser()).toBeNull();
    expect(onRefresh).not.toHaveBeenCalled();
    expect(server.times(X)).toBe(0);
  });

  it("is reported as stale, not as a parse error, when its unreadable body arrives after the session ended", async () => {
    const { body, request } = await requestWaitingOnABody();

    clearTokens();
    storeTokens(authResponse(VIEWER));
    body.reject(new SyntaxError("Unexpected end of JSON input"));

    expect(await request).toBeInstanceOf(StaleSessionError);
    expect(onFailure).not.toHaveBeenCalled();
  });
});

describe("a refresh that belongs to a session that was replaced, not ended", () => {
  it("is stale when someone signs in over it with no sign-out between, and is not joined by the new session", async () => {
    // The SSO callback signs the viewer in over the live session: storeTokens
    // with no clearTokens before it.
    storeTokens(authResponse(ADMIN, nearExpiry));
    const held = deferred<Response>();
    let refreshes = 0;
    server.routes[REFRESH] = () =>
      ++refreshes === 1 ? held.promise : json(authResponse(VIEWER));
    server.routes[X] = () => json({ ok: true });
    server.routes[Y] = (init) => json({ owner: callerOf(init) });
    const first = readX();
    await untilSent(REFRESH);

    // The viewer's request refreshes on its own: joining the admin's would hand
    // it the admin's answer.
    storeTokens(authResponse(VIEWER, nearExpiry));
    const next = settle(apiClient.get(apiPath`/api/v1/y`));
    await untilSent(REFRESH, 2);
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
    const first = readX();
    await untilSent(REFRESH);

    clearTokens();
    // Nobody is signed in: it neither waits on that refresh nor starts one.
    const later = await settle(apiClient.get(apiPath`/api/v1/y`));
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

    const err = await readX(client);

    expect(err).toMatchObject({ name: "ApiClientError", status: 401 });
    // Its own refresh was refused, which ended the session it was resuming: the
    // 401 that follows has no second refresh to try.
    expect(server.times(REFRESH)).toBe(1);
    expect(onFailure).toHaveBeenCalled();
  });

  it("resumes the session off the refresh cookie, the one thing a fresh page can do with nobody signed in", async () => {
    const client = await pageLoad();
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    expect(await getX(client)).toEqual({ owner: ADMIN.id });
    expect(server.times(REFRESH)).toBe(1);
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it("is not replayed for the user who signs in while it waits for that refresh", async () => {
    // The refresh cookie is the previous page's, held; the next user signs in
    // before it answers. What the request waited for is not theirs.
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
    await untilSent(REFRESH);

    client.storeTokens(authResponse(VIEWER)); // the next user signs in
    held.resolve(json({}, 401));

    expect(await request).toBeInstanceOf(client.StaleSessionError);
    expect(server.times(ACTION)).toBe(0); // not sent, as nobody and not as them
  });
});

describe("a session that ended here", () => {
  // The refresh cookie outlives it when the server could not be told (the logout
  // request failed): nothing signed in here may use it to sign someone back in.
  function sessionEndedButTheCookieStillRefreshes() {
    storeTokens(authResponse(ADMIN));
    clearTokens();
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = () =>
      json({ error: "unauthorized", message: "no session" }, 401);
  }

  it("is not resumed by a request made after it ended: not before it is sent, not after its 401", async () => {
    sessionEndedButTheCookieStillRefreshes();

    const err = await readX();

    expect(err).toMatchObject({ name: "ApiClientError", status: 401 });
    expect(server.times(REFRESH)).toBe(0);
    expect(onRefresh).not.toHaveBeenCalled();
    expect(getStoredUser()).toBeNull();
    // Sent without a token (a public path may answer it), once: its 401 is the
    // answer, and no session failed.
    expect(server.times(X)).toBe(1);
    expect(onFailure).not.toHaveBeenCalled();
  });

  it("is not resumed by the other requests that find nobody signed in either", async () => {
    sessionEndedButTheCookieStillRefreshes();

    const errs = await Promise.all([1, 2, 3].map(() => readX()));

    for (const err of errs) expect(err).toBeInstanceOf(ApiClientError);
    expect(server.times(REFRESH)).toBe(0);
  });

  it("control: a session that begins afterwards refreshes as any other does, when it expires and when it rotates", async () => {
    sessionEndedButTheCookieStillRefreshes();
    storeTokens(authResponse(VIEWER)); // the next user signs in

    let reads = 0;
    server.routes[X] = (init) =>
      ++reads === 1 ? expired() : json({ owner: callerOf(init) });
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    expect(await getX()).toEqual({ owner: VIEWER.id });
    expect(server.times(REFRESH)).toBe(1);

    storeTokens(authResponse(VIEWER, nearExpiry));
    await getX();
    expect(server.times(REFRESH)).toBe(2);
  });
});

describe("a refresh that names a different user than the one whose token it replaces", () => {
  // Another tab signed someone else in on the shared refresh cookie. That is not
  // a rotation of the session this tab held: it begins one, and what was waiting
  // on the refresh for the old one is not theirs to be sent as.
  it("begins a new session, as a sign-in does: the epoch moves", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const before = currentSessionEpoch();
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    await readX();

    expect(server.times(REFRESH)).toBe(1);
    expect(currentSessionEpoch()).toBeGreaterThan(before);
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it("does not send the request that waited on it as them", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    expect(await readX()).toBeInstanceOf(StaleSessionError);

    expect(server.times(X)).toBe(0);
    expect(await getX()).toEqual({ owner: VIEWER.id });
  });

  it("does not replay the request whose 401 started it as them", async () => {
    storeTokens(authResponse(ADMIN));
    let reads = 0;
    server.routes[X] = (init) =>
      ++reads === 1 ? expired() : json({ owner: callerOf(init) });
    server.routes[REFRESH] = () => json(authResponse(VIEWER));

    expect(await readX()).toBeInstanceOf(StaleSessionError);

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

    expect(await getX()).toEqual({ owner: ADMIN.id });
    expect(currentSessionEpoch()).toBe(before);
  });
});

describe("a request whose retry was in flight when its session ended", () => {
  /** The admin's read finds its token refused, is refreshed, and its retry is held. */
  async function theRetryIsOut() {
    storeTokens(authResponse(ADMIN));
    const retry = deferred<Response>();
    let reads = 0;
    server.routes[X] = () => (++reads === 1 ? expired() : retry.promise);
    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    const request = readX();
    await untilSent(X, 2);
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

  it("control: a retry that fails in a session that is still current is that request's own failure, and the session stands", async () => {
    const { retry, request } = await theRetryIsOut();
    const failure = new TypeError("Failed to fetch");

    retry.reject(failure);

    // The network failed, not the session: the refresh before the retry was good.
    expect(await request).toBe(failure);
    expect(onFailure).not.toHaveBeenCalled();
    expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
    server.routes[Y] = (init) => json({ owner: callerOf(init) });
    expect(await apiClient.get(apiPath`/api/v1/y`)).toEqual({
      owner: ADMIN.id,
    });
  });
});

describe("a request whose token was chosen just as its session ended", () => {
  // ensureValidToken returns the token held and its caller reads the session
  // again some microtasks later; a session changing hands between them left the
  // old user's token on a request that took its epoch AFTER, whose 401 was taken
  // for the new user's and replayed as them. It makes one Date.now() call right
  // before it returns, so a spy on it queues the sign-in `hops` turns of the
  // microtask queue after that.
  function theSessionChangesAsTheTokenIsChosen(hops = 0) {
    const real = Date.now.bind(Date);
    let fired = false;
    const later = (n: number, change: () => void) => {
      if (n <= 0) change();
      else
        queueMicrotask(() => {
          later(n - 1, change);
        });
    };
    vi.spyOn(Date, "now").mockImplementation(() => {
      if (!fired) {
        fired = true;
        queueMicrotask(() => {
          later(hops, () => {
            storeTokens(authResponse(VIEWER));
          });
        });
      }
      return real();
    });
  }

  // Wherever in the queue the sign-in lands, the admin's request is never sent or
  // replayed as the viewer: up to the turn in which request() resumes it is not
  // sent at all (hops 0, and 1 for the turn between the check and the
  // resumption), and from the one after it is sent as the admin and dropped at
  // its 401.
  it.each([0, 1, 2, 3, 4, 5, 6, 7, 8])(
    "is never sent or replayed as the viewer, whichever turn of the queue the sign-in lands in: %i microtasks on",
    async (hops) => {
      storeTokens(authResponse(ADMIN));
      const callers: string[] = [];
      server.routes[X] = (init) => {
        callers.push(callerOf(init));
        return callerOf(init) === VIEWER.id
          ? json({ owner: VIEWER.id })
          : expired();
      };
      // A refresh that cannot be made, so that nothing but a replay as the
      // viewer could get the admin's request an answer.
      server.routes[REFRESH] = () => json({ error: "x", message: "down" }, 503);
      theSessionChangesAsTheTokenIsChosen(hops);

      const outcome = await readX();

      expect(callers).not.toContain(VIEWER.id);
      expect(outcome).toBeInstanceOf(StaleSessionError);
      expect(callers).toEqual(hops <= 1 ? [] : [ADMIN.id]);
      expect(onFailure).not.toHaveBeenCalled();
      expect(getStoredUser()).toMatchObject({ id: VIEWER.id }); // theirs began
    },
  );

  // apiFetch and openApiRequest make the same in-turn epoch check as request()
  // (tokenForRequest); the rows above do not reach their code, so hops 0 and 1
  // repeat here.
  describe.each([0, 1])(
    "when the sign-in lands %i microtasks after the check",
    (hops) => {
      it("apiFetch does not send", async () => {
        storeTokens(authResponse(ADMIN));
        server.routes[X] = () => json({ ok: true });
        theSessionChangesAsTheTokenIsChosen(hops);

        expect(await settle(apiFetch(apiPath`/api/v1/x`))).toBeInstanceOf(
          StaleSessionError,
        );
        expect(server.times(X)).toBe(0);
      });

      it("openApiRequest is not given a token", async () => {
        storeTokens(authResponse(ADMIN));
        vi.spyOn(XMLHttpRequest.prototype, "open").mockImplementation(
          () => undefined,
        );
        const header = vi
          .spyOn(XMLHttpRequest.prototype, "setRequestHeader")
          .mockImplementation(() => undefined);
        theSessionChangesAsTheTokenIsChosen(hops);

        expect(
          await settle(openApiRequest("POST", apiPath`/api/v1/x`)),
        ).toBeInstanceOf(StaleSessionError);
        expect(header).not.toHaveBeenCalled();
      });
    },
  );

  it("control: with no change of session, the same request is sent under its token", async () => {
    storeTokens(authResponse(ADMIN));
    server.routes[X] = (init) => json({ owner: callerOf(init) });

    expect(await getX()).toEqual({ owner: ADMIN.id });
    expect(server.times(X)).toBe(1);
  });
});

describe("a request that was given no token because nobody was signed in", () => {
  // The harness's clearTokens() latched the module: the request is chosen to go
  // out as nobody, and a user who signs in as it is chosen must not find it
  // carried into their session, meet a 401 there, and be replayed as them
  // (tokenForRequest).
  it.each([
    ["a few microtasks after it was chosen", false],
    ["before the choice is looked at", true],
  ])(
    "is not carried into the session of the user who signs in %s",
    async (_name, signInFirst) => {
      server.routes[X] = (init) =>
        callerOf(init) === ""
          ? json({ error: "unauthorized", message: "no token" }, 401)
          : json({ owner: callerOf(init) });
      const signIn = () => {
        queueMicrotask(() => {
          storeTokens(authResponse(VIEWER));
        });
      };

      if (signInFirst) signIn();
      const outcome = readX();
      if (!signInFirst) signIn();

      expect(await outcome).toBeInstanceOf(StaleSessionError);
      expect(server.times(X)).toBe(0);
    },
  );

  it("is not sent by apiFetch or openApiRequest then either", async () => {
    server.routes[X] = () => json({ ok: true });
    vi.spyOn(XMLHttpRequest.prototype, "open").mockImplementation(
      () => undefined,
    );
    vi.spyOn(XMLHttpRequest.prototype, "setRequestHeader").mockImplementation(
      () => undefined,
    );

    const fetched = settle(apiFetch(apiPath`/api/v1/x`));
    queueMicrotask(() => {
      storeTokens(authResponse(VIEWER));
    });
    expect(await fetched).toBeInstanceOf(StaleSessionError);
    expect(server.times(X)).toBe(0);

    clearTokens(); // nobody again
    const opened = settle(openApiRequest("POST", apiPath`/api/v1/x`));
    queueMicrotask(() => {
      storeTokens(authResponse(VIEWER));
    });
    expect(await opened).toBeInstanceOf(StaleSessionError);
  });

  it("control: with nobody signing in, it goes out as nobody and is told its session expired", async () => {
    server.routes[X] = () =>
      json({ error: "unauthorized", message: "no token" }, 401);

    expect(await readX()).toMatchObject({
      name: "ApiClientError",
      status: 401,
      message: "Session expired",
    });
    expect(server.times(X)).toBe(1);
  });
});

describe("a sign-in while localStorage refuses the cached user", () => {
  // storeTokens() puts the token in memory and moves the epoch before it reaches
  // localStorage, so a full quota throwing there left a token behind for a
  // session the caller then never applied.
  it("completes: the session is signed in, in memory, and what could not be cached is only the reload's", async () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("quota exceeded", "QuotaExceededError");
    });

    expect(() => {
      storeTokens(authResponse(ADMIN));
    }).not.toThrow();

    server.routes[X] = (init) => json({ owner: callerOf(init) });
    expect(await getX()).toEqual({ owner: ADMIN.id });
    expect(getStoredUser()).toBeNull();
  });
});

describe("clearTokens and the user stored for the browser (nexara_user)", () => {
  // Every tab reads the record, and a sign-in or a rotation of the cookie writes
  // it, so it names whose session the shared refresh cookie belongs to. A tab
  // whose own session ends takes it with it, but not when it names someone else:
  // that is another tab's live session, which a reload of that tab resumes off.
  const record = (user: object) => {
    localStorage.setItem("nexara_user", JSON.stringify(user));
  };

  it("leaves the record of another user's session alone when this tab's own ends", async () => {
    storeTokens(authResponse(ADMIN));
    record(VIEWER); // another tab signs in
    const epoch = currentSessionEpoch();

    clearTokens();

    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });
    // The session ended all the same: a request goes out as nobody, no refresh.
    expect(currentSessionEpoch()).not.toBe(epoch);
    server.routes[X] = (init) => json({ owner: callerOf(init) });
    expect(await getX()).toEqual({ owner: "" });
    expect(server.times(REFRESH)).toBe(0);
  });

  it.each<[string, () => void, object]>([
    [
      "it is this tab's own",
      () => {
        storeTokens(authResponse(ADMIN));
      },
      { id: ADMIN.id },
    ],
    [
      "this tab holds no token: the boot's record, or a resume that failed",
      () => {
        record(VIEWER);
      },
      { id: VIEWER.id },
    ],
    [
      "it names no one, as a corrupt value does: it is no session's",
      () => {
        storeTokens(authResponse(ADMIN));
        record({ email: "x" });
      },
      { email: "x" },
    ],
  ])("control: removes the record when %s", (_name, setUp, stored) => {
    setUp();
    expect(getStoredUser()).toMatchObject(stored);

    clearTokens();

    expect(getStoredUser()).toBeNull();
  });

  it("control: removes it when the session ends by expiry: a refresh the server refused", async () => {
    storeTokens(authResponse(ADMIN, { expiresIn: -10 }));
    server.routes[REFRESH] = () => json({}, 401);

    await readX();

    expect(onFailure).toHaveBeenCalledTimes(1);
    expect(getStoredUser()).toBeNull();
  });

  it("control: a refresh that answered for another user makes the record this tab's: the sign-out that follows removes it", async () => {
    storeTokens(authResponse(ADMIN, { expiresIn: -10 }));
    // The cookie in the jar is the viewer's now, and the answer is theirs: a
    // session begins in this tab, and the record names it.
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    await readX();
    expect(getStoredUser()).toMatchObject({ id: VIEWER.id });

    clearTokens();

    expect(getStoredUser()).toBeNull();
  });
});

describe("sessionScope", () => {
  it("says the session it was taken in has ended once it has, and not when its tokens rotate", async () => {
    storeTokens(authResponse(ADMIN, nearExpiry));
    const ended = sessionScope();
    expect(ended()).toBe(false);

    server.routes[REFRESH] = () => json(authResponse(ADMIN));
    server.routes[X] = () => json({ ok: true });
    await getX();
    expect(server.times(REFRESH)).toBe(1);
    expect(ended()).toBe(false);

    clearTokens();
    expect(ended()).toBe(true);
    // And it stays ended: even the same user signing in again is another session.
    storeTokens(authResponse(ADMIN));
    expect(ended()).toBe(true);
  });

  it.each([
    [
      "a sign-out, when it is first asked after",
      () => {
        clearTokens();
      },
    ],
    [
      "a session that replaced it with no sign-out between",
      () => {
        storeTokens(authResponse(VIEWER));
      },
    ],
  ])("says it has ended after %s", (_name, end) => {
    storeTokens(authResponse(ADMIN));
    const ended = sessionScope();

    end();

    expect(ended()).toBe(true);
  });

  it("is judged against the session it was taken in, not the one before", () => {
    storeTokens(authResponse(ADMIN));
    clearTokens();
    storeTokens(authResponse(VIEWER));

    expect(sessionScope()()).toBe(false);
  });
});
