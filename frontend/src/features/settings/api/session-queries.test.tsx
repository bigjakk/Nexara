import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
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
  type FakeServer,
} from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import type { UserSession } from "@/types/api";
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

async function adminIsSignedIn() {
  server.routes[LOGIN] = () =>
    json(authResponse(ADMIN, { permissions: ["manage:user"] }));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: ADMIN.email, password: "example-password" });
  });
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
