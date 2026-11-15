import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { clearTokens } from "@/lib/api-client";
import { queryClient } from "@/lib/query-client";
import {
  ADMIN,
  authResponse,
  installFakeServer,
  json,
  VIEWER,
  type FakeServer,
  flush,
} from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import type { User } from "@/types/api";
import { useAuthStore } from "@/stores/auth-store";
import { ProfilePage } from "./ProfilePage";

/**
 * Changing the password revokes every session on the server, and the page then
 * signs the user out locally after a short delay, so they see that it worked.
 * That delay is a timer that outlives the page, and the session it was started
 * for may be gone by the time it fires.
 */

const CHANGE = "POST /api/v1/auth/change-password";

let server: FakeServer;

/**
 * The page's 2 s timer, held: its callbacks wait for fire(), and clearTimeout
 * drops one as it would. Nothing else is held (TanStack and the DOM tests keep
 * their own timers), so the rest of the test runs on the real clock.
 */
function holdTheSignOutTimer() {
  const held = new Map<number, () => void>();
  let nextId = 1_000_000;
  const realSetTimeout = globalThis.setTimeout;
  const realClearTimeout = globalThis.clearTimeout;
  vi.spyOn(globalThis, "setTimeout").mockImplementation(((
    callback: () => void,
    ms?: number,
    ...args: unknown[]
  ) => {
    if (ms === 2000) {
      const id = nextId++;
      held.set(id, callback);
      return id;
    }
    return realSetTimeout(callback, ms, ...args);
  }) as unknown as typeof setTimeout);
  vi.spyOn(globalThis, "clearTimeout").mockImplementation((id) => {
    if (typeof id === "number" && held.delete(id)) return;
    realClearTimeout(id);
  });
  return {
    count: () => held.size,
    fire: () => {
      for (const [id, callback] of [...held]) {
        held.delete(id);
        callback();
      }
    },
  };
}

async function signInAs(user: User) {
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(user));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

/** Signs in, opens the page and changes the password, up to the timer. */
async function changesThePassword() {
  const timer = holdTheSignOutTimer();
  await signInAs(ADMIN);
  const user = userEvent.setup();
  const view = render(
    <QueryClientProvider client={queryClient}>
      <ProfilePage />
    </QueryClientProvider>,
  );
  const [opensTheDialog] = await screen.findAllByRole("button", {
    name: "Change password",
  });
  await user.click(opensTheDialog as HTMLElement);
  await user.type(screen.getByLabelText("Current password"), "old-secret-01");
  await user.type(screen.getByLabelText("New password"), "new-secret-01");
  await user.type(
    screen.getByLabelText("Confirm new password"),
    "new-secret-01",
  );
  await user.click(screen.getByRole("button", { name: "Change password" }));
  await screen.findByText(/Signing you out/);
  expect(timer.count()).toBe(1);
  return { timer, view };
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

describe("the local sign-out after a password change", () => {
  it("control: ends the session it was started for, as an expiry, once the delay is up", async () => {
    const { timer } = await changesThePassword();
    expect(useAuthStore.getState().isAuthenticated).toBe(true);

    act(() => {
      timer.fire();
    });

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
    expect(useAuthStore.getState().signedOutByUser).toBe(false);
  });

  it("still ends it when the page has been left in the meantime: the server revoked it either way", async () => {
    const { timer, view } = await changesThePassword();
    view.unmount();

    act(() => {
      timer.fire();
    });

    expect(useAuthStore.getState().isAuthenticated).toBe(false);
  });

  it("does not end the session of the user who signed in meanwhile", async () => {
    const { timer } = await changesThePassword();
    await act(async () => {
      await useAuthStore.getState().logout();
    });
    await signInAs(VIEWER);

    act(() => {
      timer.fire();
    });
    await flush();

    expect(useAuthStore.getState().isAuthenticated).toBe(true);
    expect(useAuthStore.getState().user?.id).toBe(VIEWER.id);
  });

  it("does not turn a Sign out in the meantime into an expiry: the next sign-in would be sent back to the page they left", async () => {
    const { timer } = await changesThePassword();
    await act(async () => {
      await useAuthStore.getState().logout();
    });
    expect(useAuthStore.getState().signedOutByUser).toBe(true);

    act(() => {
      timer.fire();
    });

    expect(useAuthStore.getState().signedOutByUser).toBe(true);
  });
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
