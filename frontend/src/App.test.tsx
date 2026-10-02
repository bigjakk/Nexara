import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import { clearTokens, storeTokens } from "@/lib/api-client";
import { queryClient } from "@/lib/query-client";
import {
  ADMIN,
  authResponse,
  callerOf,
  flush,
  installFakeServer,
  json,
  VIEWER,
} from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import { useAuthStore } from "@/stores/auth-store";
import App from "./App";

/**
 * The signed-in app, through the router it is built with, when the session it
 * is showing changes hands: another tab signed someone else in on the shared
 * refresh cookie, and this tab's next refresh was answered for them. The hub
 * WebSocket that AppShell opens is bound to whoever's token minted it — the
 * server authorizes every subscribe frame on it as that user — so it has to go
 * with the session, which is what remounting the shell does.
 */

const MINT = "POST /api/v1/auth/ws-token";

/** What the app opened a socket with, and whether it has closed it since. */
const sockets: { protocols: unknown; closed: boolean }[] = [];

class FakeSocket {
  static OPEN = 1;
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private readonly record: { protocols: unknown; closed: boolean };

  constructor(_url: string, protocols: unknown) {
    this.record = { protocols, closed: false };
    sockets.push(this.record);
  }

  close() {
    this.record.closed = true;
  }

  send() {
    // Nothing is listening: the test reads what was opened, not what was said.
  }
}

/** Who each hub token was minted for, in order. */
let mintedFor: string[];

beforeEach(async () => {
  localStorage.clear();
  clearTokens();
  queryClient.clear();
  emptyPerSessionStores();
  sockets.length = 0;
  mintedFor = [];
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
  const server = installFakeServer();
  vi.stubGlobal("WebSocket", FakeSocket);
  server.routes["POST /api/v1/auth/refresh"] = () => json({}, 401);
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(ADMIN));
  server.routes[MINT] = (init) => {
    mintedFor.push(callerOf(init));
    return json({ token: `hub-${callerOf(init)}` });
  };
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: ADMIN.email, password: "example-password" });
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
});

function renderTheApp() {
  render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>,
  );
}

describe("the app when the session changes hands", () => {
  it("tears down the hub socket the previous user's token opened, and opens one for the next", async () => {
    renderTheApp();
    await waitFor(() => {
      expect(mintedFor).toEqual([ADMIN.id]);
    });
    expect(sockets).toEqual([
      {
        protocols: ["nexara.token", `nexara.token.hub-${ADMIN.id}`],
        closed: false,
      },
    ]);

    act(() => {
      // As a refresh answered for the viewer does: their token, then their
      // identity (api-client refreshTokens, then auth-store).
      storeTokens(authResponse(VIEWER));
      useAuthStore.getState().setAuthFromResponse(authResponse(VIEWER));
    });

    await waitFor(() => {
      expect(mintedFor).toEqual([ADMIN.id, VIEWER.id]);
    });
    await flush();
    expect(sockets).toEqual([
      {
        protocols: ["nexara.token", `nexara.token.hub-${ADMIN.id}`],
        closed: true,
      },
      {
        protocols: ["nexara.token", `nexara.token.hub-${VIEWER.id}`],
        closed: false,
      },
    ]);
  });

  it("control: a refresh for the same user, new name, role and permissions, keeps the socket it has", async () => {
    renderTheApp();
    await waitFor(() => {
      expect(mintedFor).toEqual([ADMIN.id]);
    });

    act(() => {
      // The same id as a later refresh finds them: only what is said about them
      // has changed, so who is signed in has not.
      useAuthStore
        .getState()
        .setAuthFromResponse(
          authResponse(
            { ...ADMIN, display_name: "Admin (renamed)", role: "user" },
            { permissions: ["a:b"] },
          ),
        );
    });
    await flush();
    await flush();

    expect(mintedFor).toEqual([ADMIN.id]);
    expect(sockets).toHaveLength(1);
    expect(sockets[0]?.closed).toBe(false);
  });
});
