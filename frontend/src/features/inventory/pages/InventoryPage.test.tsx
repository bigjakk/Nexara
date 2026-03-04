import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { InventoryPage } from "./InventoryPage";

// Mock the inventory queries hook
vi.mock("../api/inventory-queries", () => ({
  useInventoryData: vi.fn(),
}));

import { useInventoryData } from "../api/inventory-queries";

const mockUseInventoryData = vi.mocked(useInventoryData);

describe("InventoryPage", () => {
  it("renders loading state", () => {
    mockUseInventoryData.mockReturnValue({
      rows: [],
      isLoading: true,
      error: null,
    });
    renderWithProviders(<InventoryPage />);
    expect(screen.getByText("Inventory")).toBeInTheDocument();
  });

  it("renders error state", () => {
    mockUseInventoryData.mockReturnValue({
      rows: [],
      isLoading: false,
      error: new Error("Network error"),
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
    });
    renderWithProviders(<InventoryPage />);
    expect(
      screen.getByText("No resources found. Add a cluster to get started."),
    ).toBeInTheDocument();
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
          cpuPercent: 50,
          memPercent: 60,
        },
      ],
      isLoading: false,
      error: null,
    });
    renderWithProviders(<InventoryPage />);
    expect(screen.getByText("web-server")).toBeInTheDocument();
    expect(screen.getByText("1 resource total")).toBeInTheDocument();
  });
});
