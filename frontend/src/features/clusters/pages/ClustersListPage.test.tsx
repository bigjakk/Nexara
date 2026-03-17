import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
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
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
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
    created_at: "2024-02-01T00:00:00Z",
    updated_at: "2024-02-01T00:00:00Z",
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
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
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

describe("ClustersListPage", () => {
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
  });

  it("shows online/inactive badges", () => {
    mockUseClusters.mockReturnValue({
      data: mockClusters,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClustersListPage />);
    expect(screen.getByText("Online")).toBeInTheDocument();
    expect(screen.getByText("Inactive")).toBeInTheDocument();
  });

  it("shows empty state when no clusters", () => {
    mockUseClusters.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
    });

    renderWithProviders(<ClustersListPage />);
    expect(
      screen.getByText("No clusters configured. Add one to get started."),
    ).toBeInTheDocument();
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
