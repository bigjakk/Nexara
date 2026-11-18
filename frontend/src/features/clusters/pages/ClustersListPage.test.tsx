import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import { ClustersListPage } from "./ClustersListPage";
import type { ClusterResponse } from "@/types/api";

const mockClusters: ClusterResponse[] = [
  {
    id: "c1",
    name: "Prod Cluster",
    api_url: "https://pve1.example.com:8006",
    token_id: "root@pam!token",
    tls_fingerprint: "",
    sync_interval_seconds: 60,
    is_active: true,
    status: "online",
    pve_version: "",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    credential_source: "manual",
  },
  {
    id: "c2",
    name: "Dev Cluster",
    api_url: "https://pve2.example.com:8006",
    token_id: "root@pam!dev",
    tls_fingerprint: "",
    sync_interval_seconds: 120,
    is_active: false,
    status: "inactive",
    pve_version: "",
    created_at: "2024-02-01T00:00:00Z",
    updated_at: "2024-02-01T00:00:00Z",
    credential_source: "manual",
  },
];

const mockUseClusters = vi.fn();

vi.mock("@/features/dashboard/api/dashboard-queries", () => ({
  useClusters: (...args: unknown[]) => mockUseClusters(...args) as unknown,
  useCreateCluster: () => ({
    mutate: vi.fn(),
    isPending: false,
    error: null,
    data: null,
    reset: vi.fn(),
  }),
}));

// Mock useQueries to return empty arrays for node/vm queries
vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQueries: () => [] as unknown[],
  };
});

beforeEach(() => {
  vi.clearAllMocks();
});

// A grant made in one test must not carry into the next.
afterEach(() => {
  grantPermissions();
});

describe("ClustersListPage", () => {
  // As on the cluster's page: Edit takes manage:cluster, Delete delete:cluster;
  // a Viewer got both on every row, each answering 403.
  it.each([
    [["manage:cluster"], true, false],
    [["delete:cluster"], false, true],
    [["view:cluster"], false, false],
  ])("with %j offers Edit %s and Delete %s", (permissions, edit, del) => {
    grantPermissions(...permissions);
    mockUseClusters.mockReturnValue({
      data: mockClusters,
      isLoading: false,
      error: null,
    });
    renderWithProviders(<ClustersListPage />);
    const has = (name: string) =>
      screen.queryByRole("button", { name }) !== null;
    expect([has("Edit Prod Cluster"), has("Delete Prod Cluster")]).toEqual([
      edit,
      del,
    ]);
  });

  it("renders cluster names in table", () => {
    mockUseClusters.mockReturnValue({
      data: mockClusters,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClustersListPage />);
    expect(screen.getByText("Clusters")).toBeInTheDocument();
    expect(screen.getByText("Prod Cluster")).toBeInTheDocument();
    expect(screen.getByText("Dev Cluster")).toBeInTheDocument();
    // The summary counts what has been read: the clusters and their states.
    expect(screen.getByText("2 clusters, 1 inactive")).toBeInTheDocument();
  });

  it("names a cluster's state only when it is not online", () => {
    mockUseClusters.mockReturnValue({
      data: mockClusters,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClustersListPage />);
    // Online is the state dot alone, named for a screen reader.
    expect(screen.getByRole("img", { name: "Online" })).toBeInTheDocument();
    expect(screen.queryByText("Online")).not.toBeInTheDocument();
    expect(screen.getByText("Inactive")).toBeInTheDocument();
  });

  it("shows empty state when no clusters", () => {
    grantPermissions("manage:cluster");
    mockUseClusters.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClustersListPage />);
    expect(
      screen.getByText("No clusters configured. Add one to get started."),
    ).toBeInTheDocument();
    // The empty state's, not a second one in the header.
    expect(
      screen.getAllByRole("button", { name: /add cluster/i }),
    ).toHaveLength(1);
  });

  it("renders links to cluster detail pages", () => {
    mockUseClusters.mockReturnValue({
      data: mockClusters,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClustersListPage />);
    const link = screen.getByTestId("cluster-link-c1");
    expect(link).toHaveAttribute("href", "/clusters/c1");
  });

  it("shows error state", () => {
    mockUseClusters.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("Network error"),
    });

    renderWithProviders(<ClustersListPage />);
    expect(screen.getByText("Failed to load clusters.")).toBeInTheDocument();
  });
});
