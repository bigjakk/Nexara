import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { Toaster, toast } from "sonner";
import { queryClient } from "@/lib/query-client";
import { ADMIN, VIEWER, authResponse, json } from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import {
  LOGOUT,
  LOGOUT_ALL,
  REFRESH,
  REGISTER,
  TOTP,
  server,
  installAuthStoreHarness,
} from "@/test/api-client-harness";
import {
  afterADismissal,
  mountedToasterAndDrawn,
  toasterShowing,
} from "@/test/real-toaster";
import { useAuthStore } from "./auth-store";
import { usePBSKeyStore } from "./pbs-key-store";

/**
 * What is on the toast screen when a session ends, or changes hands, is not for
 * the next user (stores/session-reset.ts). The real auth store and sonner, with a
 * Toaster unmounted and mounted again as AppShell's is: sonner hands every toast
 * never dismissed to each Toaster that mounts. Each path is paired with the same
 * run in which nothing dismisses, so that what the other run finds absent is
 * absent because of the dismissal.
 */

const ON_SCREEN = "Saving the options of pve-01 failed: Proxmox API denied it";
const OWN = "A toast the session that has begun raises itself";

// Registered ahead of the harness's hooks so that, hooks running in a stack, the
// dismissal after a test follows the restoring of the mocks of it.
beforeEach(() => {
  toast.dismiss();
});
afterEach(() => {
  toast.dismiss();
});

const { signInAs } = installAuthStoreHarness({
  store: useAuthStore,
  act,
  reset: () => {
    queryClient.clear();
    emptyPerSessionStores();
    usePBSKeyStore.setState({ pending: [] });
  },
});

/** The ways a session ends or changes hands, each as the user of it was ADMIN. */
const signingOut =
  (route: string, signOut: () => Promise<void>) => async () => {
    server.routes[route] = () => new Response(null, { status: 204 });
    await act(async () => {
      await signOut();
    });
  };

const PATHS: [name: string, end: () => Promise<void>][] = [
  ["Sign out", signingOut(LOGOUT, () => useAuthStore.getState().logout())],
  [
    "Sign out everywhere",
    signingOut(LOGOUT_ALL, () => useAuthStore.getState().logoutAll()),
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

/** ADMIN has a toast on screen, `end` ends the session, and AppShell's Toaster remounts for the next user. */
async function theNextUsersScreenAfter(
  end: () => Promise<void>,
  { dismissing = true } = {},
) {
  await signInAs(ADMIN);
  const first = await toasterShowing(ON_SCREEN);
  // The one thing taken out: the dismissal the reset makes. (dismiss is typed as
  // answering with an id, though the form with none answers undefined at
  // runtime: the stand-in's 0 is there for the type.)
  if (!dismissing) vi.spyOn(toast, "dismiss").mockImplementation(() => 0);

  await end();
  first.unmount();
  await mountedToasterAndDrawn();
}

describe("the toasts on screen when a session ends or changes hands", () => {
  it.each(PATHS)(
    "control: are shown to the next user after %s when nothing dismisses them",
    async (_, end) => {
      await theNextUsersScreenAfter(end, { dismissing: false });

      expect(screen.getByText(ON_SCREEN)).toBeInTheDocument();
    },
  );

  it.each(PATHS)("are not shown to the next user after %s", async (_, end) => {
    await theNextUsersScreenAfter(end);

    expect(screen.queryByText(ON_SCREEN)).toBeNull();
  });

  // A refresh that names the same user rotates their token every few minutes: not
  // the end of anything, and what is on screen is still theirs.
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
// state (the login pages have no Toaster to show it or to clear it) until the
// Toaster that mounts for the next user is handed it. So the next identity
// dismisses once more as it begins, before AppShell, and that Toaster, render.
// Every way of beginning one goes through adoptIdentity.
describe("the toasts raised while nobody is signed in", () => {
  /** Nobody is signed in, and a TOTP code is being asked for. */
  const awaitingTotp = () => {
    server.routes[TOTP] = () => json(authResponse(ADMIN));
    useAuthStore.setState({
      totpPending: true,
      totpPendingToken: "pending-token",
    });
  };

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
        awaitingTotp();
        await act(async () => {
          await useAuthStore.getState().verifyTotp("123456");
        });
      },
    ],
    [
      "a TOTP recovery code",
      async () => {
        awaitingTotp();
        await act(async () => {
          await useAuthStore.getState().verifyTotpRecovery("recovery-code");
        });
      },
    ],
    [
      "a registration",
      async () => {
        server.routes[REGISTER] = () => json(authResponse(ADMIN));
        await act(async () => {
          await useAuthStore.getState().register({
            email: ADMIN.email,
            password: "example-password",
            display_name: ADMIN.display_name,
          });
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

  // AppShell's Toaster mounts a render after the store says someone is in:
  // whatever is still active then is what it is handed.
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

  // The dismissal is not the sign-in's to depend on: if it fails, signing in
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
