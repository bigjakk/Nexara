import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { makeCluster, makeNode, makeVM } from "@/test/cluster.fixtures";
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
});
