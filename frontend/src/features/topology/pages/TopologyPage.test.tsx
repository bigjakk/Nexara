import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { makeCluster, makeNode } from "@/test/cluster.fixtures";
import { upsDevice, upsFeed } from "@/features/power/power.fixtures";
import { TopologyPage } from "./TopologyPage";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());
// One map for every render: the canvas re-injects live metrics whenever it changes.
vi.mock("@/hooks/useMetrics", () => {
  const none = new Map();
  return { useDashboardMetrics: () => none };
});

const upsListed = () => vi.mocked(apiClient.list).mock.calls.some(([p]) => String(p).includes("ups-devices"));

describe("TopologyPage power overlay", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.endsWith("/clusters")) return Promise.resolve([makeCluster({ name: "cluster01" })]);
      if (p.endsWith("/nodes")) return Promise.resolve([makeNode({ id: "n1", name: "pve-01" }), makeNode({ id: "n2", name: "pve-02" })]);
      if (p.includes("ups-devices")) return Promise.resolve([upsDevice({ feeds: [upsFeed({ protection: "not_protected" })] })]);
      return Promise.resolve([]);
    });
  });

  it("draws each UPS and marks the hosts no upsmon would shut down once Power is on", async () => {
    useAuthStore.setState({ globalPermissions: ["view:ups"], permissions: ["view:ups"] });
    const user = setupUser();
    renderWithProviders(<TopologyPage />);

    await user.click(await screen.findByRole("checkbox", { name: "Power" }));

    expect(await screen.findByText("ups01")).toBeInTheDocument();
    expect(screen.getByText("No upsmon")).toBeInTheDocument();

    // A re-layout (here a new direction; the clusters refetch does it every
    // 30 s) drops the overlay, which must be applied again after it.
    await user.selectOptions(screen.getByDisplayValue("Top-Down"), "LR");
    expect(await screen.findByText("ups01")).toBeInTheDocument();
    expect(screen.getByText("No upsmon")).toBeInTheDocument();
  });

  it("offers no Power overlay, and lists no UPS, without view:ups", async () => {
    useAuthStore.setState({ globalPermissions: [], permissions: [] });
    renderWithProviders(<TopologyPage />);

    expect(await screen.findByText("pve-01")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: "Power" })).not.toBeInTheDocument();
    expect(upsListed()).toBe(false);
  });
});
