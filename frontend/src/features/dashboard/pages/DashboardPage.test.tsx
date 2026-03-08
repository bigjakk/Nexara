import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { DashboardPage } from "./DashboardPage";
import type { DashboardData } from "../api/dashboard-queries";

const mockDashboardData: DashboardData = {
  clusters: [
    {
      cluster: {
        id: "c1",
        name: "Prod Cluster",
        api_url: "https://pve1.example.com:8006",
        token_id: "root@pam!token",
        tls_fingerprint: "",
        sync_interval_seconds: 60,
        is_active: true,
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
      },
      nodeCount: 3,
      vmCount: 10,
      containerCount: 5,
      storageTotalBytes: 1099511627776,
    },
  ],
  totalNodes: 3,
  totalVMs: 10,
  totalContainers: 5,
  totalStorageBytes: 1099511627776,
  vmNameMap: new Map(),
};

const mockUseDashboardData = vi.fn();

vi.mock("../api/dashboard-queries", () => ({
  useDashboardData: (...args: unknown[]) =>
    mockUseDashboardData(...args) as unknown,
  useCreateCluster: () => ({
    mutate: vi.fn(),
    isPending: false,
    error: null,
    data: null,
    reset: vi.fn(),
  }),
}));

vi.mock("../api/historical-queries", () => ({
  useHistoricalMetrics: () => ({
    data: undefined,
    isLoading: false,
    error: null,
  }),
  useSeedMetrics: () => undefined,
}));

vi.mock("react-grid-layout", async () => {
  const React = await import("react");
  return {
    ResponsiveGridLayout: ({ children }: { children: React.ReactNode }) =>
      React.createElement("div", { "data-testid": "grid-layout" }, children),
    useContainerWidth: () => ({
      width: 1200,
      mounted: true,
      containerRef: { current: null },
      measureWidth: () => undefined,
    }),
  };
});

vi.mock("@/features/settings/api/settings-queries", () => ({
  useSetting: () => ({
    data: undefined,
    isLoading: false,
    error: null,
  }),
  useUpsertSetting: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

describe("DashboardPage", () => {
  it("renders dashboard heading", () => {
    mockUseDashboardData.mockReturnValue({
      data: mockDashboardData,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<DashboardPage />);
    expect(screen.getByText("Dashboard")).toBeInTheDocument();
  });

  it("renders cluster cards when data is loaded", () => {
    mockUseDashboardData.mockReturnValue({
      data: mockDashboardData,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<DashboardPage />);
    expect(screen.getByText("Prod Cluster")).toBeInTheDocument();
  });

  it("shows empty state when no clusters", () => {
    mockUseDashboardData.mockReturnValue({
      data: {
        clusters: [],
        totalNodes: 0,
        totalVMs: 0,
        totalContainers: 0,
        totalStorageBytes: 0,
        vmNameMap: new Map(),
      },
      isLoading: false,
      error: null,
    });

    renderWithProviders(<DashboardPage />);
    expect(screen.getByText("No clusters registered")).toBeInTheDocument();
  });

  it("shows error message on error", () => {
    mockUseDashboardData.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("Network error"),
    });

    renderWithProviders(<DashboardPage />);
    expect(
      screen.getByText("Failed to load dashboard data. Please try again."),
    ).toBeInTheDocument();
  });

  it("shows CPU Usage chart title for first cluster", () => {
    mockUseDashboardData.mockReturnValue({
      data: mockDashboardData,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<DashboardPage />);
    expect(
      screen.getByText("Prod Cluster — CPU Usage"),
    ).toBeInTheDocument();
  });

  it("renders time range selector with all buttons enabled", () => {
    mockUseDashboardData.mockReturnValue({
      data: mockDashboardData,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<DashboardPage />);
    expect(screen.getByTestId("range-live")).not.toBeDisabled();
    expect(screen.getByTestId("range-1h")).not.toBeDisabled();
    expect(screen.getByTestId("range-7d")).not.toBeDisabled();
  });
});
