import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider, useMutation } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { apiClient, clearTokens } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { queryClient } from "@/lib/query-client";
import {
  ADMIN,
  authResponse,
  deferred,
  flush,
  installFakeServer,
  json,
  VIEWER,
  type FakeServer,
} from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import type { User } from "@/types/api";
import { useAuthStore } from "@/stores/auth-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { ProtectedRoute } from "./ProtectedRoute";

/**
 * Where a signed-out user is sent. The login URL carries the page they were on
 * (returnTo) so that someone whose session EXPIRED comes back to it; after a
 * sign-out they asked for, the same URL would hand that page to whoever signs in
 * next.
 */

let server: FakeServer;

/**
 * The admin as a later refresh finds them: the same id, a new name and a
 * different role. Who is signed in has not changed, so nothing should remount.
 */
const ADMIN_RENAMED: User = {
  ...ADMIN,
  display_name: "Admin (renamed)",
  role: "user",
};

/** A page with a state of its own, which only a remount takes from it. */
function Counter() {
  const [clicks, setClicks] = useState(0);
  return (
    <button
      onClick={() => {
        setClicks(clicks + 1);
      }}
    >
      clicked {clicks}
    </button>
  );
}

/**
 * A page that starts something and puts it in the task log when the answer
 * arrives — as the app's start/stop/migrate buttons do, with a callback given to
 * mutate() rather than to the hook.
 */
function Starter() {
  const start = useMutation({
    mutationFn: () => apiClient.post(apiPath`/api/v1/start`, {}),
  });
  return (
    <button
      onClick={() => {
        start.mutate(undefined, {
          onSuccess: () => {
            useTaskLogStore.getState().setFocusedTask({
              clusterId: "cluster01",
              upid: "UPID:pve-01:00000001:00000002:00000003:qmstart:101:admin@example.com:",
              description: "Start linux01",
            });
          },
        });
      }}
    >
      start
    </button>
  );
}

/** `entries` is a history to start in, with the last of them current. */
function renderApp(start: string, entries: string[] = [start]) {
  const router = createMemoryRouter(
    [
      { path: "/login", element: <p>login page</p> },
      {
        element: <ProtectedRoute />,
        children: [
          { path: "/", element: <p>home page</p> },
          { path: "/clusters", element: <p>clusters page</p> },
          { path: "/counter", element: <Counter /> },
          { path: "/starter", element: <Starter /> },
        ],
      },
    ],
    { initialEntries: entries, initialIndex: entries.length - 1 },
  );
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

/** A session with a token behind it, as a real sign-in leaves one. */
async function signInForReal(user = ADMIN) {
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(user));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

function signedIn() {
  act(() => {
    useAuthStore.setState({
      user: ADMIN,
      permissions: [],
      isAuthenticated: true,
      isInitialized: true,
      signedOutByUser: false,
    });
  });
}

beforeEach(async () => {
  localStorage.clear();
  clearTokens();
  queryClient.clear();
  emptyPerSessionStores();
  useTaskLogStore.setState({ focusedTask: null });
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
    isLoading: false,
    isInitialized: true,
    totpPending: false,
    totpPendingToken: null,
    isLoggingOut: false,
    signedOutByUser: false,
  });
  server = installFakeServer();
  server.routes["POST /api/v1/auth/logout"] = () =>
    new Response(null, { status: 204 });
  server.routes["POST /api/v1/auth/logout-all"] = () =>
    new Response(null, { status: 204 });
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(ADMIN));
  // No session cookie: the refresh is refused, as it is once a session is gone.
  server.routes["POST /api/v1/auth/refresh"] = () => json({}, 401);
  // Registers the forced-logout and refresh callbacks, as main.tsx does at boot.
  await useAuthStore.getState().initialize();
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
});

describe("the login page a signed-out user is sent to", () => {
  it("is the bare /login after Sign out: the page they were on is not handed on", async () => {
    signedIn();
    const router = renderApp("/clusters");
    expect(await screen.findByText("clusters page")).toBeInTheDocument();

    await act(async () => {
      await useAuthStore.getState().logout();
    });

    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toBe("");
  });

  it("is the bare /login after Sign out everywhere", async () => {
    signedIn();
    const router = renderApp("/clusters");
    expect(await screen.findByText("clusters page")).toBeInTheDocument();

    await act(async () => {
      await useAuthStore.getState().logoutAll();
    });

    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(router.state.location.search).toBe("");
  });

  it("is the bare /login after the user revokes the session they hold", async () => {
    signedIn();
    const router = renderApp("/clusters");
    expect(await screen.findByText("clusters page")).toBeInTheDocument();

    act(() => {
      useAuthStore.getState().clearAuth({ byUser: true });
    });

    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(router.state.location.search).toBe("");
  });

  it("control: carries the page after an expiry, so the same user resumes where they were", async () => {
    signedIn();
    const router = renderApp("/clusters");
    expect(await screen.findByText("clusters page")).toBeInTheDocument();

    act(() => {
      useAuthStore.getState().clearAuth();
    });

    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(router.state.location.search).toBe("?returnTo=%2Fclusters");
  });

  it("still carries the page for someone who arrives without a session: a page load, with nothing signed out in it", async () => {
    // A bookmark or an address typed into the bar is a full page load, which
    // starts the in-memory store with nothing signed out.
    const router = renderApp("/clusters");

    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(router.state.location.search).toBe("?returnTo=%2Fclusters");
  });

  it("is the bare /login too when history Back returns to a page of the user who signed out", async () => {
    signedIn();
    const router = renderApp("/", ["/clusters", "/"]);
    expect(await screen.findByText("home page")).toBeInTheDocument();
    await act(async () => {
      await useAuthStore.getState().logout();
    });
    expect(await screen.findByText("login page")).toBeInTheDocument();

    // The page before it in the history is a protected route mounting afresh,
    // for nobody. It is still the signed-out user's page, not the next user's.
    await act(async () => {
      await router.navigate(-1);
    });

    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search).toBe("");
  });

  it("carries the page again once the next session is the one that expires", async () => {
    signedIn();
    const router = renderApp("/clusters");
    expect(await screen.findByText("clusters page")).toBeInTheDocument();
    await act(async () => {
      await useAuthStore.getState().logout();
    });
    expect(await screen.findByText("login page")).toBeInTheDocument();

    await act(async () => {
      await useAuthStore
        .getState()
        .login({ email: ADMIN.email, password: "example-password" });
      await router.navigate("/clusters");
    });
    expect(await screen.findByText("clusters page")).toBeInTheDocument();
    expect(useAuthStore.getState().signedOutByUser).toBe(false);
    act(() => {
      useAuthStore.getState().clearAuth();
    });

    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(router.state.location.search).toBe("?returnTo=%2Fclusters");
  });
});

describe("a Sign out over a session the server had already expired", () => {
  // The Sign out request is refused (401) and so is the refresh behind it, which
  // ends the session through the forced-logout path before Sign out has got to
  // its own last step. The user still asked for it.
  const SIGN_OUTS = [
    [
      "logout",
      "POST /api/v1/auth/logout",
      () => useAuthStore.getState().logout(),
    ],
    [
      "logoutAll",
      "POST /api/v1/auth/logout-all",
      () => useAuthStore.getState().logoutAll(),
    ],
  ] as const;

  async function onAnExpiredSession(route: string) {
    await signInForReal();
    server.routes[route] = () =>
      json({ error: "unauthorized", message: "session expired" }, 401);
    return renderApp("/clusters");
  }

  it.each(SIGN_OUTS)(
    "%s: is the bare /login, as every other sign-out the user asked for",
    async (_name, route, signOut) => {
      const router = await onAnExpiredSession(route);
      expect(await screen.findByText("clusters page")).toBeInTheDocument();

      await act(async () => {
        await signOut();
      });

      expect(await screen.findByText("login page")).toBeInTheDocument();
      expect(router.state.location.search).toBe("");
    },
  );

  it.each(SIGN_OUTS)(
    "%s: the first signed-out state the store shows already says the user asked for it",
    async (_name, route, signOut) => {
      await onAnExpiredSession(route);
      const firstSignedOut: boolean[] = [];
      const unsubscribe = useAuthStore.subscribe((state, previous) => {
        if (previous.isAuthenticated && !state.isAuthenticated) {
          firstSignedOut.push(state.signedOutByUser);
        }
      });

      await act(async () => {
        await signOut();
      });
      unsubscribe();

      expect(firstSignedOut).toEqual([true]);
    },
  );

  it("control: an expiry nobody asked to sign out of still carries the page", async () => {
    await signInForReal();
    server.routes["GET /api/v1/probe"] = () =>
      json({ error: "unauthorized", message: "session expired" }, 401);
    const router = renderApp("/clusters");
    expect(await screen.findByText("clusters page")).toBeInTheDocument();

    await act(async () => {
      await apiClient.get(apiPath`/api/v1/probe`).catch(() => undefined);
    });

    expect(await screen.findByText("login page")).toBeInTheDocument();
    expect(router.state.location.search).toBe("?returnTo=%2Fclusters");
  });
});

describe("a session that changes hands without a sign-out", () => {
  // Another tab signed someone else in on the shared refresh cookie, and this
  // tab's next refresh was answered for them: the stores and the cache are reset
  // (see auth-store), but the page that is open is not, unless it remounts.
  it("remounts the page: its state is not carried to the next user", async () => {
    const user = userEvent.setup();
    signedIn();
    renderApp("/counter");
    await user.click(await screen.findByRole("button", { name: "clicked 0" }));
    expect(
      screen.getByRole("button", { name: "clicked 1" }),
    ).toBeInTheDocument();

    act(() => {
      useAuthStore.getState().setAuthFromResponse(authResponse(VIEWER));
    });

    expect(
      await screen.findByRole("button", { name: "clicked 0" }),
    ).toBeInTheDocument();
  });

  it("control: a refresh for the same user, new name, role and permissions, keeps the page as it was", async () => {
    const user = userEvent.setup();
    signedIn();
    renderApp("/counter");
    await user.click(await screen.findByRole("button", { name: "clicked 0" }));

    act(() => {
      useAuthStore
        .getState()
        .setAuthFromResponse(
          authResponse(ADMIN_RENAMED, { permissions: ["a:b"] }),
        );
    });
    await flush();

    expect(
      screen.getByRole("button", { name: "clicked 1" }),
    ).toBeInTheDocument();
  });

  it("does not put what the previous user started in the next user's task log", async () => {
    // A per-call callback runs only while the page that made the call is
    // mounted: one that outlived the swap would write the admin's task into the
    // viewer's panel, after the swap had emptied it.
    const user = userEvent.setup();
    await signInForReal();
    const answer = deferred<Response>();
    server.routes["POST /api/v1/start"] = () => answer.promise;
    renderApp("/starter");
    await user.click(await screen.findByRole("button", { name: "start" }));
    expect(server.times("POST /api/v1/start")).toBe(1);

    act(() => {
      useAuthStore.getState().setAuthFromResponse(authResponse(VIEWER));
    });
    answer.resolve(json({ upid: "UPID:pve-01:00000001" }));
    await flush();
    await flush();

    expect(useTaskLogStore.getState().focusedTask).toBeNull();
  });

  it("control: the same start, answered with no swap, does reach the task log", async () => {
    const user = userEvent.setup();
    await signInForReal();
    const answer = deferred<Response>();
    server.routes["POST /api/v1/start"] = () => answer.promise;
    renderApp("/starter");
    await user.click(await screen.findByRole("button", { name: "start" }));

    answer.resolve(json({ upid: "UPID:pve-01:00000001" }));
    await flush();
    await flush();

    expect(useTaskLogStore.getState().focusedTask).not.toBeNull();
  });
});
