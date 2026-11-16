import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, useParams } from "react-router-dom";

import { AppRoot } from "@/components/AppRoot";
import { useWebSocketStore } from "@/stores/websocket-store";
import { installFakeServer } from "@/test/fake-server";
import { renderOnAppClient } from "@/test/save-outcome-kit";
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
  renderOnAppClient(<AppRoot router={router} />);
  return router;
}

function pageState(): string {
  return screen.getByTestId("page-state").textContent;
}

const socketActions = {
  connect: useWebSocketStore.getState().connect,
  disconnect: useWebSocketStore.getState().disconnect,
};

beforeEach(() => {
  installFakeServer();
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
    const router = renderShell("/clusters/cluster-a?tab=metric-servers");
    await user.click(await screen.findByTestId("page-state"));
    expect(pageState()).toBe("cluster-a|1");

    await act(async () => {
      await router.navigate("/clusters/cluster-b?tab=metric-servers");
    });

    expect(pageState()).toBe("cluster-b|0");
  });

  it("keeps the page when only the query string changes: choosing a tab is not a move", async () => {
    const user = userEvent.setup();
    const router = renderShell("/clusters/cluster-a");
    await user.click(await screen.findByTestId("page-state"));
    expect(pageState()).toBe("cluster-a|1");

    await act(async () => {
      await router.navigate("/clusters/cluster-a?tab=metric-servers");
    });

    expect(pageState()).toBe("cluster-a|1");
  });
});
