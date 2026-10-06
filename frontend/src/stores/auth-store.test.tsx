import type { ReactNode } from "react";
import {
  beforeEach,
  describe,
  expect,
  it,
  vi,
  type MockInstance,
} from "vitest";
import {
  QueryClientProvider,
  QueryObserver,
  useQuery,
} from "@tanstack/react-query";
import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { ProtectedRoute } from "@/components/auth/ProtectedRoute";
import { useCreateStorage } from "@/features/storage/api/storage-queries";
import { apiClient } from "@/lib/api-client";
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
  sleep,
} from "@/test/fake-server";
import {
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import {
  LOGOUT,
  LOGOUT_ALL,
  REFRESH,
  server,
  installAuthStoreHarness,
} from "@/test/api-client-harness";
import { useAuthStore } from "./auth-store";
import { usePBSKeyStore } from "./pbs-key-store";

/**
 * What ending a session does to what the SPA holds (stores/session-reset.ts),
 * through the real auth store, api-client and TanStack singleton the app runs on.
 * Not renderWithProviders' client (gcTime 0, staleTime 0): the bug this guards is
 * the app's own five-minute staleTime serving the next user the previous user's
 * reads.
 */

// Synthetic key file, as in PendingPBSKey.test.tsx.
const PBS_KEY =
  '{"kdf":null,"data":"CANARY-auth-store-key","fingerprint":"aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"}';

const { signInAs } = installAuthStoreHarness({
  store: useAuthStore,
  act,
  reset: () => {
    queryClient.clear();
    emptyPerSessionStores();
    usePBSKeyStore.setState({ pending: [] });
  },
});

const signedOut = () => new Response(null, { status: 204 });

function withClient(ui: ReactNode) {
  return <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>;
}

describe("every path that ends a session", () => {
  it.each<[name: string, end: () => Promise<void>]>([
    [
      "logout",
      async () => {
        server.routes[LOGOUT] = signedOut;
        await useAuthStore.getState().logout();
      },
    ],
    [
      "logout, with the server call failing",
      async () => {
        server.routes[LOGOUT] = () =>
          json({ error: "internal", message: "unavailable" }, 500);
        await useAuthStore.getState().logout();
      },
    ],
    [
      "logoutAll",
      async () => {
        server.routes[LOGOUT_ALL] = signedOut;
        await useAuthStore.getState().logoutAll();
      },
    ],
    [
      "clearAuth",
      () => {
        useAuthStore.getState().clearAuth();
        return Promise.resolve();
      },
    ],
    // The callback initialize() registers with api-client: a request is
    // answered 401 and the refresh that follows is refused too.
    [
      "the forced logout of a request whose refresh is refused",
      async () => {
        server.routes["GET /api/v1/probe"] = () =>
          json({ error: "unauthorized", message: "expired" }, 401);
        await apiClient.get(apiPath`/api/v1/probe`).catch(() => undefined);
      },
    ],
  ])(
    "%s empties the query cache and every store that belongs to the session, and leaves a waiting PBS key",
    async (_name, endTheSession) => {
      server.routes["GET /api/v1/reader"] = (init) =>
        json({ owner: callerOf(init) });
      await signInAs(ADMIN);
      queryClient.setQueryData(["previous-user"], "cached-read");
      await queryClient.query({
        queryKey: ["fetched-by-the-api-client"],
        queryFn: () =>
          apiClient.get<{ owner: string }>(apiPath`/api/v1/reader`),
      });
      for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
      usePBSKeyStore.getState().deliver({
        owner: ADMIN.id,
        cluster: "cluster01",
        clusterName: "cluster01",
        storage: "store01",
        keyText: PBS_KEY,
      });
      // All of it is there before, so that what is gone below is gone because
      // the session ended, not because it was never there.
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(queryClient.getQueryData(["previous-user"])).toBe("cached-read");
      expect(queryClient.getQueryData(["fetched-by-the-api-client"])).toEqual({
        owner: ADMIN.id,
      });
      for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
        expect(probe.holdsData(), file).toBe(true);
      }
      expect(usePBSKeyStore.getState().pending).toHaveLength(1);

      await act(endTheSession);

      expect(useAuthStore.getState().isAuthenticated).toBe(false);
      expect(queryClient.getQueryCache().getAll()).toEqual([]);
      expect(queryClient.getQueryData(["previous-user"])).toBeUndefined();
      for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
        expect(probe.holdsData(), file).toBe(false);
      }
      // Owner-scoped, not session-scoped: waiting for its owner to sign back in.
      expect(usePBSKeyStore.getState().pending).toHaveLength(1);
      expect(usePBSKeyStore.getState().pending[0]?.keyText).toBe(PBS_KEY);
    },
  );
});

describe("a fetch in flight when the session ends", () => {
  function startFetch() {
    const answer = deferred<string>();
    const observer = new QueryObserver(queryClient, {
      queryKey: ["in-flight"],
      queryFn: () => answer.promise,
    });
    const received: unknown[] = [];
    const unsubscribe = observer.subscribe((result) => {
      received.push(result.data);
    });
    return { answer, observer, received, unsubscribe };
  }

  it("does not land in the cache, or reach an observer, once the session is over", async () => {
    await signInAs(ADMIN);
    server.routes[LOGOUT] = signedOut;
    const { answer, observer, received, unsubscribe } = startFetch();
    expect(observer.getCurrentResult().fetchStatus).toBe("fetching");

    await act(async () => {
      await useAuthStore.getState().logout();
    });
    answer.resolve("previous-user-data");
    await flush();

    expect(queryClient.getQueryData(["in-flight"])).toBeUndefined();
    expect(observer.getCurrentResult().data).toBeUndefined();
    expect(received.filter((data) => data !== undefined)).toEqual([]);
    unsubscribe();
  });

  it("control: the same fetch does land, in the cache and in the observer, when the session goes on", async () => {
    await signInAs(ADMIN);
    const { answer, observer, received, unsubscribe } = startFetch();
    expect(observer.getCurrentResult().fetchStatus).toBe("fetching");

    answer.resolve("previous-user-data");
    await flush();

    expect(queryClient.getQueryData(["in-flight"])).toBe("previous-user-data");
    expect(observer.getCurrentResult().data).toBe("previous-user-data");
    expect(received).toContain("previous-user-data");
    unsubscribe();
  });

  it("leaves an observer that was refetching idle, not fetching for good", async () => {
    await signInAs(ADMIN);
    server.routes[LOGOUT] = signedOut;
    const first = deferred<string>();
    const second = deferred<string>();
    let calls = 0;
    const observer = new QueryObserver(queryClient, {
      queryKey: ["refetching"],
      queryFn: () => (calls++ === 0 ? first.promise : second.promise),
    });
    const unsubscribe = observer.subscribe(() => undefined);
    first.resolve("first read");
    await flush();
    void observer.refetch();
    await flush();
    expect(observer.getCurrentResult().fetchStatus).toBe("fetching");
    expect(observer.getCurrentResult().data).toBe("first read");

    await act(async () => {
      await useAuthStore.getState().logout();
    });

    // What cancelQueries() adds to clear(): the revert. Cleared alone, the
    // removed query is left "fetching" for any observer still attached.
    expect(observer.getCurrentResult().fetchStatus).toBe("idle");
    second.resolve("late");
    await flush();
    unsubscribe();
  });
});

describe("the next user in the same tab", () => {
  function Reader({ staleTime }: { staleTime?: number }) {
    const q = useQuery({
      queryKey: ["reader"],
      queryFn: () => apiClient.get<{ owner: string }>(apiPath`/api/v1/reader`),
      ...(staleTime !== undefined ? { staleTime } : {}),
    });
    return (
      <>
        <p>{q.data ? `reads ${q.data.owner}` : "no data"}</p>
        {q.isError && <p>read failed</p>}
      </>
    );
  }

  async function firstUserReadsThenSignsOut() {
    server.routes["GET /api/v1/reader"] = (init) =>
      json({ owner: callerOf(init) });
    server.routes[LOGOUT] = signedOut;
    await signInAs(ADMIN);
    const page = render(withClient(<Reader />));
    expect(await screen.findByText("reads user-admin")).toBeInTheDocument();
    page.unmount(); // what the route guard does when the session ends
    expect(server.times("GET /api/v1/reader")).toBe(1);
    await act(async () => {
      await useAuthStore.getState().logout();
    });
  }

  it("is served by the server, not from the previous user's copy inside the five-minute staleTime", async () => {
    await firstUserReadsThenSignsOut();
    await signInAs(VIEWER);

    render(withClient(<Reader />));

    expect(screen.queryByText("reads user-admin")).toBeNull();
    expect(await screen.findByText("reads user-viewer")).toBeInTheDocument();
    expect(server.times("GET /api/v1/reader")).toBe(2); // the query function ran again
  });

  it("never sees the previous user's copy, even once their own read is refused", async () => {
    await firstUserReadsThenSignsOut();
    await signInAs(VIEWER);
    server.routes["GET /api/v1/reader"] = () =>
      json({ error: "forbidden", message: "not permitted" }, 403);

    // staleTime 0: stale at once, so it refetches, as the app's own would once
    // five minutes had passed, and a failed refetch keeps what it had.
    render(withClient(<Reader staleTime={0} />));

    expect(await screen.findByText("read failed")).toBeInTheDocument();
    expect(screen.queryByText("reads user-admin")).toBeNull();
    expect(screen.getByText("no data")).toBeInTheDocument();
  });
});

describe("the render that follows a sign-out", () => {
  // Re-renders on every change of the store's permissions (clearAuth sets a fresh
  // [] each time) and whenever its query starts or stops fetching, as a page
  // with a refresh spinner does.
  function Page() {
    useAuthStore((s) => s.permissions);
    const q = useQuery({
      queryKey: ["page"],
      queryFn: () => apiClient.get<{ ok: boolean }>(apiPath`/api/v1/page`),
    });
    return (
      <>
        <p>{`page ${q.status}`}</p>
        <p>{q.isFetching ? "refreshing" : "up to date"}</p>
      </>
    );
  }

  function renderGuarded() {
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
    return render(withClient(<RouterProvider router={router} />));
  }

  it("unmounts the page before its query could be rebuilt: no refetch, no token-less request", async () => {
    // The second read is held back, so a refetch (a poll, a window that regained
    // focus) is in flight when the user signs out.
    const refetch = deferred<Response>();
    let reads = 0;
    server.routes["GET /api/v1/page"] = () =>
      ++reads === 1 ? json({ ok: true }) : refetch.promise;
    server.routes[LOGOUT] = signedOut;
    await signInAs(ADMIN);
    renderGuarded();
    expect(await screen.findByText("page success")).toBeInTheDocument();
    act(() => {
      void queryClient.invalidateQueries();
    });
    await waitFor(() => {
      expect(server.times("GET /api/v1/page")).toBe(2);
    });

    await act(async () => {
      await useAuthStore.getState().logout();
    });
    expect(await screen.findByText("login page")).toBeInTheDocument();
    // cancelQueries() reverts the refetch, and TanStack tells the observer so a
    // macrotask later. Were the page still mounted and authenticated then, it
    // would re-render and rebuild its removed query: a third read, with the token
    // already gone.
    await act(async () => {
      await sleep(100);
    });

    expect(server.times("GET /api/v1/page")).toBe(2);
    expect(server.times(REFRESH)).toBe(0);
    refetch.resolve(json({ ok: true }));
  });

  it("is survived by an observer outside the guard, which is rebuilt once and then left alone", async () => {
    // Loop breaker: past 25 refresh attempts the server stops answering, so a
    // runaway loop ends and fails the bound below instead of hanging the run.
    let refreshes = 0;
    server.routes[REFRESH] = () => {
      refreshes++;
      return refreshes > 25
        ? new Promise<Response>(() => undefined)
        : json({}, 401);
    };
    server.routes["GET /api/v1/page"] = () => json({ ok: true });
    await signInAs(ADMIN);
    render(withClient(<Page />));
    expect(await screen.findByText("page success")).toBeInTheDocument();

    server.routes["GET /api/v1/page"] = () =>
      json({ error: "unauthorized", message: "expired" }, 401);
    await act(async () => {
      void queryClient.invalidateQueries();
      await sleep(200);
    });
    // The hazard is real in this setup: the removed query was rebuilt and read
    // again (the first read, the refetch, and the rebuild) ...
    expect(server.times("GET /api/v1/page")).toBeGreaterThanOrEqual(3);
    // ... and then nothing more: a request that fails with nobody signed in does
    // not end the session again, so nothing clears the rebuilt query.
    expect(refreshes).toBeLessThanOrEqual(4);
    const settledAt = server.times("GET /api/v1/page");
    await act(async () => {
      await sleep(200);
    });
    expect(server.times("GET /api/v1/page")).toBe(settledAt);
    expect(refreshes).toBeLessThanOrEqual(4);
  });
});

describe("a PBS encryption key still on its way when the session ends", () => {
  it("is delivered when it lands, though the reset dropped its mutation, and only its owner is ever shown it", async () => {
    server.routes[LOGOUT] = signedOut;
    await signInAs(ADMIN);
    const answer = deferred<Response>();
    server.routes["POST /api/v1/clusters/cluster01/storage"] = () =>
      answer.promise;
    const { result } = renderHook(() => useCreateStorage(), {
      wrapper: ({ children }) => withClient(children),
    });
    act(() => {
      result.current.mutate({
        clusterId: "cluster01",
        data: {
          storage: "store01",
          type: "pbs",
          params: { "encryption-key": "autogen" },
        },
      });
    });
    await waitFor(() => {
      expect(queryClient.getMutationCache().getAll()).toHaveLength(1);
    });

    await act(async () => {
      await useAuthStore.getState().logout();
    });
    // The reset dropped it from the cache; clear() does not cancel a mutation.
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0);
    expect(usePBSKeyStore.getState().pending).toEqual([]);

    answer.resolve(
      json({
        status: "ok",
        storage: "store01",
        generated_encryption_key: PBS_KEY,
      }),
    );
    await waitFor(() => {
      expect(usePBSKeyStore.getState().pending).toHaveLength(1);
    });
    expect(usePBSKeyStore.getState().pending[0]).toMatchObject({
      owner: ADMIN.id,
      storage: "store01",
      keyText: PBS_KEY,
    });

    // Not shown to anyone else: the PBS store drops it when they sign in.
    await signInAs(VIEWER);
    expect(usePBSKeyStore.getState().pending).toEqual([]);
  });
});

describe("localStorage refusing a write while the session ends", () => {
  // zustand's persist writes console-store's copy after it has updated the store,
  // and does not catch a full quota, so the reset can throw. Ending the session
  // must still end it, and say what went wrong rather than lose it.
  let reported: MockInstance<typeof console.error>;

  beforeEach(() => {
    reported = vi.spyOn(console, "error").mockImplementation(() => undefined);
  });

  function fillUp() {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key: string) => {
      if (key === "nexara-console-tabs") {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
    });
  }

  function theFailureWasReported() {
    expect(reported).toHaveBeenCalledWith(
      expect.stringContaining("Could not forget"),
      expect.objectContaining({ name: "QuotaExceededError" }),
    );
  }

  async function signedInWithStateToForget() {
    await signInAs(ADMIN);
    queryClient.setQueryData(["previous-user"], "cached-read");
    for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
  }

  function everythingIsForgotten() {
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(queryClient.getQueryData(["previous-user"])).toBeUndefined();
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(false);
    }
  }

  it("does not leave the UI signed in after clearAuth, and forgets everything else all the same", async () => {
    await signedInWithStateToForget();
    fillUp();

    useAuthStore.getState().clearAuth(); // does not throw

    everythingIsForgotten();
    theFailureWasReported();
  });

  it("does not leave the UI signed in after logout, and logout does not reject", async () => {
    server.routes[LOGOUT] = signedOut;
    await signedInWithStateToForget();
    fillUp();

    await useAuthStore.getState().logout();

    everythingIsForgotten();
    expect(useAuthStore.getState().isLoggingOut).toBe(false);
    theFailureWasReported();
  });

  it("does not leave boot on the spinner that waits for isInitialized", async () => {
    localStorage.setItem("nexara_user", JSON.stringify(ADMIN));
    PER_SESSION_STORES["console-store.ts"]?.dirty();
    useAuthStore.setState({ isInitialized: false });
    fillUp();

    await useAuthStore.getState().initialize();

    expect(useAuthStore.getState().isInitialized).toBe(true);
    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    theFailureWasReported();
  });

  it("has nothing to fail on at a signed-out boot: the console store is at its defaults, and writes nothing", async () => {
    useAuthStore.setState({ isInitialized: false });
    fillUp();

    await useAuthStore.getState().initialize(); // no stored user

    expect(useAuthStore.getState().isInitialized).toBe(true);
    expect(reported).not.toHaveBeenCalled();
  });

  it("does not stop the next user's identity from applying when a session changes hands", async () => {
    await signedInWithStateToForget();
    fillUp();

    act(() => {
      useAuthStore.getState().setAuthFromResponse(authResponse(VIEWER));
    });

    expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
    expect(queryClient.getQueryData(["previous-user"])).toBeUndefined();
    theFailureWasReported();
  });
});

describe("initialize", () => {
  // The two stores whose persisted copy a page rehydrates for the stored user.
  const rehydrated = [
    PER_SESSION_STORES["console-store.ts"],
    PER_SESSION_STORES["health-dismiss-store.ts"],
  ];

  it.each([
    [
      "drops what a dead session left persisted when the stored session cannot be resumed",
      true,
      false,
    ],
    [
      "drops it too when there is no stored user at all, as after a session that ended before this build",
      false,
      false,
    ],
    [
      "control: keeps it when the stored session resumes, as a reload must",
      true,
      true,
    ],
  ])("%s", async (_name, hasStoredUser, resumes) => {
    if (hasStoredUser)
      localStorage.setItem("nexara_user", JSON.stringify(ADMIN));
    if (resumes) server.routes[REFRESH] = () => json(authResponse(ADMIN));
    for (const probe of rehydrated) {
      probe?.dirty();
      expect(probe?.holdsData()).toBe(true);
    }

    await useAuthStore.getState().initialize();

    expect(useAuthStore.getState().isAuthenticated).toBe(resumes);
    for (const probe of rehydrated) expect(probe?.holdsData()).toBe(resumes);
  });
});
