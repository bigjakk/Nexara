import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { Toaster, toast } from "sonner";
import { clearTokens } from "@/lib/api-client";
import { queryClient } from "@/lib/query-client";
import {
  ADMIN,
  authResponse,
  installFakeServer,
  json,
  VIEWER,
  type FakeServer,
} from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import {
  afterADismissal,
  mountedToasterAndDrawn,
  toasterShowing,
} from "@/test/real-toaster";
import type { RegisterRequest, User } from "@/types/api";
import { useAuthStore } from "./auth-store";
import { usePBSKeyStore } from "./pbs-key-store";

/**
 * What is on the toast screen when a session ends, or changes hands, is not for
 * the next user (stores/session-reset.ts). Run through the real auth store, with
 * the real sonner and a Toaster that is unmounted and mounted again as
 * AppShell's is: sonner hands every toast that was never dismissed to each
 * Toaster that mounts, and a toast naming a node, in the server's words, would be
 * handed to whoever signs in next.
 *
 * Each path is paired with the same run in which nothing dismisses the toasts,
 * which shows the toast does come back there: what the other run finds absent is
 * absent because of the dismissal.
 */

const LOGOUT = "POST /api/v1/auth/logout";
const LOGOUT_ALL = "POST /api/v1/auth/logout-all";
const LOGIN = "POST /api/v1/auth/login";
const REFRESH = "POST /api/v1/auth/refresh";
const TOTP = "POST /api/v1/auth/totp/verify-login";
const REGISTER = "POST /api/v1/auth/register";

const ON_SCREEN = "Saving the options of pve-01 failed: Proxmox API denied it";
const OWN = "A toast the session that has begun raises itself";

let server: FakeServer;

async function signInAs(user: User) {
  server.routes[LOGIN] = () => json(authResponse(user));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

/** The ways a session ends or changes hands, each as the user of it was ADMIN. */
const PATHS: [name: string, end: () => Promise<void>][] = [
  [
    "Sign out",
    async () => {
      server.routes[LOGOUT] = () => new Response(null, { status: 204 });
      await act(async () => {
        await useAuthStore.getState().logout();
      });
    },
  ],
  [
    "Sign out everywhere",
    async () => {
      server.routes[LOGOUT_ALL] = () => new Response(null, { status: 204 });
      await act(async () => {
        await useAuthStore.getState().logoutAll();
      });
    },
  ],
  [
    "an expiry, which signs the user out",
    () => {
      act(() => {
        useAuthStore.getState().clearAuth();
      });
      return Promise.resolve();
    },
  ],
  [
    "a refresh answered for another user",
    () => {
      act(() => {
        useAuthStore.getState().setAuthFromResponse(authResponse(VIEWER));
      });
      return Promise.resolve();
    },
  ],
  [
    "another user signing in over the one held",
    async () => {
      await signInAs(VIEWER);
    },
  ],
  [
    "a resume at boot whose cookie names someone else",
    async () => {
      // A reload: the stored user is still ADMIN, and the shared cookie now
      // answers for VIEWER.
      server.routes[REFRESH] = () => json(authResponse(VIEWER));
      useAuthStore.setState({ isInitialized: false });
      await act(async () => {
        await useAuthStore.getState().initialize();
      });
    },
  ],
];

beforeEach(async () => {
  toast.dismiss();
  localStorage.clear();
  clearTokens();
  queryClient.clear();
  emptyPerSessionStores();
  usePBSKeyStore.setState({ pending: [] });
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
  server = installFakeServer();
  // No session cookie: the refresh is refused, as it is once a session is gone.
  server.routes[REFRESH] = () => json({}, 401);
  // Registers the forced-logout and refresh callbacks, as main.tsx does at boot.
  await useAuthStore.getState().initialize();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  toast.dismiss();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
  emptyPerSessionStores();
  usePBSKeyStore.setState({ pending: [] });
});

describe("the toasts on screen when a session ends or changes hands", () => {
  it.each(PATHS)(
    "control: are shown to the next user after %s when nothing dismisses them",
    async (_, end) => {
      await signInAs(ADMIN);
      const first = await toasterShowing(ON_SCREEN);
      // The one thing taken out: the dismissal the reset makes. (dismiss is typed
      // as answering with an id, though the form with none answers undefined at
      // runtime: the stand-in's 0 is there for the type.)
      vi.spyOn(toast, "dismiss").mockImplementation(() => 0);

      await end();
      // AppShell, and the Toaster in it, remounts for the next user.
      first.unmount();
      await mountedToasterAndDrawn();

      expect(screen.getByText(ON_SCREEN)).toBeInTheDocument();
    },
  );

  it.each(PATHS)("are not shown to the next user after %s", async (_, end) => {
    await signInAs(ADMIN);
    const first = await toasterShowing(ON_SCREEN);

    await end();
    first.unmount();
    await mountedToasterAndDrawn();

    expect(screen.queryByText(ON_SCREEN)).toBeNull();
  });

  // A refresh that names the same user rotates their token and their
  // permissions, every few minutes; it is not the end of anything, and what they
  // have on screen is still theirs.
  it("stay on screen through a refresh that names the same user", async () => {
    await signInAs(ADMIN);
    await toasterShowing(ON_SCREEN);

    act(() => {
      useAuthStore.getState().setAuthFromResponse(authResponse(ADMIN));
    });
    await afterADismissal();

    expect(screen.getByText(ON_SCREEN)).toBeInTheDocument();
    expect(toast.getToasts()).toHaveLength(1);
  });
});

// The reset dismisses what is on the screen when a session ends, and only what
// exists then. A toast raised afterwards, with nobody signed in, sits in sonner's
// state — the login pages have no Toaster to show it or to clear it — until the
// Toaster that mounts for the next user is handed it. So the next identity
// dismisses once more as it begins, before AppShell, and that Toaster, render.
// Every way of beginning one goes through adoptIdentity.
describe("the toasts raised while nobody is signed in", () => {
  /** The ways someone begins a session, as nobody is signed in. */
  const BEGINNINGS: [name: string, begin: () => Promise<void>][] = [
    [
      "a sign-in",
      async () => {
        await signInAs(ADMIN);
      },
    ],
    [
      "a TOTP code",
      async () => {
        server.routes[TOTP] = () => json(authResponse(ADMIN));
        useAuthStore.setState({
          totpPending: true,
          totpPendingToken: "pending-token",
        });
        await act(async () => {
          await useAuthStore.getState().verifyTotp("123456");
        });
      },
    ],
    [
      "a TOTP recovery code",
      async () => {
        server.routes[TOTP] = () => json(authResponse(ADMIN));
        useAuthStore.setState({
          totpPending: true,
          totpPendingToken: "pending-token",
        });
        await act(async () => {
          await useAuthStore.getState().verifyTotpRecovery("recovery-code");
        });
      },
    ],
    [
      "a registration",
      async () => {
        server.routes[REGISTER] = () => json(authResponse(ADMIN));
        const request: RegisterRequest = {
          email: ADMIN.email,
          password: "example-password",
          display_name: ADMIN.display_name,
        };
        await act(async () => {
          await useAuthStore.getState().register(request);
        });
      },
    ],
    [
      "an SSO callback",
      () => {
        act(() => {
          useAuthStore.getState().setAuthFromResponse(authResponse(ADMIN));
        });
        return Promise.resolve();
      },
    ],
  ];

  it.each(BEGINNINGS)(
    "are dismissed when %s begins a session",
    async (_, begin) => {
      toast.error(ON_SCREEN);
      // Active before, so it is the beginning that takes it away.
      expect(toast.getToasts()).toHaveLength(1);

      await begin();

      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(toast.getToasts()).toHaveLength(0);
    },
  );

  // AppShell renders from the store, and the Toaster in it mounts a render after
  // the store says someone is in: whatever is still active then is what that
  // Toaster is handed.
  it("have been dismissed by the time the store says someone is signed in", async () => {
    toast.error(ON_SCREEN);
    expect(toast.getToasts()).toHaveLength(1);
    const activeWhenTold: number[] = [];
    const unsubscribe = useAuthStore.subscribe((state) => {
      if (state.isAuthenticated) activeWhenTold.push(toast.getToasts().length);
    });

    await signInAs(ADMIN);
    unsubscribe();

    expect(activeWhenTold.length).toBeGreaterThan(0);
    expect(activeWhenTold.every((active) => active === 0)).toBe(true);
  });

  it("are not shown by the Toaster that mounts for the person who signed in", async () => {
    toast.error(ON_SCREEN);

    await signInAs(ADMIN);
    await mountedToasterAndDrawn();

    expect(screen.queryByText(ON_SCREEN)).toBeNull();
  });

  it("control: are shown by it when nothing dismisses them", async () => {
    toast.error(ON_SCREEN);
    vi.spyOn(toast, "dismiss").mockImplementation(() => 0);

    await signInAs(ADMIN);
    await mountedToasterAndDrawn();

    expect(screen.getByText(ON_SCREEN)).toBeInTheDocument();
  });

  it("do not take the toasts of the session that has begun: one it raises is shown", async () => {
    toast.error(ON_SCREEN);
    await signInAs(ADMIN);
    render(<Toaster />);

    toast.error(OWN);

    expect(await screen.findByText(OWN)).toBeInTheDocument();
    expect(screen.queryByText(ON_SCREEN)).toBeNull();
  });

  // A page's mount effect raises its toast while the Toaster beside it has yet to
  // subscribe, and what a session raises as it begins is its own. So the dismissal
  // comes before the store announces the identity, and neither after it nor at the
  // Toaster's mount: a toast raised as the announcement is made survives, and the
  // Toaster that mounts afterwards is handed it, and not what came before.
  it("keep a toast the new session raises as it begins, before its Toaster has mounted", async () => {
    toast.error(ON_SCREEN);
    let raised = false;
    const unsubscribe = useAuthStore.subscribe((state) => {
      if (state.isAuthenticated && !raised) {
        raised = true;
        toast.error(OWN);
      }
    });

    await signInAs(ADMIN);
    unsubscribe();
    expect(raised).toBe(true);
    // Active: its own, and not the one the previous session left.
    expect(toast.getToasts()).toHaveLength(1);

    await mountedToasterAndDrawn();

    expect(screen.getByText(OWN)).toBeInTheDocument();
    expect(screen.queryByText(ON_SCREEN)).toBeNull();
  });

  it("are kept by a refresh that names the user already signed in", async () => {
    await signInAs(ADMIN);
    toast.error(ON_SCREEN);
    expect(toast.getToasts()).toHaveLength(1);

    act(() => {
      useAuthStore.getState().setAuthFromResponse(authResponse(ADMIN));
    });

    expect(toast.getToasts()).toHaveLength(1);
  });

  // The dismissal is not the sign-in's to depend on: a toast that the sign-in
  // finds when its dismissal fails is shown, as it was before, and signing in
  // still completes.
  it("do not stop a sign-in when sonner cannot dismiss them", async () => {
    const failure = new Error("sonner could not dismiss");
    vi.spyOn(toast, "dismiss").mockImplementation(() => {
      throw failure;
    });
    const reported = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);

    await signInAs(ADMIN);

    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(useAuthStore.getState().user?.id).toBe(ADMIN.id);
    expect(reported).toHaveBeenCalledWith(
      "Could not dismiss the toasts a session left behind",
      failure,
    );
  });
});
