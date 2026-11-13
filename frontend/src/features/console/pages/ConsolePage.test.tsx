import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { useConsoleStore } from "@/stores/console-store";
import { makeCluster } from "@/test/cluster.fixtures";
import { renderWithProviders } from "@/test/test-utils";
import { vncTab } from "../console.fixtures";
import { resetConsoleStore } from "../console.mocks";
import { ConsolePage } from "./ConsolePage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// The open sessions are rows the floating window's own tabs are read from:
// the summary counts them, and a row's Close takes its session away.
describe("ConsolePage", () => {
  beforeEach(() => {
    resetConsoleStore();
    vi.mocked(apiClient.list).mockResolvedValue([
      makeCluster({ id: "cluster-1", name: "cluster01" }),
    ]);
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
