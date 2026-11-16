import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { clearTokens } from "@/lib/api-client";
import { queryClient } from "@/lib/query-client";
import {
  ADMIN,
  authResponse,
  installFakeServer,
  json,
  type FakeServer,
  deferred,
  flush,
} from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import type { User } from "@/types/api";
import { SIGNED_OUT_KEY, useAuthStore } from "@/stores/auth-store";
import { useSignOutFromOtherTabs } from "@/hooks/useSignOutFromOtherTabs";
import { ProfilePage } from "./ProfilePage";

/**
 * The profile page's saves. Changing the password ends every session on the
 * server, which refuses this one from its next request, so the page signs the
 * user out at once and the login page says why.
 */

const CHANGE = "POST /api/v1/auth/change-password";

let server: FakeServer;

async function signInAs(user: User) {
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(user));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

beforeEach(() => {
  localStorage.clear();
  clearTokens();
  queryClient.clear();
  emptyPerSessionStores();
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
    signOutNotice: null,
  });
  server = installFakeServer();
  server.routes["POST /api/v1/auth/refresh"] = () => json({}, 401);
  server.routes["POST /api/v1/auth/logout"] = () =>
    new Response(null, { status: 204 });
  server.routes["GET /api/v1/auth/me"] = () =>
    json({
      id: ADMIN.id,
      email: ADMIN.email,
      display_name: "Admin",
      role: "admin",
      auth_source: "local",
      totp_enabled: false,
      created_at: "2026-01-01T00:00:00Z",
    });
  server.routes[CHANGE] = () => json({ message: "password changed" });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
});

type Clicker = ReturnType<typeof userEvent.setup>;

async function savesTheDisplayName(user: Clicker) {
  await user.type(await screen.findByLabelText("Display name"), "x");
  await user.click(screen.getByRole("button", { name: "Save changes" }));
}

async function savesThePassword(user: Clicker) {
  const [opens] = await screen.findAllByRole("button", {
    name: "Change password",
  });
  await user.click(opens as HTMLElement);
  await user.type(screen.getByLabelText("Current password"), "old-01");
  await user.type(screen.getByLabelText("New password"), "new-01");
  await user.type(screen.getByLabelText("Confirm new password"), "new-01");
  await user.click(screen.getByRole("button", { name: "Change password" }));
}

const refused = () => json({ message: "Refused for a reason" }, 400);
// A limiter's answer: plain text, and over HTTP/2 no status text either.
const limited = () =>
  new Response("Too Many Requests", { status: 429, statusText: "" });

describe("a save that fails", () => {
  // Shown on the page, and not toasted as well: the app's error toast cannot
  // see an onError given to mutate() (lib/query-client.ts).
  it.each([
    [
      "the display name",
      "PUT /api/v1/auth/profile",
      savesTheDisplayName,
      refused,
      "Refused for a reason",
    ],
    ["the password", CHANGE, savesThePassword, refused, "Refused for a reason"],
    [
      "the password, by a limiter",
      CHANGE,
      savesThePassword,
      limited,
      "HTTP 429",
    ],
  ])(
    "is reported once, on the page: %s",
    async (_name, route, save, answer, shown) => {
      const toastError = vi.spyOn(toast, "error");
      await signInAs(ADMIN);
      server.routes[route] = answer;
      render(
        <QueryClientProvider client={queryClient}>
          <ProfilePage />
        </QueryClientProvider>,
      );

      await save(userEvent.setup());

      expect(await screen.findByText(shown)).toBeInTheDocument();
      await flush();
      expect(toastError).not.toHaveBeenCalled();
    },
  );
});

it("keeps no password once the dialog is closed", async () => {
  // In the mutation's variables, which TanStack keeps until it is collected.
  await signInAs(ADMIN);
  server.routes[CHANGE] = refused;
  render(
    <QueryClientProvider client={queryClient}>
      <ProfilePage />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  await savesThePassword(user);
  await screen.findByText("Refused for a reason");

  await user.click(screen.getByRole("button", { name: "Cancel" }));
  await flush();

  expect(
    JSON.stringify(
      queryClient
        .getMutationCache()
        .getAll()
        .map((m) => m.state.variables),
    ),
  ).not.toContain("old-01");
});

describe("the sign-out after a password change", () => {
  function renderPage() {
    render(
      <QueryClientProvider client={queryClient}>
        <ProfilePage />
      </QueryClientProvider>,
    );
  }

  it("is at once, as the user's, with the reason for the login page", async () => {
    await signInAs(ADMIN);
    renderPage();

    await savesThePassword(userEvent.setup());

    await waitFor(() => {
      expect(useAuthStore.getState().isAuthenticated).toBe(false);
    });
    expect(useAuthStore.getState().signedOutByUser).toBe(true);
    expect(useAuthStore.getState().signOutNotice).toBe("passwordChanged");
  });

  it("is followed by the other tabs at once, as the user's own sign-out", async () => {
    await signInAs(ADMIN);
    renderPage();
    await savesThePassword(userEvent.setup());
    await waitFor(() => {
      expect(useAuthStore.getState().isAuthenticated).toBe(false);
    });
    const note = localStorage.getItem(SIGNED_OUT_KEY);
    // Another tab of the same user, listening, hears the note this one wrote.
    await signInAs(ADMIN);
    useAuthStore.setState({ sessionBeganAt: 0 });
    renderHook(() => {
      useSignOutFromOtherTabs();
    });
    const asked = server.times("POST /api/v1/auth/refresh");

    act(() => {
      window.dispatchEvent(
        new StorageEvent("storage", {
          key: SIGNED_OUT_KEY,
          newValue: note,
          storageArea: localStorage,
        }),
      );
    });

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().signedOutByUser).toBe(true);
    // Said there too, not just "signed out".
    expect(useAuthStore.getState().signOutNotice).toBe("passwordChanged");
    expect(server.times("POST /api/v1/auth/refresh")).toBe(asked);
  });

  it("happens though the dialog was closed before the answer", async () => {
    const answer = deferred<Response>();
    server.routes[CHANGE] = () => answer.promise;
    await signInAs(ADMIN);
    renderPage();
    const user = userEvent.setup();
    await savesThePassword(user);
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    answer.resolve(json({ message: "password changed" }));

    await waitFor(() => {
      expect(useAuthStore.getState().isAuthenticated).toBe(false);
    });
  });
});
