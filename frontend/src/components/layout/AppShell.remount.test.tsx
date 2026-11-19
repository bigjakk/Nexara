import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, useParams } from "react-router-dom";

import { AppRoot } from "@/components/AppRoot";
import { clearTokens } from "@/lib/api-client";
import { queryClient } from "@/lib/query-client";
import { useAuthStore } from "@/stores/auth-store";
import { useWebSocketStore } from "@/stores/websocket-store";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import { useChangelogStore } from "@/stores/changelog-store";
import {
  ADMIN,
  authResponse,
  installFakeServer,
  json,
  type FakeServer,
} from "@/test/fake-server";
import {
  flushInAct,
  renderOnAppClient,
  waitUntil,
} from "@/test/save-outcome-kit";
import { AppShell } from "./AppShell";

// A detail page is one route across many ids and holds dialogs, drafts and saves in local
// state not keyed on the id; left mounted, it would show or send them against the next id.
// The shell keys its error boundary on the pathname (AppShell.tsx), so a move to another id
// builds the page anew; no other test pins that second job of the key. These run the real
// shell around a page with one piece of local state.

function Page() {
  const { clusterId } = useParams();
  const [presses, setPresses] = useState(0);
  return (
    <button
      type="button"
      data-testid="page-state"
      onClick={() => {
        setPresses(presses + 1);
      }}
    >
      {`${clusterId ?? ""}|${String(presses)}`}
    </button>
  );
}

function renderShell(initial: string) {
  const router = createMemoryRouter(
    [
      {
        element: <AppShell />,
        children: [{ path: "clusters/:clusterId", element: <Page /> }],
      },
    ],
    { initialEntries: [initial] },
  );
  const { qc } = renderOnAppClient(<AppRoot router={router} />);
  return { router, qc };
}

function pageState(): string {
  return screen.getByTestId("page-state").textContent;
}

const socketActions = {
  connect: useWebSocketStore.getState().connect,
  disconnect: useWebSocketStore.getState().disconnect,
};

let server: FakeServer;

beforeEach(() => {
  server = installFakeServer();
  // The shell opens the live-event socket; this is about what it does to a
  // page, not about the socket.
  useWebSocketStore.setState({
    connect: () => undefined,
    disconnect: () => undefined,
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  useWebSocketStore.setState(socketActions);
});

describe("the app shell, when the route under it changes", () => {
  it("builds the page anew for the next id, and what it held is gone", async () => {
    const user = userEvent.setup();
    const { router } = renderShell("/clusters/cluster-a?tab=metric-servers");
    await user.click(await screen.findByTestId("page-state"));
    expect(pageState()).toBe("cluster-a|1");

    await act(async () => {
      await router.navigate("/clusters/cluster-b?tab=metric-servers");
    });

    expect(pageState()).toBe("cluster-b|0");
  });

  it("keeps the page when only the query string changes: choosing a tab is not a move", async () => {
    const user = userEvent.setup();
    const { router } = renderShell("/clusters/cluster-a");
    await user.click(await screen.findByTestId("page-state"));
    expect(pageState()).toBe("cluster-a|1");

    await act(async () => {
      await router.navigate("/clusters/cluster-a?tab=metric-servers");
    });

    expect(pageState()).toBe("cluster-a|1");
  });
});

describe("AppShell — Sign out all devices", () => {
  // It ended every session of the account at once, the moment it was chosen.
  it("asks before ending every session", async () => {
    const user = userEvent.setup();
    renderShell("/clusters/c1");
    await user.click(await screen.findByRole("button", { name: /User$/ }));
    await user.click(
      await screen.findByRole("menuitem", { name: "Sign out all devices" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Sign out all devices?",
    });
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(
      screen.queryByRole("dialog", { name: "Sign out all devices?" }),
    ).toBeNull();
  });
});

describe("AppShell — API keys in the user menu", () => {
  afterEach(() => {
    useAuthStore.setState({ permissions: [], globalPermissions: [] });
  });

  // The page and every route it calls take manage:api_key install-wide.
  it.each<[string, string[], boolean]>([
    ["on a cluster only", [], false],
    ["install-wide", ["manage:api_key"], true],
  ])(
    "with manage:api_key %s (install-wide: %j) offers API keys: %s",
    async (_held, globalPermissions, offered) => {
      useAuthStore.setState({
        permissions: ["manage:api_key"],
        globalPermissions,
      });
      const user = userEvent.setup();
      renderShell("/clusters/c1");
      await user.click(await screen.findByRole("button", { name: /User$/ }));
      await screen.findByRole("menuitem", { name: "Sign out all devices" });
      expect(
        screen.queryByRole("menuitem", { name: "API keys" }) !== null,
      ).toBe(offered);
    },
  );
});

describe("AppShell — What's new", () => {
  // Signed in from scratch: the earlier tests leave a session and caches behind.
  beforeEach(() => {
    localStorage.clear();
    clearTokens();
    queryClient.clear();
    emptyPerSessionStores();
  });
  afterEach(() => {
    useAuthStore.setState(useAuthStore.getInitialState(), true);
  });

  // A version with no release in the list yet was marked seen at once, so its popup
  // never showed: the release is created after the image is pushed, and the list is
  // cached for an hour. Opened from the menu meanwhile, the dialog shows an older
  // release, and closing it marked the version too. The second row is the control.
  it.each([
    { released: ["1.17.2"], fromMenu: false, open: false },
    { released: ["1.18.0", "1.17.2"], fromMenu: false, open: true },
    { released: ["1.17.2"], fromMenu: true, open: false },
  ])(
    "on 1.18.0 with releases $released, from the menu $fromMenu",
    async ({ released, fromMenu, open }) => {
      useChangelogStore.setState({ lastSeenVersion: null });
      server.routes["POST /api/v1/auth/refresh"] = () =>
        json(authResponse(ADMIN));
      server.routes["GET /api/v1/version"] = () => json({ version: "v1.18.0" });
      server.routes["GET /api/v1/changelog"] = () =>
        json({
          items: released.map((version) => ({
            version,
            date: "2026-11-19",
            highlights: [{ title: `In ${version}` }],
          })),
          total: released.length,
        });
      const { qc } = renderShell("/clusters/c1");
      await screen.findByTestId("page-state");
      await waitUntil(() => {
        expect(qc.getQueryState(["changelog"])?.status).toBe("success");
      });
      await flushInAct();
      if (fromMenu) {
        const user = userEvent.setup();
        await user.click(screen.getByRole("button", { name: /User$/ }));
        await user.click(
          await screen.findByRole("menuitem", { name: "What's new" }),
        );
        await screen.findByRole("dialog", { name: /What's new/ });
        await user.keyboard("{Escape}");
        await flushInAct();
      }
      expect(
        screen.queryByRole("dialog", { name: /What's new/ }) !== null,
      ).toBe(open);
      expect(useChangelogStore.getState().lastSeenVersion).toBeNull();
    },
  );
});
