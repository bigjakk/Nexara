import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ActiveSessionsCard } from "./ActiveSessionsCard";
import { useAuthStore } from "@/stores/auth-store";
import type { UserSession } from "@/types/api";

const USER_A = "aaaaaaaa-0000-0000-0000-000000000001";
const USER_B = "bbbbbbbb-0000-0000-0000-000000000002";

function signIn(id: string) {
  useAuthStore.setState({
    user: { id, email: "u@example.com", display_name: "U", role: "admin" },
    isAuthenticated: true,
  });
}

const listMock = vi.fn();
const deleteMock = vi.fn();

vi.mock("@/lib/api-client", () => ({
  apiClient: {
    list: (path: string) => listMock(path) as unknown,
    delete: (path: string) => deleteMock(path) as unknown,
  },
}));

function session(overrides: Partial<UserSession> = {}): UserSession {
  return {
    id: "11111111-1111-1111-1111-111111111111",
    device_name: "",
    device_type: "web",
    user_agent: "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Firefox/130.0",
    ip_address: "192.0.2.10",
    created_at: new Date().toISOString(),
    last_used_at: new Date().toISOString(),
    expires_at: new Date(Date.now() + 3_600_000).toISOString(),
    is_current: false,
    ...overrides,
  };
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
}

function renderCard(client: QueryClient = makeClient()) {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, ...render(<ActiveSessionsCard />, { wrapper }) };
}

beforeEach(() => {
  listMock.mockReset();
  deleteMock.mockReset();
  deleteMock.mockResolvedValue({ message: "Session revoked" });
  signIn(USER_A);
});

describe("ActiveSessionsCard", () => {
  it("renders each session with its address", async () => {
    listMock.mockResolvedValue([
      session({ device_name: "Ops laptop" }),
      session({ id: "22222222-2222-2222-2222-222222222222" }),
    ]);
    renderCard();

    expect(await screen.findByText("Ops laptop")).toBeInTheDocument();
    expect(screen.getByText("Firefox on Linux")).toBeInTheDocument();
    expect(screen.getAllByText(/192\.0\.2\.10/)).toHaveLength(2);
  });

  it("revokes another device immediately, without confirming", async () => {
    listMock.mockResolvedValue([session()]);
    renderCard();

    await userEvent.click(
      await screen.findByRole("button", { name: "Revoke" }),
    );

    await waitFor(() => {
      expect(deleteMock).toHaveBeenCalledWith(
        "/api/v1/auth/sessions/11111111-1111-1111-1111-111111111111",
      );
    });
  });

  // Revoking the session you are holding signs you out on the spot, so it has
  // to clear the same confirmation bar as every other disruptive action. A
  // regression here logs the operator out on a stray click.
  it("confirms before signing out the current device", async () => {
    listMock.mockResolvedValue([session({ is_current: true })]);
    renderCard();

    expect(await screen.findByText("This device")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Revoke" }));

    expect(
      await screen.findByText("Sign out this device?"),
    ).toBeInTheDocument();
    expect(deleteMock).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Sign out" }));
    await waitFor(() => {
      expect(deleteMock).toHaveBeenCalledOnce();
    });
  });

  it("cancelling the confirmation revokes nothing", async () => {
    listMock.mockResolvedValue([session({ is_current: true })]);
    renderCard();

    await userEvent.click(
      await screen.findByRole("button", { name: "Revoke" }),
    );
    await userEvent.click(
      await screen.findByRole("button", { name: "Cancel" }),
    );

    expect(deleteMock).not.toHaveBeenCalled();
  });

  it("surfaces a failed revoke", async () => {
    listMock.mockResolvedValue([session()]);
    deleteMock.mockRejectedValue(new Error("Session not found"));
    renderCard();

    await userEvent.click(
      await screen.findByRole("button", { name: "Revoke" }),
    );

    expect(await screen.findByText("Session not found")).toBeInTheDocument();
  });

  it("renders an empty state rather than a bare card", async () => {
    listMock.mockResolvedValue([]);
    renderCard();

    expect(await screen.findByText("No active sessions.")).toBeInTheDocument();
    // Without this the test passes vacuously whenever the query is disabled —
    // a disabled query also yields no rows, and that is how the user-scoped
    // key first broke this file.
    expect(listMock).toHaveBeenCalledWith("/api/v1/auth/sessions");
  });

  // Nothing clears the QueryClient on logout, and the client caches for five
  // minutes, so a key shared between users would let whoever signs in next on
  // this browser read the previous user's device names and IP addresses out of
  // cache with no refetch. The key carries the user id to prevent that.
  it("does not serve one user's sessions to the next user on the same client", async () => {
    const client = makeClient();
    listMock.mockResolvedValue([session({ device_name: "A laptop" })]);
    const first = renderCard(client);
    expect(await screen.findByText("A laptop")).toBeInTheDocument();
    first.unmount();

    // Same QueryClient, different user — as after a logout/login in one tab.
    signIn(USER_B);
    listMock.mockResolvedValue([session({ device_name: "B laptop" })]);
    renderCard(client);

    expect(await screen.findByText("B laptop")).toBeInTheDocument();
    expect(screen.queryByText("A laptop")).not.toBeInTheDocument();
  });
});
