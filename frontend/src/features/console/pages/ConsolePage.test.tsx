import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { useConsoleStore } from "@/stores/console-store";
import { makeCluster } from "@/test/cluster.fixtures";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import { vncTab } from "../console.fixtures";
import { resetConsoleStore } from "../console.mocks";
import { ConsolePage } from "./ConsolePage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// A grant made in one test must not carry into the next.
afterEach(() => {
  grantPermissions();
});

// The open sessions are rows the floating window's own tabs are read from:
// the summary counts them, and a row's Close takes its session away.

describe("ConsolePage", () => {
  beforeEach(() => {
    resetConsoleStore();
    vi.mocked(apiClient.list).mockResolvedValue([
      makeCluster({ id: "cluster-1", name: "cluster01" }),
    ]);
  });

  // New console lists guests only: a role with no guest console got an empty
  // picker, and the page told it to open one.
  it.each([
    [
      ["console:vm"],
      "Open one here, or from a guest's or a node's own page.",
      true,
    ],
    [["console:node"], "Open one from a node's own page.", false],
    [["view:vm"], "Your role cannot open a console.", false],
  ])("with %j says %s", async (permissions, text, picker) => {
    grantPermissions(...permissions);
    renderWithProviders(<ConsolePage />);
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /New console/ }) !== null).toBe(
      picker,
    );
  });

  it("lists the open sessions and closes one from its row", async () => {
    useConsoleStore.setState({
      tabs: [vncTab({ status: "connected" })],
      activeTabId: "vnc-tab-1",
    });
    renderWithProviders(<ConsolePage />);

    expect(
      await screen.findByText(/1 session open, 1 connected/),
    ).toBeInTheDocument();
    expect(
      screen.getByText("VNC display on cluster01 / pve1"),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Close VNC: linux11" }));
    expect(useConsoleStore.getState().tabs).toEqual([]);
    expect(screen.getByText("No console is open")).toBeInTheDocument();
  });
});
