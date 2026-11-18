import { describe, it, expect, vi, afterEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import { InventoryPage } from "./InventoryPage";

// Mock the inventory queries hook
vi.mock("../api/inventory-queries", () => ({
  useInventoryData: vi.fn(),
}));

vi.mock("@/features/dashboard/api/dashboard-queries", () => ({
  useClusters: () => ({ data: [] }),
  useCreateCluster: () => ({
    mutate: vi.fn(),
    isPending: false,
    error: null,
    data: null,
    reset: vi.fn(),
  }),
}));

import { useInventoryData } from "../api/inventory-queries";

const mockUseInventoryData = vi.mocked(useInventoryData);

// A guest as the inventory reads it.
const row = {
  key: "c1:vm:1",
  id: "vm-1",
  type: "vm" as const,
  name: "linux01",
  status: "running" as const,
  clusterName: "cluster01",
  clusterId: "c1",
  nodeName: "pve-01",
  vmid: 100,
  cpuCount: 4,
  memTotal: 8589934592,
  diskTotal: 107374182400,
  uptime: 86400,
  tags: "",
  haState: "",
  pool: "",
  template: false,
  ostype: "",
  configOstype: "",
  cpuPercent: 50,
  memPercent: 60,
  diskReadBps: null,
  diskWriteBps: null,
  netInBps: null,
  netOutBps: null,
};

// A grant made in one test must not carry into the next.
afterEach(() => {
  grantPermissions();
});

describe("InventoryPage", () => {
  it("renders loading state", () => {
    mockUseInventoryData.mockReturnValue({
      rows: [],
      isLoading: true,
      error: null,
      failedClusterIds: [],
    });
    renderWithProviders(<InventoryPage />);
    expect(screen.getByText("Inventory")).toBeInTheDocument();
  });

  it("renders error state", () => {
    mockUseInventoryData.mockReturnValue({
      rows: [],
      isLoading: false,
      error: new Error("Network error"),
      failedClusterIds: [],
    });
    renderWithProviders(<InventoryPage />);
    expect(
      screen.getByText("Failed to load inventory data. Please try again."),
    ).toBeInTheDocument();
  });

  it("renders empty state when no resources", () => {
    mockUseInventoryData.mockReturnValue({
      rows: [],
      isLoading: false,
      error: null,
      failedClusterIds: [],
    });
    renderWithProviders(<InventoryPage />);
    expect(screen.getByText("No clusters registered")).toBeInTheDocument();
  });

  it("renders table when data is available", () => {
    mockUseInventoryData.mockReturnValue({
      rows: [
        {
          key: "c1:vm:1",
          id: "vm-1",
          type: "vm" as const,
          name: "web-server",
          status: "running" as const,
          clusterName: "Production",
          clusterId: "c1",
          nodeName: "node1",
          vmid: 100,
          cpuCount: 4,
          memTotal: 8589934592,
          diskTotal: 107374182400,
          uptime: 86400,
          tags: "",
          haState: "",
          pool: "",
          template: false,
          ostype: "",
          configOstype: "",
          cpuPercent: 50,
          memPercent: 60,
          diskReadBps: null,
          diskWriteBps: null,
          netInBps: null,
          netOutBps: null,
        },
      ],
      isLoading: false,
      error: null,
      failedClusterIds: [],
    });
    renderWithProviders(<InventoryPage />);
    expect(screen.getByText("web-server")).toBeInTheDocument();
    expect(screen.getByText("1 resource total")).toBeInTheDocument();
  });

  // Deploy is a clone, which takes manage on the template's kind; the
  // Templates tab offered it to a Viewer.
  it.each([
    [["manage:vm"], true],
    [["view:vm"], false],
  ])(
    "with %j offers Deploy on a template: %s",
    async (permissions, offered) => {
      grantPermissions(...permissions);
      mockUseInventoryData.mockReturnValue({
        rows: [{ ...row, template: true }],
        isLoading: false,
        error: null,
        failedClusterIds: [],
      });
      renderWithProviders(<InventoryPage />);
      await userEvent
        .setup()
        .click(screen.getByRole("tab", { name: /Templates/ }));
      expect(screen.queryByRole("button", { name: /Deploy/ }) !== null).toBe(
        offered,
      );
    },
  );

  it("says what the estate holds under the title", () => {
    mockUseInventoryData.mockReturnValue({
      rows: [
        row,
        { ...row, key: "c1:vm:2", id: "vm-2", status: "stopped" as const },
        { ...row, key: "c1:vm:3", id: "vm-3", template: true },
        {
          ...row,
          key: "c1:node:1",
          id: "n-1",
          type: "node" as const,
          status: "online" as const,
          vmid: null,
        },
      ],
      isLoading: false,
      error: null,
      failedClusterIds: [],
    });
    renderWithProviders(<InventoryPage />);
    expect(
      screen.getByText("2 guests on 1 node, 1 running, 1 template"),
    ).toBeInTheDocument();
  });

  it("shows healthy clusters' rows alongside the unreachable-cluster warning", () => {
    mockUseInventoryData.mockReturnValue({
      rows: [
        {
          key: "c1:vm:1",
          id: "vm-1",
          type: "vm" as const,
          name: "web-server",
          status: "running" as const,
          clusterName: "Production",
          clusterId: "c1",
          nodeName: "node1",
          vmid: 100,
          cpuCount: 4,
          memTotal: 8589934592,
          diskTotal: 107374182400,
          uptime: 86400,
          tags: "",
          haState: "",
          pool: "",
          template: false,
          ostype: "",
          configOstype: "",
          cpuPercent: 50,
          memPercent: 60,
          diskReadBps: null,
          diskWriteBps: null,
          netInBps: null,
          netOutBps: null,
        },
      ],
      isLoading: false,
      error: null,
      failedClusterIds: ["c2"],
    });
    renderWithProviders(<InventoryPage />);
    expect(
      screen.getByText(/Couldn't load inventory from/),
    ).toBeInTheDocument();
    expect(screen.getByText("web-server")).toBeInTheDocument();
  });

  it("warns about unreachable clusters instead of blanking the page", () => {
    mockUseInventoryData.mockReturnValue({
      rows: [],
      isLoading: false,
      error: null,
      failedClusterIds: ["c2"],
    });
    renderWithProviders(<InventoryPage />);
    expect(
      screen.getByText(/Couldn't load inventory from/),
    ).toBeInTheDocument();
    // The empty state must not claim "no clusters" while a cluster is
    // merely unreachable.
    expect(screen.queryByText("No clusters registered")).toBeNull();
  });
});
