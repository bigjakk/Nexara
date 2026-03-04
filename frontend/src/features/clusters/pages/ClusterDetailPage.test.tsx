import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { ClusterDetailPage } from "./ClusterDetailPage";

vi.mock("react-router-dom", async () => {
  const actual =
    await vi.importActual<typeof import("react-router-dom")>(
      "react-router-dom",
    );
  return {
    ...actual,
    useParams: () => ({ clusterId: "test-cluster-id" }),
  };
});

const mockUseCluster = vi.fn();
const mockUseClusterNodes = vi.fn();

vi.mock("../api/cluster-queries", () => ({
  useCluster: (...args: unknown[]) => mockUseCluster(...args) as unknown,
  useClusterNodes: (...args: unknown[]) =>
    mockUseClusterNodes(...args) as unknown,
}));

beforeEach(() => {
  vi.clearAllMocks();
});

describe("ClusterDetailPage", () => {
  it("renders cluster name and nodes", () => {
    mockUseCluster.mockReturnValue({
      data: {
        id: "test-cluster-id",
        name: "Test Cluster",
        api_url: "https://pve.example.com:8006",
        token_id: "root@pam!token",
        tls_fingerprint: "",
        sync_interval_seconds: 60,
        is_active: true,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
      isLoading: false,
      error: null,
    });
    mockUseClusterNodes.mockReturnValue({
      data: [
        {
          id: "node-1",
          cluster_id: "test-cluster-id",
          name: "pve-node-1",
          status: "online",
          cpu_count: 16,
          mem_total: 68719476736,
          disk_total: 1099511627776,
          pve_version: "8.1.3",
          uptime: 864000,
          last_seen_at: "2024-01-01T00:00:00Z",
          created_at: "2024-01-01T00:00:00Z",
          updated_at: "2024-01-01T00:00:00Z",
        },
      ],
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClusterDetailPage />);
    expect(screen.getByText("Test Cluster")).toBeInTheDocument();
    expect(screen.getByText("pve-node-1")).toBeInTheDocument();
    expect(screen.getByText("16")).toBeInTheDocument();
    expect(screen.getByText("64.0 GB")).toBeInTheDocument();
    expect(screen.getByText("8.1.3")).toBeInTheDocument();
    expect(screen.getByText("10d 0h")).toBeInTheDocument();
  });

  it("shows error state", () => {
    mockUseCluster.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("Not found"),
    });
    mockUseClusterNodes.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClusterDetailPage />);
    expect(
      screen.getByText("Failed to load cluster data."),
    ).toBeInTheDocument();
  });
});
