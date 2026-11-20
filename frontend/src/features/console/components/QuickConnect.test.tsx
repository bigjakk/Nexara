import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { useConsoleStore } from "@/stores/console-store";
import { makeCluster, makeNode, makeVM } from "@/test/cluster.fixtures";
import { resetConsoleStore } from "../console.mocks";
import { QuickConnect } from "./QuickConnect";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

describe("QuickConnect", () => {
  // A halted VM is "running" to Proxmox; its dot said Running.
  it("shows a halted VM's state, not running", async () => {
    useAuthStore.setState({ permissions: ["console:vm"] });
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.endsWith("/clusters")) return Promise.resolve([makeCluster()]);
      if (p.endsWith("/nodes")) return Promise.resolve([makeNode()]);
      if (p.endsWith("/vms")) {
        return Promise.resolve([
          makeVM({ name: "linux01", status: "running", run_state: "io-error" }),
        ]);
      }
      return Promise.resolve([]);
    });
    renderWithProviders(<QuickConnect />);

    await userEvent.click(
      await screen.findByRole("button", { name: "New console" }),
    );

    await screen.findByText("linux01");
    expect(screen.getByRole("img", { name: "I/O error" })).toBeInTheDocument();
  });

  // Nodes are listed beside guests, and the search narrows both.
  it("searches the list and opens a node's shell", async () => {
    resetConsoleStore();
    useAuthStore.setState({ permissions: ["console:node", "console:vm"] });
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.endsWith("/clusters")) return Promise.resolve([makeCluster()]);
      if (p.endsWith("/nodes")) {
        return Promise.resolve([makeNode({ name: "pve-01" })]);
      }
      if (p.endsWith("/vms")) {
        return Promise.resolve([
          makeVM({ id: "g1", vmid: 100, name: "linux01" }),
          makeVM({ id: "g2", vmid: 101, name: "win01" }),
        ]);
      }
      return Promise.resolve([]);
    });
    renderWithProviders(<QuickConnect />);
    await userEvent.click(
      await screen.findByRole("button", { name: "New console" }),
    );
    await screen.findByText("linux01");

    const search = screen.getByRole("textbox", { name: "Search" });
    await userEvent.type(search, "win");
    expect(screen.queryByText("linux01")).not.toBeInTheDocument();
    expect(screen.queryByText("pve-01")).not.toBeInTheDocument();
    expect(screen.getByText("win01")).toBeInTheDocument();

    await userEvent.clear(search);
    await userEvent.click(screen.getByRole("button", { name: /Shell/ }));
    expect(useConsoleStore.getState().tabs).toMatchObject([
      { type: "node_shell", node: "pve-01", label: "Shell: pve-01" },
    ]);
  });
});
