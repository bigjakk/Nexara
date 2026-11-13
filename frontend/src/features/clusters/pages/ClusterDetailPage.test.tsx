import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { makeCluster, makeNode } from "@/test/cluster.fixtures";
import { useAuthStore } from "@/stores/auth-store";
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
  // The Performance strip's storage figure. Not read here: the strip shows a
  // dash until it answers.
  useClusterStorage: () => ({ data: undefined }),
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
vi.mock("@/features/storage/components/ClusterStoresPanel", () => ({
  ClusterStoresPanel: (props: {
    clusterId: string;
    title: ReactNode;
    actions?: ReactNode;
  }) => (
    <section aria-label={`stores of ${props.clusterId}`}>
      {props.title}
      {props.actions}
    </section>
  ),
}));
vi.mock("@/features/backup/components/ClusterBackupTab", () => ({
  ClusterBackupTab: (props: { clusterId: string; canManage: boolean }) => (
    <section aria-label={`backup of ${props.clusterId}`}>
      Backup schedules
      {props.canManage && <button type="button">Add schedule</button>}
    </section>
  ),
}));
vi.mock("@/features/storage/components/AddStorageDialog", () => ({
  AddStorageDialog: (props: { clusterId: string }) => (
    <button type="button">Add storage to {props.clusterId}</button>
  ),
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
  useAuthStore.setState({ user: null, permissions: [] });
});

describe("ClusterDetailPage", () => {
  it("renders cluster name, nodes, and tab triggers", () => {
    mockUseCluster.mockReturnValue({
      data: makeCluster({ id: "test-cluster-id", name: "Test Cluster" }),
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
    expect(
      screen.getByRole("heading", { level: 1, name: "Test Cluster" }),
    ).toBeInTheDocument();
    // The facts line says the state in words, with the nodes behind it.
    expect(screen.getByText("Online")).toBeInTheDocument();
    expect(screen.getByText("1 of 1 nodes")).toBeInTheDocument();
    expect(screen.getByText("pve-node-1")).toBeInTheDocument();
    expect(screen.getByText("16 cores")).toBeInTheDocument();
    expect(screen.getByText("64.0 GiB")).toBeInTheDocument();
    expect(screen.getByText("8.1.3")).toBeInTheDocument();
    expect(screen.getByText("10d 0h")).toBeInTheDocument();
    // The Nodes panel's note is computed from the nodes listed.
    expect(screen.getByText("1 node on 8.1.3")).toBeInTheDocument();

    // Tab triggers should be present
    expect(screen.getByRole("tab", { name: "Overview" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Ceph" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Networks" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Firewall" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "DRS" })).toBeInTheDocument();
  });

  it("maps legacy ?tab=nodes deep links to the Overview tab", () => {
    mockUseCluster.mockReturnValue({
      data: makeCluster({ id: "test-cluster-id", name: "Test Cluster" }),
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

  // The server asks manage:cluster to edit a cluster and delete:cluster to
  // delete one: the header offers neither to a caller it would refuse.
  it("offers the header's Edit and Delete only with the permission each takes", () => {
    useAuthStore.setState({ user: null, permissions: ["manage:cluster"] });
    mockUseCluster.mockReturnValue({
      data: makeCluster({ id: "test-cluster-id" }),
      isLoading: false,
      error: null,
    });
    mockUseClusterNodes.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClusterDetailPage />);
    expect(
      screen.getByRole("button", { name: "Edit cluster" }),
    ).toBeInTheDocument();
    // Without delete:cluster or the History grants, More has nothing in it.
    expect(
      screen.queryByRole("button", { name: "More actions" }),
    ).not.toBeInTheDocument();
  });

  // Where Proxmox has Datacenter > Storage and > Backup; adding to either
  // takes its manage grant.
  const STORAGE = {
    tab: "Storage",
    region: "stores of test-cluster-id",
    title: "Storage",
    add: "Add storage to test-cluster-id",
  };
  const BACKUP = {
    tab: "Backup",
    region: "backup of test-cluster-id",
    title: "Backup schedules",
    add: "Add schedule",
  };
  it.each([
    { ...STORAGE, permissions: ["manage:storage"], canAdd: true },
    { ...STORAGE, permissions: ["manage:backup"], canAdd: false },
    { ...BACKUP, permissions: ["manage:backup"], canAdd: true },
    { ...BACKUP, permissions: ["manage:storage"], canAdd: false },
  ])(
    "?tab=$tab with $permissions shows its panel, with its Add: $canAdd",
    ({ tab, region, title, add, permissions, canAdd }) => {
      useAuthStore.setState({ user: null, permissions });
      mockUseCluster.mockReturnValue({
        data: makeCluster({ id: "test-cluster-id" }),
        isLoading: false,
        error: null,
      });
      mockUseClusterNodes.mockReturnValue({
        data: [],
        isLoading: false,
        error: null,
      });

      renderWithProviders(<ClusterDetailPage />, {
        router: {
          initialEntries: [
            `/clusters/test-cluster-id?tab=${tab.toLowerCase()}`,
          ],
        },
      });

      expect(
        screen.getByRole("tab", { name: tab, selected: true }),
      ).toBeInTheDocument();
      expect(screen.getByRole("region", { name: region })).toHaveTextContent(
        title,
      );
      expect(screen.queryByRole("button", { name: add }) !== null).toBe(canAdd);
    },
  );

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
