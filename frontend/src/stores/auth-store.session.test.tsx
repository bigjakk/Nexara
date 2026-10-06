import { describe, expect, it, vi } from "vitest";
import { act, render, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { OIDCCallbackPage } from "@/features/auth/pages/OIDCCallbackPage";
import { apiClient, getStoredUser, StaleSessionError } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { queryClient } from "@/lib/query-client";
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
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import {
  LOGOUT,
  REFRESH,
  REGISTER,
  TOTP,
  nearExpiry,
  server,
  settle,
  installAuthStoreHarness,
} from "@/test/api-client-harness";
import type { User } from "@/types/api";
import { useAuthStore } from "./auth-store";
import { usePBSKeyStore } from "./pbs-key-store";

/**
 * A session that ended, or changed hands, while something of it was still on its
 * way: through the real auth store and api-client, with only fetch replaced.
 */

const { signInAs } = installAuthStoreHarness({
  store: useAuthStore,
  act,
  reset: () => {
    queryClient.clear();
    emptyPerSessionStores();
    usePBSKeyStore.setState({ pending: [] });
  },
});

async function signsOut() {
  server.routes[LOGOUT] = () => new Response(null, { status: 204 });
  await act(async () => {
    await useAuthStore.getState().logout();
  });
}

/** The stored user, which the shared cookie's tabs read. */
const storedUserId = () =>
  (JSON.parse(localStorage.getItem("nexara_user") ?? "{}") as User).id;

/** A read as whoever holds the token, to show whose token is in use. */
async function readerOwner() {
  server.routes["GET /api/v1/reader"] = (init) =>
    json({ owner: callerOf(init) });
  return apiClient.get(apiPath`/api/v1/reader`);
}

describe("a token refresh still in flight when the session ends", () => {
  /** The admin is signed in, and a read finds its token refused: that starts a refresh, held back. */
  async function aRefreshIsInFlight() {
    await signInAs(ADMIN, { permissions: ["manage:user"] });
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;
    server.routes["GET /api/v1/probe"] = () =>
      json({ error: "unauthorized", message: "expired" }, 401);
    const probe = settle(apiClient.get(apiPath`/api/v1/probe`));
    await waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    return { held, probe };
  }

  it("(a) cannot sign the user back in once they have pressed Sign out", async () => {
    const { held, probe } = await aRefreshIsInFlight();
    await signsOut();
    expect(useAuthStore.getState().isAuthenticated).toBe(false);

    held.resolve(json(authResponse(ADMIN, { permissions: ["manage:user"] })));
    await act(async () => {
      await probe;
    });
    await flush();

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().user).toBeNull();
    expect(useAuthStore.getState().permissions).toEqual([]);
    expect(localStorage.getItem("nexara_user")).toBeNull();
  });

  it.each<[string, (held: ReturnType<typeof deferred<Response>>) => void]>([
    [
      "(b) cannot take over the session of the user who signed in since",
      (held) => {
        held.resolve(
          json(authResponse(ADMIN, { permissions: ["manage:user"] })),
        );
      },
    ],
    [
      "(c) cannot end the session of the user who signed in since, by failing",
      (held) => {
        held.resolve(json({}, 401)); // the ended session's refresh token is refused
      },
    ],
    [
      "(c') nor by the network failing",
      (held) => {
        held.reject(new TypeError("Failed to fetch"));
      },
    ],
  ])("%s", async (_name, answer) => {
    const { held, probe } = await aRefreshIsInFlight();
    await signsOut();
    await signInAs(VIEWER, { permissions: ["view:cluster"] });

    answer(held);
    await act(async () => {
      await probe;
    });
    await flush();

    const state = useAuthStore.getState();
    expect(state.isAuthenticated).toBe(true);
    expect(state.user?.id).toBe(VIEWER.id);
    expect(state.permissions).toEqual(["view:cluster"]);
    expect(storedUserId()).toBe(VIEWER.id);
    expect(await readerOwner()).toEqual({ owner: VIEWER.id });
  });

  it("control: answered while its session is current, it still rehydrates the user", async () => {
    // The refresh that makes a permission change reach the SPA (Finding A11).
    const { held, probe } = await aRefreshIsInFlight();

    held.resolve(
      json(
        authResponse(ADMIN, { permissions: ["manage:user", "view:cluster"] }),
      ),
    );
    await act(async () => {
      await probe;
    });
    await flush();

    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(useAuthStore.getState().permissions).toEqual([
      "manage:user",
      "view:cluster",
    ]);
  });
});

describe("a request still in flight when the session ends", () => {
  it("is not replayed as the user who signed in since, when it is answered 401", async () => {
    await signInAs(ADMIN);
    const answer = deferred<Response>();
    server.routes["GET /api/v1/x"] = () => answer.promise;
    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    await waitFor(() => {
      expect(server.times("GET /api/v1/x")).toBe(1);
    });
    await signsOut();
    server.routes[REFRESH] = () => json(authResponse(VIEWER)); // the next user's cookie would refresh fine
    await signInAs(VIEWER);

    answer.resolve(json({ error: "unauthorized", message: "expired" }, 401));
    await act(async () => {
      await request;
    });
    await flush();

    expect(server.times("GET /api/v1/x")).toBe(1); // never replayed under their token
    expect(server.times(REFRESH)).toBe(0);
    expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
    expect(useAuthStore.getState().isAuthenticated).toBe(true);
  });

  it("is not sent at all when it was waiting on a refresh of the session that ended", async () => {
    await signInAs(ADMIN, nearExpiry); // the next request refreshes first
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;
    server.routes["GET /api/v1/x"] = () => json({ ok: true });
    const request = settle(apiClient.get(apiPath`/api/v1/x`));
    await waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    // Ended without a request of its own (Sign out's would join the very refresh
    // that is being held): the session is revoked elsewhere, say, and another
    // request's failure finds out.
    act(() => {
      useAuthStore.getState().clearAuth();
    });

    held.resolve(json(authResponse(ADMIN)));
    await act(async () => {
      await request;
    });
    await flush();

    expect(server.times("GET /api/v1/x")).toBe(0);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });
});

describe("an auth response that names a different user than the one held", () => {
  /** The admin is signed in, with something read and something open that is theirs. */
  async function adminHoldsState() {
    await signInAs(ADMIN, { permissions: ["manage:user"] });
    queryClient.setQueryData(["previous-user"], "cached-read");
    for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
    expect(queryClient.getQueryData(["previous-user"])).toBe("cached-read");
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(true);
    }
  }

  function nothingOfTheAdminsIsLeft() {
    expect(queryClient.getQueryData(["previous-user"])).toBeUndefined();
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(false);
    }
  }

  function everythingOfTheAdminsIsKept() {
    expect(queryClient.getQueryData(["previous-user"])).toBe("cached-read");
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(true);
    }
  }

  describe("by a token refresh (another tab signed someone else in on the shared cookie)", () => {
    async function nextRequestRefreshesAndTheCookieIsNow(who: User) {
      await signInAs(ADMIN, { permissions: ["manage:user"], ...nearExpiry });
      queryClient.setQueryData(["previous-user"], "cached-read");
      for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
      server.routes[REFRESH] = () =>
        json(authResponse(who, { permissions: ["view:cluster"] }));
      return readerOwner();
    }

    it("forgets the previous user's reads and open state before the new identity applies, and does not send the read that waited on it as them", async () => {
      const read = await settle(nextRequestRefreshesAndTheCookieIsNow(VIEWER));
      await flush();

      // The admin's read waited on a refresh that came back as the viewer. It
      // belongs to the session that was replaced: not theirs to be sent as.
      expect(read).toBeInstanceOf(StaleSessionError);
      expect(server.times("GET /api/v1/reader")).toBe(0);
      expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      nothingOfTheAdminsIsLeft();
    });

    it("control: the same user's refresh keeps all of it", async () => {
      const read = await nextRequestRefreshesAndTheCookieIsNow(ADMIN);
      await flush();

      expect(read).toEqual({ owner: ADMIN.id });
      expect(useAuthStore.getState().permissions).toEqual(["view:cluster"]);
      everythingOfTheAdminsIsKept();
    });
  });

  // Each applies the identity it is answered with through the same function as
  // login, and each passes it the user it is to compare against. The SSO callback
  // calls setAuthFromResponse; the others start a session by their own request.
  const viaStore = (call: (who: User) => Promise<void> | void) => {
    return async (who: User) => {
      await act(async () => {
        await call(who);
      });
    };
  };
  const sessionOf = (who: User) =>
    authResponse(who, { permissions: ["view:cluster"] });
  const awaitingTotp = () => {
    useAuthStore.setState({
      totpPending: true,
      totpPendingToken: "pending-01",
    });
  };

  const ACTIONS: [name: string, run: (who: User) => Promise<void>][] = [
    [
      "setAuthFromResponse (what the SSO callback calls)",
      viaStore((who) => {
        useAuthStore.getState().setAuthFromResponse(sessionOf(who));
      }),
    ],
    [
      "a sign-in action, called while someone is still held",
      (who) => signInAs(who, { permissions: ["view:cluster"] }),
    ],
    [
      "verifyTotp",
      viaStore(async (who) => {
        server.routes[TOTP] = () => json(sessionOf(who));
        awaitingTotp();
        await useAuthStore.getState().verifyTotp("123456");
      }),
    ],
    [
      "verifyTotpRecovery",
      viaStore(async (who) => {
        server.routes[TOTP] = () => json(sessionOf(who));
        awaitingTotp();
        await useAuthStore.getState().verifyTotpRecovery("recovery-code-01");
      }),
    ],
    [
      "register",
      viaStore(async (who) => {
        server.routes[REGISTER] = () => json(sessionOf(who));
        await useAuthStore.getState().register({
          email: who.email,
          display_name: who.display_name,
          password: "example-password",
        });
      }),
    ],
  ];

  it.each(ACTIONS)(
    "%s: forgets the previous user's state first",
    async (_name, run) => {
      await adminHoldsState();

      await run(VIEWER);

      expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
      expect(useAuthStore.getState().permissions).toEqual(["view:cluster"]);
      nothingOfTheAdminsIsLeft();
    },
  );

  it.each(ACTIONS)(
    "%s, control: the same user keeps it",
    async (_name, run) => {
      await adminHoldsState();

      await run(ADMIN);

      expect(useAuthStore.getState().permissions).toEqual(["view:cluster"]);
      everythingOfTheAdminsIsKept();
    },
  );

  describe("by the SSO callback page, over a live session", () => {
    function renderCallback() {
      server.routes["POST /api/v1/auth/oidc/token-exchange"] = () =>
        json(authResponse(VIEWER, { permissions: ["view:cluster"] }));
      const router = createMemoryRouter(
        [
          { path: "/oidc-callback", element: <OIDCCallbackPage /> },
          { path: "/", element: <p>home</p> },
        ],
        { initialEntries: ["/oidc-callback?oidc_token=one-time-code"] },
      );
      render(<RouterProvider router={router} />);
      return router;
    }

    it("forgets the previous user's state, and the new one is who is signed in", async () => {
      await adminHoldsState();

      const router = renderCallback();
      await waitFor(() => {
        expect(router.state.location.pathname).toBe("/");
      });

      expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
      nothingOfTheAdminsIsLeft();
      expect(await readerOwner()).toEqual({ owner: VIEWER.id }); // the token in use is theirs
    });
  });

  describe("by a resume at boot (the cookie is no longer the stored user's)", () => {
    it.each([
      ["forgets what was persisted for the stored user", VIEWER, false],
      [
        "control: keeps it when the cookie is still the stored user's",
        ADMIN,
        true,
      ],
    ])("%s", async (_name, cookieOf, kept) => {
      localStorage.setItem("nexara_user", JSON.stringify(ADMIN));
      PER_SESSION_STORES["console-store.ts"]?.dirty();
      PER_SESSION_STORES["health-dismiss-store.ts"]?.dirty();
      useAuthStore.setState({ isInitialized: false });
      server.routes[REFRESH] = () =>
        json(authResponse(cookieOf, { permissions: ["view:cluster"] }));

      await useAuthStore.getState().initialize();

      expect(useAuthStore.getState().user?.id).toBe(cookieOf.id);
      expect(useAuthStore.getState().isInitialized).toBe(true);
      expect(PER_SESSION_STORES["console-store.ts"]?.holdsData()).toBe(kept);
      expect(PER_SESSION_STORES["health-dismiss-store.ts"]?.holdsData()).toBe(
        kept,
      );
    });
  });
});

describe("a Sign out whose request to the server failed", () => {
  // The server never revoked the refresh cookie. Nothing after the local sign-out
  // may use it to sign the user back in: a request that finds nobody signed in
  // starts a refresh after the sign-out, so it is nobody's stale answer.
  async function signedOutWithAGoodCookie() {
    await signInAs(ADMIN, { permissions: ["manage:user"] });
    server.routes[LOGOUT] = () =>
      json({ error: "internal", message: "unavailable" }, 500);
    await act(async () => {
      await useAuthStore.getState().logout();
    });
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    server.routes[REFRESH] = () =>
      json(authResponse(ADMIN, { permissions: ["manage:user"] }));
  }

  it("is not undone by a request that finds nobody signed in", async () => {
    await signedOutWithAGoodCookie();
    server.routes["GET /api/v1/straggler"] = () =>
      json({ error: "unauthorized", message: "no session" }, 401);

    const err = await settle(apiClient.get(apiPath`/api/v1/straggler`));
    await flush();

    expect(err).toMatchObject({ name: "ApiClientError", status: 401 });
    expect(server.times(REFRESH)).toBe(0);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().user).toBeNull();
    expect(localStorage.getItem("nexara_user")).toBeNull();
  });

  it("control: the next sign-in is a session like any other, which refreshes", async () => {
    await signedOutWithAGoodCookie();
    await signInAs(VIEWER, { permissions: ["view:cluster"] });
    let reads = 0;
    server.routes["GET /api/v1/reader"] = (init) =>
      ++reads === 1
        ? json({ error: "unauthorized", message: "expired" }, 401)
        : json({ owner: callerOf(init) });
    server.routes[REFRESH] = () =>
      json(authResponse(VIEWER, { permissions: ["view:cluster"] }));

    expect(await apiClient.get(apiPath`/api/v1/reader`)).toEqual({
      owner: VIEWER.id,
    });
    expect(server.times(REFRESH)).toBe(1);
    expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
  });
});

describe("a page that starts signed out", () => {
  // The modules as a page load finds them, which initialize() is first called
  // on: no session ended in them yet. The harness's clearTokens() latches the
  // api-client signed out, so these tests get a copy of the module graph.
  async function aFreshPage() {
    vi.resetModules();
    const { useAuthStore: store } = await import("./auth-store");
    const { apiClient: client } = await import("@/lib/api-client");
    const { apiPath: path } = await import("@/lib/api-path");
    return { store, client, path };
  }

  // The refresh cookie outlives a session the server could not be told about, so
  // it can still be good on a page with no stored user. Nothing that runs on that
  // page may use it to sign anyone in: only a sign-in begins a session there.
  it("is not signed in by a request that finds nobody signed in, whatever the refresh cookie would say", async () => {
    const page = await aFreshPage();
    server.routes[REFRESH] = () =>
      json(authResponse(ADMIN, { permissions: ["manage:user"] }));
    server.routes["GET /api/v1/version"] = () =>
      json({ error: "unauthorized", message: "no session" }, 401);

    await page.store.getState().initialize(); // no stored user
    expect(page.store.getState().isInitialized).toBe(true);
    const err = await settle(page.client.get(page.path`/api/v1/version`));
    await flush();

    expect(err).toMatchObject({ name: "ApiClientError", status: 401 });
    expect(server.times(REFRESH)).toBe(0);
    expect(page.store.getState().isAuthenticated).toBe(false);
    expect(page.store.getState().user).toBeNull();
    expect(localStorage.getItem("nexara_user")).toBeNull();
  });

  it("control: a stored user is still resumed by initialize(), and the session then rotates like any other", async () => {
    const page = await aFreshPage();
    localStorage.setItem("nexara_user", JSON.stringify(ADMIN));
    server.routes[REFRESH] = () =>
      json(
        authResponse(ADMIN, { permissions: ["manage:user"], ...nearExpiry }),
      );
    server.routes["GET /api/v1/reader"] = (init) =>
      json({ owner: callerOf(init) });

    await page.store.getState().initialize();
    expect(page.store.getState().isAuthenticated).toBe(true);
    expect(server.times(REFRESH)).toBe(1); // the resume

    server.routes[REFRESH] = () =>
      json(authResponse(ADMIN, { permissions: ["manage:user"] }));
    expect(await page.client.get(page.path`/api/v1/reader`)).toEqual({
      owner: ADMIN.id,
    });
    expect(server.times(REFRESH)).toBe(2); // the rotation
  });
});

describe("a resume at boot while localStorage refuses the cached user", () => {
  // A full quota. The token and the epoch are written before it, so a throw there
  // left a token in memory for a session the boot then called signed out.
  function refuseTheCachedUser() {
    const setItem = Storage.prototype.setItem.bind(localStorage);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(
      (key: string, value: string) => {
        if (key === "nexara_user") {
          throw new DOMException("quota exceeded", "QuotaExceededError");
        }
        setItem(key, value);
      },
    );
  }

  it("signs the user in, with the token they resumed", async () => {
    localStorage.setItem("nexara_user", JSON.stringify(ADMIN));
    useAuthStore.setState({ isInitialized: false });
    server.routes[REFRESH] = () =>
      json(authResponse(ADMIN, { permissions: ["manage:user"] }));
    refuseTheCachedUser();

    await useAuthStore.getState().initialize();

    const state = useAuthStore.getState();
    expect(state.isAuthenticated).toBe(true);
    expect(state.user?.id).toBe(ADMIN.id);
    expect(state.isInitialized).toBe(true);
    expect(await readerOwner()).toEqual({ owner: ADMIN.id });
  });

  it("signs a login in as well", async () => {
    refuseTheCachedUser();

    await signInAs(ADMIN);

    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(await readerOwner()).toEqual({ owner: ADMIN.id });
  });
});

describe("several requests that all meet an expired session at once", () => {
  it("end it once, not once per request", async () => {
    await signInAs(ADMIN);
    const burst = ["0", "1", "2", "3", "4"];
    for (const n of burst) {
      server.routes[`GET /api/v1/burst/${n}`] = () =>
        json({ error: "unauthorized", message: "expired" }, 401);
    }
    // Counts the resets (stores/session-reset.ts): one per call that ends the
    // session. The refresh is refused too, as it is once a session has expired.
    const resets = vi.spyOn(queryClient, "clear");

    await act(async () => {
      await Promise.all(
        burst.map((n) => settle(apiClient.get(apiPath`/api/v1/burst/${n}`))),
      );
    });

    for (const n of burst) {
      expect(server.times(`GET /api/v1/burst/${n}`)).toBe(1);
    }
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(resets).toHaveBeenCalledTimes(1);
  });
});

describe("a session that ends by a refresh the server refused, with another user's record stored", () => {
  // Another tab has signed the viewer in, so nexara_user names them, while this
  // tab still holds the admin's token. The refresh is refused: the cookie is dead,
  // whoever's it was. api-client ends the session first (clearTokens), which holds
  // the admin's token and so leaves the viewer's record alone; then the store's
  // failure callback signs the store out (clearAuth, clearTokens again) with no
  // token held, so the record goes.
  it.each([401, 403])(
    "removes the record when the refresh is refused with %i: the second clearTokens, from the store's own sign-out",
    async (status) => {
      await signInAs(ADMIN, { expiresIn: -600 }); // expired: the next request refreshes first
      localStorage.setItem("nexara_user", JSON.stringify(VIEWER));
      server.routes[REFRESH] = () => json({}, status);
      server.routes["GET /api/v1/probe"] = () =>
        json({ error: "unauthorized", message: "no session" }, 401);

      await act(async () => {
        await settle(apiClient.get(apiPath`/api/v1/probe`));
      });

      expect(server.times(REFRESH)).toBe(1);
      expect(useAuthStore.getState().isAuthenticated).toBe(false);
      expect(useAuthStore.getState().user).toBeNull();
      expect(getStoredUser()).toBeNull();
    },
  );

  it("control: the same refusal, with the admin's own record stored, removes it as well", async () => {
    await signInAs(ADMIN, { expiresIn: -600 });
    expect(getStoredUser()).toMatchObject({ id: ADMIN.id });
    server.routes[REFRESH] = () => json({}, 401);

    await act(async () => {
      await settle(apiClient.get(apiPath`/api/v1/probe`));
    });

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(getStoredUser()).toBeNull();
  });
});

describe("a resume at boot whose refresh is slow", () => {
  // The login page does not wait for isInitialized, so someone can sign in while
  // the stored user's session is still being resumed.
  async function bootingWhileSomeoneSignsIn() {
    localStorage.setItem("nexara_user", JSON.stringify(ADMIN));
    useAuthStore.setState({ isInitialized: false });
    const held = deferred<Response>();
    server.routes[REFRESH] = () => held.promise;
    const booting = useAuthStore.getState().initialize();
    await waitFor(() => {
      expect(server.times(REFRESH)).toBe(1);
    });
    await signInAs(VIEWER, { permissions: ["view:cluster"] });
    return { held, booting };
  }

  it("is not applied over the user who signed in meanwhile", async () => {
    const { held, booting } = await bootingWhileSomeoneSignsIn();

    held.resolve(json(authResponse(ADMIN, { permissions: ["manage:user"] })));
    await act(async () => {
      await booting;
    });

    const state = useAuthStore.getState();
    expect(state.user?.id).toBe(VIEWER.id);
    expect(state.permissions).toEqual(["view:cluster"]);
    expect(state.isInitialized).toBe(true);
    expect(state.isLoading).toBe(false);
    expect(storedUserId()).toBe(VIEWER.id);
  });

  it("does not end the session of the user who signed in meanwhile, when it fails", async () => {
    const { held, booting } = await bootingWhileSomeoneSignsIn();

    held.resolve(json({}, 401));
    await act(async () => {
      await booting;
    });

    const state = useAuthStore.getState();
    expect(state.isAuthenticated).toBe(true);
    expect(state.user?.id).toBe(VIEWER.id);
    expect(state.isInitialized).toBe(true);
    expect(localStorage.getItem("nexara_user")).not.toBeNull();
  });
});
