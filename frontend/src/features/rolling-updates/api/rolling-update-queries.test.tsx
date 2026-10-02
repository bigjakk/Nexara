import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { clearTokens, storeTokens } from "@/lib/api-client";
import { queryClient } from "@/lib/query-client";
import { useAuthStore } from "@/stores/auth-store";
import type { AuthResponse, SSHCredential, User } from "@/types/api";
import { useUpsertSSHCredentials } from "./rolling-update-queries";

/**
 * The one place the app writes a mutation's answer into the cache outright
 * (the other two setQueryData calls patch an entry that is already there).
 * TanStack runs a mutation's hook-level onSuccess when the answer lands, even
 * after the session that sent it ended and cleared the cache (stores/
 * session-reset.ts) — so the seed has to check whose session it is.
 *
 * Runs on the app's own singleton QueryClient, the one a sign-out clears.
 */

const ADMIN: User = {
  id: "user-admin",
  email: "admin@example.com",
  display_name: "Admin",
  role: "admin",
};
const VIEWER: User = {
  id: "user-viewer",
  email: "viewer@example.com",
  display_name: "Viewer",
  role: "user",
};

const KEY = ["ssh-credentials", "cluster01"];
const SAVED: SSHCredential = {
  cluster_id: "cluster01",
  username: "admin",
  port: 22,
  auth_type: "password",
  has_key: false,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

const SAVE = "PUT /api/v1/clusters/cluster01/ssh-credentials";

let answer: { promise: Promise<Response>; resolve: (r: Response) => void };

function deferredResponse() {
  let resolve!: (r: Response) => void;
  const promise = new Promise<Response>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

function signIn(user: User) {
  const response: AuthResponse = {
    user,
    access_token: `token-${user.id}`,
    refresh_token: "",
    expires_at: Math.floor(Date.now() / 1000) + 3600,
    permissions: [],
  };
  storeTokens(response);
  act(() => {
    useAuthStore.setState({
      user,
      permissions: [],
      isAuthenticated: true,
      isInitialized: true,
    });
  });
}

/** Sends the save, and holds its answer until the test lets it land. */
function startSave() {
  const { result } = renderHook(() => useUpsertSSHCredentials(), { wrapper });
  act(() => {
    result.current.mutate({
      clusterId: "cluster01",
      username: "admin",
      port: 22,
      auth_type: "password",
      password: "example-password",
    });
  });
  return result;
}

/** Lets the answer land, and waits for the mutation to finish with it. */
async function theAnswerLands(
  result: ReturnType<typeof startSave>,
): Promise<void> {
  answer.resolve(
    new Response(JSON.stringify(SAVED), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );
  // isSuccess is dispatched after the hook's onSuccess and onSettled ran.
  await waitFor(() => {
    expect(result.current.isSuccess).toBe(true);
  });
}

beforeEach(() => {
  clearTokens();
  queryClient.clear();
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
    isInitialized: true,
    isLoggingOut: false,
  });
  answer = deferredResponse();
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      const key = `${init?.method ?? "GET"} ${url}`;
      if (key === SAVE) return answer.promise;
      // The refresh, with no session cookie to refresh from.
      if (key === "POST /api/v1/auth/refresh") {
        return Promise.resolve(new Response("{}", { status: 401 }));
      }
      // Logout, and anything else: nothing to say.
      return Promise.resolve(new Response(null, { status: 204 }));
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
});

describe("useUpsertSSHCredentials", () => {
  it("control: seeds the credential cache for the session that sent the save", async () => {
    signIn(ADMIN);
    const result = startSave();

    await theAnswerLands(result);

    expect(queryClient.getQueryData(KEY)).toEqual(SAVED);
  });

  it("does not seed it once the session that sent the save has ended", async () => {
    signIn(ADMIN);
    const result = startSave();
    await act(async () => {
      await useAuthStore.getState().logout();
    });
    expect(useAuthStore.getState().isAuthenticated).toBe(false);

    await theAnswerLands(result);

    expect(queryClient.getQueryData(KEY)).toBeUndefined();
    expect(queryClient.getQueryCache().find({ queryKey: KEY })).toBeUndefined();
  });

  it("does not seed it into the next user's session, who may not hold manage:ssh_credentials", async () => {
    signIn(ADMIN);
    const result = startSave();
    await act(async () => {
      await useAuthStore.getState().logout();
    });
    signIn(VIEWER);

    await theAnswerLands(result);

    expect(queryClient.getQueryData(KEY)).toBeUndefined();
  });

  it("seeds nothing for a save sent and answered with no one signed in", async () => {
    // Nobody at either end: "the same session" would be true of undefined and
    // undefined, and the record would wait in the cache for whoever signs in.
    const result = startSave();
    expect(useAuthStore.getState().isAuthenticated).toBe(false);

    await theAnswerLands(result);

    expect(queryClient.getQueryData(KEY)).toBeUndefined();
  });
});
