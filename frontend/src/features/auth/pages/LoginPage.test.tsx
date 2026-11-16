import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { clearTokens } from "@/lib/api-client";
import {
  ADMIN,
  installFakeServer,
  json,
  type FakeServer,
} from "@/test/fake-server";
import { useAuthStore } from "@/stores/auth-store";
import { LoginPage } from "./LoginPage";

/**
 * Once someone is signed in the login page sends them on to returnTo, which
 * comes from the URL. Only an in-app path is followed (lib/return-to.ts).
 */

let server: FakeServer;

function renderLoginAt(url: string) {
  const router = createMemoryRouter(
    [
      { path: "/login", element: <LoginPage /> },
      { path: "*", element: <p>somewhere in the app</p> },
    ],
    { initialEntries: [url] },
  );
  render(<RouterProvider router={router} />);
  return router;
}

function signedIn() {
  act(() => {
    useAuthStore.setState({
      user: ADMIN,
      permissions: [],
      isAuthenticated: true,
      isInitialized: true,
    });
  });
}

beforeEach(() => {
  clearTokens();
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
  server.routes["GET /api/v1/auth/setup-status"] = () =>
    json({ needs_setup: false });
  server.routes["GET /api/v1/auth/sso-status"] = () =>
    json({ enabled: false, providers: [] });
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
});

describe("LoginPage, once someone is signed in", () => {
  it("follows an in-app returnTo", async () => {
    signedIn();

    const router = renderLoginAt("/login?returnTo=%2Fclusters%2Fc1");

    expect(await screen.findByText("somewhere in the app")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/clusters/c1");
  });

  it.each([
    ["/\\evil.example.com", "%2F%5Cevil.example.com"],
    ["//evil.example.com", "%2F%2Fevil.example.com"],
    ["/<tab>/evil.example.com", "%2F%09%2Fevil.example.com"],
  ])("does not follow %s: it goes to the root", async (_what, encoded) => {
    signedIn();

    const router = renderLoginAt(`/login?returnTo=${encoded}`);

    expect(await screen.findByText("somewhere in the app")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/");
  });
});

describe("LoginPage, after a sign-out the server did not confirm", () => {
  it("says what may still be signed in", async () => {
    useAuthStore.setState({ signOutNotice: "signOutAllUnconfirmed" });

    renderLoginAt("/login");

    expect(await screen.findByRole("status")).toHaveTextContent(
      /other devices may still be signed in/,
    );
  });
});

describe("LoginPage, after a refused single sign-on", () => {
  it("says what the refusal's code means", async () => {
    renderLoginAt("/login?error=email_not_verified");

    expect(
      await screen.findByText(/has not verified your email address/),
    ).toBeInTheDocument();
  });

  // The query is anybody's: a crafted link must not put its words on the page.
  it("never shows the query's own text", async () => {
    const crafted = "Your account is locked. Call the help desk.";

    renderLoginAt(`/login?error=${encodeURIComponent(crafted)}`);

    expect(
      await screen.findByText(/single sign-on did not complete/),
    ).toBeInTheDocument();
    expect(document.body.textContent).not.toContain(crafted);
  });
});
