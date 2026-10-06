import { describe, expect, it } from "vitest";
import { act } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { queryClient } from "@/lib/query-client";
import { ADMIN, VIEWER, authResponse, json } from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import {
  REFRESH,
  server,
  installAuthStoreHarness,
} from "@/test/api-client-harness";
import type { User } from "@/types/api";
import { useAuthStore } from "./auth-store";
import { usePBSKeyStore } from "./pbs-key-store";

/**
 * The flag that holds a refresh back mid-logout (isLoggingOut) is raised for the
 * identity that is signing out: by logout(), logoutAll(), or the revoke of the
 * session the tab holds. A new identity beginning has nothing to do with it, and
 * adoptIdentity lowers it as they begin; the same user again keeps theirs, since
 * a flag up then is a sign-out they have begun.
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

/** What is left of a sign-out that was begun and never ended: the flag is up. */
function aSignOutIsUnderWay() {
  act(() => {
    useAuthStore.setState({ isLoggingOut: true });
  });
  expect(useAuthStore.getState().isLoggingOut).toBe(true);
}

describe("a flag raised for the identity before, when a new one begins", () => {
  // The ways a different identity begins, with the flag up when it does: who is
  // held (if anyone) and what begins it. A refresh never gets there with the flag
  // up: its callback drops the answer, as the last test here shows.
  it.each<[name: string, held: User | null, begin: () => Promise<void>]>([
    [
      "a sign-in when nobody is held",
      null,
      async () => {
        await signInAs(ADMIN);
      },
    ],
    [
      "a sign-in over another user",
      ADMIN,
      async () => {
        await signInAs(VIEWER);
      },
    ],
    [
      "an SSO callback answered for another user",
      ADMIN,
      () => {
        act(() => {
          useAuthStore.getState().setAuthFromResponse(authResponse(VIEWER));
        });
        return Promise.resolve();
      },
    ],
    [
      "a resume at boot whose cookie names someone else",
      ADMIN,
      async () => {
        server.routes[REFRESH] = () => json(authResponse(VIEWER));
        useAuthStore.setState({ isInitialized: false });
        await act(async () => {
          await useAuthStore.getState().initialize();
        });
      },
    ],
  ])(
    "is lowered for %s, which is theirs to begin from",
    async (_, held, begin) => {
      if (held !== null) await signInAs(held);
      aSignOutIsUnderWay();

      await begin();

      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(useAuthStore.getState().user?.id).not.toBe(held?.id);
      expect(useAuthStore.getState().isLoggingOut).toBe(false);
    },
  );

  it.each<[name: string, begin: () => Promise<void>]>([
    [
      "an SSO callback that names the same user, whose sign-out it is",
      () => {
        act(() => {
          useAuthStore.getState().setAuthFromResponse(authResponse(ADMIN));
        });
        return Promise.resolve();
      },
    ],
    [
      "the same user signing in again over the one held",
      async () => {
        await signInAs(ADMIN);
      },
    ],
  ])("is kept for %s", async (_, begin) => {
    await signInAs(ADMIN);
    aSignOutIsUnderWay();

    await begin();

    expect(useAuthStore.getState().user?.id).toBe(ADMIN.id);
    expect(useAuthStore.getState().isLoggingOut).toBe(true);
  });

  // The suppression the flag exists for still holds for the user who is signing
  // out: the refresh callback drops an answer while it is up, and adopts one for
  // the next user once it is down.
  it("holds a refresh back while it is up, and lets the next user's refreshes through once it is down", async () => {
    await signInAs(ADMIN);
    aSignOutIsUnderWay();
    server.routes[REFRESH] = () =>
      json(authResponse(ADMIN, { permissions: ["manage:user"] }));
    server.routes["GET /api/v1/probe"] = () =>
      json({ error: "unauthorized", message: "expired" }, 401);
    const probe = () =>
      act(async () => {
        await apiClient.get(apiPath`/api/v1/probe`).then(
          () => undefined,
          () => undefined,
        );
      });

    // A refresh of the user who is signing out: the flag drops its answer.
    await probe();
    expect(server.times(REFRESH)).toBe(1);
    expect(useAuthStore.getState().permissions).toEqual([]);

    // The next user begins, and their refresh is adopted as one is for anyone.
    await signInAs(VIEWER);
    expect(useAuthStore.getState().isLoggingOut).toBe(false);
    server.routes[REFRESH] = () =>
      json(authResponse(VIEWER, { permissions: ["view:node"] }));
    await probe();
    expect(server.times(REFRESH)).toBe(2);
    expect(useAuthStore.getState().permissions).toEqual(["view:node"]);
  });
});
