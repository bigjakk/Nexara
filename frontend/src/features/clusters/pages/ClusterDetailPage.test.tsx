import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { makeCluster, makeNode } from "@/test/cluster.fixtures";
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
  // Consumed by ClusterCertificateBanner, which the page renders above the
  // tabs. Inert here — the banner returns null unless the cluster carries the
  // certificate issue, which these fixtures do not.
  useVerifyClusterCertificate: () => ({ mutate: vi.fn(), isPending: false }),
}));

// Mock tab components to avoid pulling in their dependencies
vi.mock("../components/ClusterCephTab", () => ({
  ClusterCephTab: () => <div data-testid="ceph-tab">Ceph Tab</div>,
}));
vi.mock("../components/ClusterNetworksTab", () => ({
  ClusterNetworksTab: () => <div data-testid="networks-tab">Networks Tab</div>,
}));
vi.mock("../components/ClusterFirewallTab", () => ({
  ClusterFirewallTab: () => <div data-testid="firewall-tab">Firewall Tab</div>,
}));
vi.mock("../components/ClusterDRSTab", () => ({
  ClusterDRSTab: () => <div data-testid="drs-tab">DRS Tab</div>,
}));

const mockUseInventoryData = vi.fn();

vi.mock("@/features/inventory/api/inventory-queries", () => ({
  useInventoryData: () => mockUseInventoryData() as unknown,
}));

const inventory = (over: object = {}) => ({
  rows: [],
  isLoading: false,
  error: null,
  failedClusterIds: [],
  ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  mockUseInventoryData.mockReturnValue(inventory());
});

describe("ClusterDetailPage", () => {
  it("renders cluster name, nodes, and tab triggers", () => {
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
    expect(screen.getByText("64.0 GiB")).toBeInTheDocument();
    expect(screen.getByText("8.1.3")).toBeInTheDocument();
    expect(screen.getByText("10d 0h")).toBeInTheDocument();

    // Tab triggers should be present
    expect(screen.getByRole("tab", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Ceph" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Networks" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Firewall" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "DRS" })).toBeInTheDocument();
  });

  it("maps legacy ?tab=nodes deep links to the Overview tab", () => {
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
      data: [],
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClusterDetailPage />, {
      router: { initialEntries: ["/clusters/test-cluster-id?tab=nodes"] },
    });

    expect(
      screen.getByRole("tab", { name: "Overview", selected: true }),
    ).toBeInTheDocument();
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

  describe("the Nodes table's guests", () => {
    const guestRow = {
      id: "v1",
      type: "vm",
      name: "linux01",
      status: "running",
      clusterId: "test-cluster-id",
      nodeName: "pve-01",
      vmid: 100,
      template: false,
      cpuPercent: 12,
    };

    beforeEach(() => {
      mockUseCluster.mockReturnValue({
        data: makeCluster({ id: "test-cluster-id" }),
        isLoading: false,
        error: null,
      });
      mockUseClusterNodes.mockReturnValue({
        data: [makeNode({ id: "n1", name: "pve-01" })],
        isLoading: false,
        error: null,
      });
    });

    it("draws a node's guests as linked cells", () => {
      mockUseInventoryData.mockReturnValue(inventory({ rows: [guestRow] }));
      renderWithProviders(<ClusterDetailPage />);
      expect(
        screen.getByRole("link", { name: "100 linux01, running, CPU 12%" }),
      ).toHaveAttribute("href", "/inventory/vm/test-cluster-id/v1");
    });

    // An empty list is only "no guests" once the list has actually answered.
    it.each([
      { name: "still loading", over: { isLoading: true } },
      { name: "unreadable", over: { failedClusterIds: ["test-cluster-id"] } },
    ])(
      "does not claim a node is empty while the guest list is $name",
      ({ over }) => {
        mockUseInventoryData.mockReturnValue(inventory(over));
        renderWithProviders(<ClusterDetailPage />);
        expect(screen.getByText("pve-01")).toBeInTheDocument();
        expect(screen.queryByText("No guests")).toBeNull();
      },
    );
  });
});
