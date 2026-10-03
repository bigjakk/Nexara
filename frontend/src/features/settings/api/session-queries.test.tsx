import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import {
  apiClient,
  clearTokens,
  currentSessionEpoch,
  StaleSessionError,
} from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { queryClient } from "@/lib/query-client";
import {
  ADMIN,
  authResponse,
  callerOf,
  deferred,
  flush,
  installFakeServer,
  json,
  VIEWER,
  type FakeServer,
} from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import type { User, UserSession } from "@/types/api";
import { useAuthStore } from "@/stores/auth-store";
import { useRevokeSession } from "./session-queries";

/**
 * Revoking the session the user holds is a sign-out of the same shape as
 * logout(): the server ends the session, then clearAuth() ends it locally. A
 * token refresh still in flight must not be able to undo that.
 */

const REFRESH = "POST /api/v1/auth/refresh";
const LOGIN = "POST /api/v1/auth/login";

let server: FakeServer;

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

function session(overrides: Partial<UserSession> = {}): UserSession {
  return {
    id: "session-01",
    device_name: "",
    device_type: "web",
    user_agent: "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Firefox/130.0",
    ip_address: "192.0.2.10",
    created_at: "2026-01-01T00:00:00Z",
    last_used_at: "2026-01-01T00:00:00Z",
    expires_at: "2026-01-02T00:00:00Z",
    is_current: false,
    ...overrides,
  };
}

async function signInAs(user: User, permissions: string[] = []) {
  server.routes[LOGIN] = () => json(authResponse(user, { permissions }));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

async function adminIsSignedIn() {
  await signInAs(ADMIN, ["manage:user"]);
}

beforeEach(async () => {
  localStorage.clear();
  clearTokens();
  queryClient.clear();
  emptyPerSessionStores();
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
  server = installFakeServer();
  server.routes[REFRESH] = () => json({}, 401);
  await useAuthStore.getState().initialize();
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
  emptyPerSessionStores();
});

describe("useRevokeSession, revoking the session the user holds", () => {
  async function aRefreshIsInFlight() {
    await adminIsSignedIn();
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;
    server.routes["GET /api/v1/probe"] = () =>
      json({ error: "unauthorized", message: "expired" }, 401);
    const probe = apiClient.get(apiPath`/api/v1/probe`).then(
      () => "sent",
      (err: unknown) => err,
    );
    await waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    return { held, probe };
  }

  it("cannot be undone by a token refresh that was already in flight", async () => {
    const { held, probe } = await aRefreshIsInFlight();
    server.routes["DELETE /api/v1/auth/sessions/session-01"] = () =>
      new Response(null, { status: 204 });
    const { result } = renderHook(() => useRevokeSession(), { wrapper });

    act(() => {
      result.current.mutate(session({ id: "session-01", is_current: true }));
    });
    await waitFor(() => {
      expect(useAuthStore.getState().isAuthenticated).toBe(false);
    });

    held.resolve(json(authResponse(ADMIN, { permissions: ["manage:user"] })));
    await act(async () => {
      await probe;
    });
    await flush();

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().user).toBeNull();
    expect(localStorage.getItem("nexara_user")).toBeNull();
  });

  it("is a sign-out the user asked for", async () => {
    await adminIsSignedIn();
    server.routes["DELETE /api/v1/auth/sessions/session-01"] = () =>
      new Response(null, { status: 204 });
    const { result } = renderHook(() => useRevokeSession(), { wrapper });

    act(() => {
      result.current.mutate(session({ id: "session-01", is_current: true }));
    });
    await waitFor(() => {
      expect(useAuthStore.getState().isAuthenticated).toBe(false);
    });

    expect(useAuthStore.getState().signedOutByUser).toBe(true);
  });

  it("control: revoking another session leaves this one signed in", async () => {
    await adminIsSignedIn();
    server.routes["DELETE /api/v1/auth/sessions/session-02"] = () =>
      new Response(null, { status: 204 });
    const { result } = renderHook(() => useRevokeSession(), { wrapper });

    act(() => {
      result.current.mutate(session({ id: "session-02", is_current: false }));
    });
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });

    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(useAuthStore.getState().signedOutByUser).toBe(false);
  });
});

/** What a request settles as: the error it failed with, or "sent". */
function settled(request: Promise<unknown>) {
  return request.then(
    () => "sent",
    (err: unknown) => err,
  );
}

const CURRENT = "DELETE /api/v1/auth/sessions/session-01";
const WHOAMI = "GET /api/v1/whoami";

/** Whose token this tab's next request carries: "" for nobody's. */
async function tokenInUse(): Promise<string> {
  server.routes[WHOAMI] = (init) => json({ caller: callerOf(init) });
  const answer = await apiClient.get<{ caller: string }>(
    apiPath`/api/v1/whoami`,
  );
  return answer.caller;
}

/**
 * ADMIN is signed in, and has pressed Revoke on the session this tab holds: the
 * request is sent and held, and the flag that holds a refresh back mid-logout is
 * up. The hook stays mounted.
 */
async function revokeInFlight() {
  await adminIsSignedIn();
  const held = deferred<Response>();
  server.routes[CURRENT] = () => held.promise;
  const { result } = renderHook(() => useRevokeSession(), { wrapper });
  act(() => {
    result.current.mutate(session({ id: "session-01", is_current: true }));
  });
  await waitFor(() => {
    expect(server.times(CURRENT)).toBe(1);
  });
  return { result, held };
}

/** What the held revoke can be answered with once the session has gone. */
const ANSWERS: [
  name: string,
  answer: () => Response,
  outcome: "success" | "stale",
][] = [
  ["a late 204", () => new Response(null, { status: 204 }), "success"],
  [
    "a 401 that has gone stale",
    () => json({ error: "unauthorized", message: "expired" }, 401),
    "stale",
  ],
];

type Revoke = Awaited<ReturnType<typeof revokeInFlight>>["result"];

/** Waits for the revoke to settle as `outcome`, and checks it settled as that. */
async function settlesAs(result: Revoke, outcome: "success" | "stale") {
  if (outcome === "success") {
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
  } else {
    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });
    expect(result.current.error).toBeInstanceOf(StaleSessionError);
  }
  await flush();
}

// The revoke's callbacks are the mutation's own, so they run whenever its answer
// lands — after the session they were made in has ended, and someone else, or the
// same person again, is signed in. clearAuth would then end THEIR session over
// the revoke of one that is gone. Each case is paired with the same answer in the
// session the revoke was made in.
describe("useRevokeSession, answered after the session it was made in ended", () => {
  /**
   * The ways the session can have ended with someone adopted in this tab. None
   * leaves the flag the revoke raised up for it to find: clearAuth lowers it, and
   * so does a different identity beginning over the user held (adoptIdentity).
   */
  const HAND_OVERS: [name: string, handOver: () => Promise<void>][] = [
    [
      "an expiry and another user signing in",
      async () => {
        act(() => {
          useAuthStore.getState().clearAuth();
        });
        await signInAs(VIEWER);
      },
    ],
    [
      "a sign-out and the same user signing in again",
      async () => {
        act(() => {
          useAuthStore.getState().clearAuth();
        });
        await adminIsSignedIn();
      },
    ],
    [
      "another user signing in over the one held",
      async () => {
        await signInAs(VIEWER);
      },
    ],
  ];

  it("control: signs the user out, as one they asked for, when it is answered in the session it was made in", async () => {
    const { result, held } = await revokeInFlight();

    held.resolve(new Response(null, { status: 204 }));
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().signedOutByUser).toBe(true);
  });

  it.each(HAND_OVERS)(
    "does not sign out whoever is signed in since, after %s",
    async (_, handOver) => {
      const { result, held } = await revokeInFlight();
      await handOver();
      const signedInNow = useAuthStore.getState().user?.id;
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      // The premise: no flag is left for the revoke to find.
      expect(useAuthStore.getState().isLoggingOut).toBe(false);

      held.resolve(new Response(null, { status: 204 }));
      await waitFor(() => {
        expect(result.current.isSuccess).toBe(true);
      });
      await flush();

      // The revoke went through, and it was nothing to do with them.
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(useAuthStore.getState().user?.id).toBe(signedInNow);
      expect(useAuthStore.getState().signedOutByUser).toBe(false);
      // And it raised none: nothing is up to hold back the refreshes of whoever
      // it is.
      expect(useAuthStore.getState().isLoggingOut).toBe(false);
    },
  );

  it("control: lowers the flag that held a refresh back when it fails in the session it was made in", async () => {
    const { result, held } = await revokeInFlight();
    expect(useAuthStore.getState().isLoggingOut).toBe(true);

    held.resolve(json({ error: "bad_gateway", message: "no" }, 502));
    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });

    expect(useAuthStore.getState().isLoggingOut).toBe(false);
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
  });

  it.each(HAND_OVERS)(
    "signs out nobody, and raises no flag, when it fails after %s",
    async (_, handOver) => {
      const { result, held } = await revokeInFlight();
      await handOver();
      const signedInNow = useAuthStore.getState().user?.id;
      expect(useAuthStore.getState().isLoggingOut).toBe(false);

      held.resolve(json({ error: "bad_gateway", message: "no" }, 502));
      await waitFor(() => {
        expect(result.current.isError).toBe(true);
      });
      await flush();

      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(useAuthStore.getState().user?.id).toBe(signedInNow);
      expect(useAuthStore.getState().isLoggingOut).toBe(false);
    },
  );
});

// Another tab signed another user in on the shared refresh cookie, and this tab's
// refresh is answered for them while the revoke of its own current session is
// out. api-client moves its epoch and installs their token; auth-store's refresh
// callback drops the answer, because the revoke raised the flag that holds a
// refresh back mid-logout; and the store goes on showing the user who pressed
// Revoke, over the other user's token. The revoke then settles with its session
// gone, and nothing else would end that or lower the flag.
describe("useRevokeSession, when a refresh named another user while it was out", () => {
  /** This tab's token is refreshed, and the answer is for VIEWER. */
  async function refreshNamesAnotherUser() {
    server.routes[REFRESH] = () => json(authResponse(VIEWER));
    server.routes["GET /api/v1/probe"] = () =>
      json({ error: "unauthorized", message: "expired" }, 401);
    await act(async () => {
      await settled(apiClient.get(apiPath`/api/v1/probe`));
    });
  }

  /** The state the scenario is about, which a test checks before it settles the revoke. */
  async function theStoreShowsTheAuthorOverTheOtherUsersToken() {
    expect(useAuthStore.getState().user?.id).toBe(ADMIN.id);
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    // The answer was dropped by the flag, which is still up.
    expect(useAuthStore.getState().isLoggingOut).toBe(true);
    expect(await tokenInUse()).toBe(VIEWER.id);
  }

  it.each(ANSWERS)(
    "finishes the sign-out in this tab when the revoke settles as %s",
    async (_, answer, outcome) => {
      const { result, held } = await revokeInFlight();
      queryClient.setQueryData(["previous-user"], "cached-read");
      await refreshNamesAnotherUser();
      expect(server.times(REFRESH)).toBe(1);
      await theStoreShowsTheAuthorOverTheOtherUsersToken();

      held.resolve(answer());
      await settlesAs(result, outcome);

      // Not left showing ADMIN, their permissions and their data over VIEWER's token.
      expect(useAuthStore.getState().isAuthenticated).toBe(false);
      expect(useAuthStore.getState().user).toBeNull();
      expect(useAuthStore.getState().permissions).toEqual([]);
      expect(queryClient.getQueryData(["previous-user"])).toBeUndefined();
      // As a sign-out the user asked for, with no flag left up to hold back the
      // refreshes of whoever signs in next.
      expect(useAuthStore.getState().signedOutByUser).toBe(true);
      expect(useAuthStore.getState().isLoggingOut).toBe(false);
      // And the tab holds nobody's token: VIEWER's, which it had, is gone from it.
      expect(await tokenInUse()).toBe("");
    },
  );

  it.each(ANSWERS)(
    "control: leaves a user who was adopted since alone when the revoke settles as %s",
    async (_, answer, outcome) => {
      const { result, held } = await revokeInFlight();
      // The same hand-over, adopted this time: a sign-in over the user held.
      await signInAs(VIEWER);
      expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
      expect(await tokenInUse()).toBe(VIEWER.id);

      held.resolve(answer());
      await settlesAs(result, outcome);

      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
      expect(useAuthStore.getState().signedOutByUser).toBe(false);
      expect(useAuthStore.getState().isLoggingOut).toBe(false);
      expect(await tokenInUse()).toBe(VIEWER.id);
    },
  );

  it("control: a refresh that names the same user is a rotation, and the revoke stays on its normal path", async () => {
    const { result, held } = await revokeInFlight();
    let probed = 0;
    server.routes[REFRESH] = () =>
      json(authResponse(ADMIN, { permissions: ["manage:user"] }));
    server.routes["GET /api/v1/probe"] = () =>
      probed++ === 0
        ? json({ error: "unauthorized", message: "expired" }, 401)
        : json({ ok: true });
    await act(async () => {
      await settled(apiClient.get(apiPath`/api/v1/probe`));
    });
    expect(server.times(REFRESH)).toBe(1);
    // The flag held the answer back from the store, and the session is the same.
    const epoch = currentSessionEpoch();

    held.resolve(new Response(null, { status: 204 }));
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    await flush();

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().signedOutByUser).toBe(true);
    expect(useAuthStore.getState().isLoggingOut).toBe(false);
    // One sign-out, by the normal path: the epoch moved once, for it.
    expect(currentSessionEpoch()).toBe(epoch + 1);
  });
});

// The revoke hangs, the session is handed over to B, and B begins a sign-out of
// their own: the flag that is up is theirs, and the revoke did not raise it. It
// settles late and must leave it alone. Lowering it would turn a forced sign-out
// of B's, if their session is dead by then, into one the user did not ask for:
// the login URL would carry their last page to whoever signs in next.
describe("useRevokeSession, answered while somebody else's own sign-out is under way", () => {
  const LOGOUT = "POST /api/v1/auth/logout";

  /** The revoke held; B adopted over A; B's logout begun and held. */
  async function handedOverToSomeoneWhoBeginsToSignOut() {
    const revoke = await revokeInFlight();
    await signInAs(VIEWER);
    // Adopted: the flag the revoke raised was lowered as B began.
    expect(useAuthStore.getState().isLoggingOut).toBe(false);
    const loggingOut = deferred<Response>();
    server.routes[LOGOUT] = () => loggingOut.promise;
    let signOut!: Promise<void>;
    act(() => {
      signOut = useAuthStore.getState().logout();
    });
    await waitFor(() => {
      expect(server.times(LOGOUT)).toBe(1);
    });
    expect(useAuthStore.getState().isLoggingOut).toBe(true);
    return { ...revoke, loggingOut, signOut };
  }

  it.each(ANSWERS)(
    "leaves their flag, and them, alone when it settles as %s",
    async (_, answer, outcome) => {
      const { result, held, loggingOut, signOut } =
        await handedOverToSomeoneWhoBeginsToSignOut();

      held.resolve(answer());
      await settlesAs(result, outcome);

      // Theirs is still up, and they are still signed in, as they were.
      expect(useAuthStore.getState().isLoggingOut).toBe(true);
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);

      // Their session turns out to be dead, and the forced sign-out that finds
      // it reads the flag: it is the sign-out they asked for.
      act(() => {
        useAuthStore.getState().clearAuth();
      });
      expect(useAuthStore.getState().signedOutByUser).toBe(true);

      loggingOut.resolve(new Response(null, { status: 204 }));
      await act(async () => {
        await signOut;
      });
    },
  );
});

// TanStack types the context an onError is given as possibly absent, and an
// onSuccess's as always there, though it is there once the hook's onMutate has
// run. A revoke whose session cannot be told is left alone entirely, so the
// handlers are called here as a mutation that skipped onMutate would call them —
// and with each state a revoke that settled after its session ended can find the
// store in, one at a time. They are the handlers of the mutation a revoke of
// ANOTHER session left behind (which signs nobody out, so the cache holds it).
describe("useRevokeSession's handlers, called by hand", () => {
  const OTHER = "DELETE /api/v1/auth/sessions/session-02";
  const current = session({ id: "session-01", is_current: true });

  async function settledMutation() {
    await adminIsSignedIn();
    server.routes[OTHER] = () => new Response(null, { status: 204 });
    const { result } = renderHook(() => useRevokeSession(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync(
        session({ id: "session-02", is_current: false }),
      );
    });
    const mutation = queryClient.getMutationCache().getAll()[0];
    if (!mutation) throw new Error("the revoke left no mutation behind");
    return mutation;
  }

  const context = { client: queryClient, meta: undefined };
  type Settled = Awaited<ReturnType<typeof settledMutation>>;
  type Made = { ended: () => boolean; by: string | null } | undefined;

  const made = (
    over: Partial<{ ended: () => boolean; by: string | null }>,
  ) => ({
    ended: () => true,
    by: ADMIN.id as string | null,
    ...over,
  });

  /** Both handlers, called as the settle of a revoke of `session`. */
  const HANDLERS: [
    name: string,
    call: (mutation: Settled, made: Made, of?: UserSession) => unknown,
  ][] = [
    [
      "a success",
      (mutation, ctx, of = current) =>
        mutation.options.onSuccess?.(undefined, of, ctx, context),
    ],
    [
      "a failure",
      (mutation, ctx, of = current) =>
        mutation.options.onError?.(new Error("no"), of, ctx, context),
    ],
  ];

  async function run(call: () => unknown) {
    await act(async () => {
      await call();
    });
  }

  it("control: a success signs the user out of the session it revoked, while the session it is told of goes on", async () => {
    const mutation = await settledMutation();

    await run(() =>
      mutation.options.onSuccess?.(
        undefined,
        current,
        made({ ended: () => false }),
        context,
      ),
    );

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });

  it("control: a failure lowers the flag, while the session it is told of goes on", async () => {
    const mutation = await settledMutation();
    useAuthStore.setState({ isLoggingOut: true });

    await run(() =>
      mutation.options.onError?.(
        new Error("no"),
        current,
        made({ ended: () => false }),
        context,
      ),
    );

    expect(useAuthStore.getState().isLoggingOut).toBe(false);
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
  });

  it.each(HANDLERS)(
    "leaves a revoke whose session it is not told of alone entirely: %s",
    async (_, call) => {
      const mutation = await settledMutation();
      useAuthStore.setState({ isLoggingOut: true });
      const epoch = currentSessionEpoch();

      await run(() => call(mutation, undefined));

      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(useAuthStore.getState().isLoggingOut).toBe(true);
      expect(currentSessionEpoch()).toBe(epoch);
    },
  );

  describe("for a session that has ended", () => {
    it.each(HANDLERS)(
      "finishes the sign-out when the store still shows the author, signed in, flag up: %s",
      async (_, call) => {
        const mutation = await settledMutation();
        queryClient.setQueryData(["previous-user"], "cached-read");
        useAuthStore.setState({ isLoggingOut: true });

        await run(() => call(mutation, made({ by: ADMIN.id })));

        expect(useAuthStore.getState().isAuthenticated).toBe(false);
        expect(useAuthStore.getState().user).toBeNull();
        expect(useAuthStore.getState().signedOutByUser).toBe(true);
        expect(useAuthStore.getState().isLoggingOut).toBe(false);
        expect(queryClient.getQueryData(["previous-user"])).toBeUndefined();
      },
    );

    it.each(HANDLERS)(
      "leaves someone other than the author alone, the flag they raised included: %s",
      async (_, call) => {
        const mutation = await settledMutation();
        // ADMIN is shown and has begun a sign-out of their own; the revoke was
        // VIEWER's, and did not raise this flag.
        useAuthStore.setState({ isLoggingOut: true });
        const epoch = currentSessionEpoch();

        await run(() => call(mutation, made({ by: VIEWER.id })));

        expect(useAuthStore.getState().isAuthenticated).toBe(true);
        expect(useAuthStore.getState().user?.id).toBe(ADMIN.id);
        expect(useAuthStore.getState().isLoggingOut).toBe(true);
        expect(currentSessionEpoch()).toBe(epoch);
      },
    );

    it.each(HANDLERS)(
      "does nothing when the store already says nobody is signed in: %s",
      async (_, call) => {
        const mutation = await settledMutation();
        useAuthStore.setState({ isAuthenticated: false, isLoggingOut: true });
        const epoch = currentSessionEpoch();

        await run(() => call(mutation, made({ by: ADMIN.id })));

        // Signed nobody out again, and lowered nothing.
        expect(useAuthStore.getState().isLoggingOut).toBe(true);
        expect(currentSessionEpoch()).toBe(epoch);
      },
    );

    it.each(HANDLERS)(
      "finishes the sign-out of a user who signed in again and has begun another: %s",
      async (_, call) => {
        const mutation = await settledMutation();
        // The same user, in a session of their own now, who has pressed Sign out:
        // the flag that is up is theirs, and it is the same sign-out.
        act(() => {
          useAuthStore.getState().clearAuth();
        });
        await adminIsSignedIn();
        useAuthStore.setState({ isLoggingOut: true });
        const epoch = currentSessionEpoch();

        await run(() => call(mutation, made({ by: ADMIN.id })));

        expect(useAuthStore.getState().isAuthenticated).toBe(false);
        expect(useAuthStore.getState().signedOutByUser).toBe(true);
        expect(useAuthStore.getState().isLoggingOut).toBe(false);
        expect(currentSessionEpoch()).toBeGreaterThan(epoch);
      },
    );

    it.each(HANDLERS)(
      "does nothing with the flag down, even for the same user: %s",
      async (_, call) => {
        const mutation = await settledMutation();
        // The same user, signed in again: a session of its own, flag down.
        expect(useAuthStore.getState().isLoggingOut).toBe(false);
        const epoch = currentSessionEpoch();

        await run(() => call(mutation, made({ by: ADMIN.id })));

        expect(useAuthStore.getState().isAuthenticated).toBe(true);
        expect(useAuthStore.getState().isLoggingOut).toBe(false);
        expect(currentSessionEpoch()).toBe(epoch);
      },
    );

    it.each(HANDLERS)(
      "does nothing for a revoke of another session, whatever the flag: %s",
      async (_, call) => {
        const mutation = await settledMutation();
        // Raised by something else: a revoke of another session raises none.
        useAuthStore.setState({ isLoggingOut: true });
        const epoch = currentSessionEpoch();

        await run(() =>
          call(
            mutation,
            made({ by: ADMIN.id }),
            session({ id: "session-02", is_current: false }),
          ),
        );

        expect(useAuthStore.getState().isAuthenticated).toBe(true);
        expect(useAuthStore.getState().isLoggingOut).toBe(true);
        expect(currentSessionEpoch()).toBe(epoch);
      },
    );
  });
});
