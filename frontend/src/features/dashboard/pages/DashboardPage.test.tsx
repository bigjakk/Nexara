import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { makeVM } from "@/test/cluster.fixtures";
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
        status: "online",
        pve_version: "",
        created_at: "2024-01-01T00:00:00Z",
        updated_at: "2024-01-01T00:00:00Z",
        credential_source: "manual",
      },
      nodeCount: 3,
      nodesOnline: 3,
      vmCount: 10,
      vmsRunning: 8,
      containerCount: 5,
      containersRunning: 4,
      storageTotalBytes: 1099511627776,
      storageUsedBytes: 549755813888,
    },
  ],
  totalNodes: 3,
  totalNodesOnline: 3,
  totalVMs: 10,
  totalVMsRunning: 8,
  totalContainers: 5,
  totalContainersRunning: 4,
  totalStorageBytes: 1099511627776,
  totalStorageUsedBytes: 549755813888,
  vmNameMap: new Map(),
  nodesByCluster: new Map(),
  vmsByCluster: new Map(),
};

const mockUseDashboardData = vi.fn();

vi.mock("@/hooks/useHealthIssues", () => ({
  useHealthIssues: () => ({ visible: [], dismiss: vi.fn(), mute: vi.fn() }),
}));

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
  useSeedMetricsForClusters: () => new Map(),
}));

vi.mock("react-grid-layout", async () => {
  const React = await import("react");
  const actual =
    await vi.importActual<typeof import("react-grid-layout")>(
      "react-grid-layout",
    );
  return {
    getBreakpointFromWidth: actual.getBreakpointFromWidth,
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

interface SettingState {
  data?: { value: unknown };
  isPending?: boolean;
}
const settings = new Map<string, SettingState>();
const mockMutate = vi.fn();
/** The settings each save wrote, one array per mutation. */
const saves = () =>
  mockMutate.mock.calls.map(([w]) => w as { key: string; value: unknown }[]);

vi.mock("@/features/settings/api/settings-queries", () => ({
  useSetting: (key: string) => ({
    data: undefined,
    isPending: false,
    error: null,
    ...settings.get(key),
  }),
  useUpsertSettings: () => ({
    mutate: mockMutate,
    isPending: false,
  }),
}));

beforeEach(() => {
  vi.clearAllMocks();
  settings.clear();
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

  it("names each cluster when data is loaded", () => {
    mockUseDashboardData.mockReturnValue({
      data: mockDashboardData,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<DashboardPage />);
    expect(
      screen.getByRole("link", { name: "Prod Cluster" }),
    ).toBeInTheDocument();
  });

  it("summarises the estate under the title, leaving templates out", () => {
    mockUseDashboardData.mockReturnValue({
      data: {
        ...mockDashboardData,
        vmsByCluster: new Map([
          [
            "c1",
            [
              makeVM({ id: "v1", name: "linux01" }),
              makeVM({ id: "v2", name: "linux02", status: "stopped" }),
              makeVM({ id: "v3", name: "linux03", template: true }),
            ],
          ],
        ]),
      },
      isLoading: false,
      error: null,
    });

    renderWithProviders(<DashboardPage />);
    expect(
      screen.getByText("1 cluster, 3 nodes, 2 guests, 1 running"),
    ).toBeInTheDocument();
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

  it("shows CPU usage chart title for first cluster", () => {
    mockUseDashboardData.mockReturnValue({
      data: mockDashboardData,
      isLoading: false,
      error: null,
    });

    renderWithProviders(<DashboardPage />);
    expect(screen.getByText("Prod Cluster — CPU usage")).toBeInTheDocument();
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

  describe("the layout a reload opens on", () => {
    beforeEach(() => {
      mockUseDashboardData.mockReturnValue({
        data: mockDashboardData,
        isLoading: false,
        error: null,
      });
    });

    // A preset saved from the layout menu was only written as the layout
    // once Lock was pressed, so a reload went back to the old one. Both
    // writes go as one save, so a failure is reported once.
    it("saves a new preset as the layout too, in one save", async () => {
      const user = userEvent.setup();
      renderWithProviders(<DashboardPage />);
      await user.click(screen.getByRole("button", { name: "Layout: Default" }));
      await user.click(screen.getByText("Save current as preset"));
      await user.type(screen.getByPlaceholderText("Preset name"), "Mine");
      await user.click(screen.getByRole("button", { name: "Save" }));

      expect(saves()).toEqual([
        expect.arrayContaining([
          expect.objectContaining({
            key: "dashboard.layout",
            value: expect.objectContaining({ name: "Mine" }) as unknown,
          }),
        ]),
      ]);
    });

    // The clusters can answer before the saved layout does; the default
    // must not be chosen in that gap and kept for good.
    it("waits for the saved layout before falling back to the default", () => {
      settings.set("dashboard.layout", { isPending: true });
      const view = renderWithProviders(<DashboardPage />);
      settings.set("dashboard.layout", {
        data: {
          value: {
            name: "Mine",
            widgetIds: ["top-consumers"],
            layouts: [{ i: "top-consumers", x: 0, y: 0, w: 12, h: 5 }],
          },
        },
      });
      view.rerender(<DashboardPage />);

      expect(
        screen.getByRole("button", { name: "Layout: Mine" }),
      ).toBeInTheDocument();
    });
  });

  // A saved layout keeps its arrangement: what the default has and it lacks
  // is offered, once, and goes in only when asked for.
  describe("the offer of widgets a saved layout lacks", () => {
    beforeEach(() => {
      mockUseDashboardData.mockReturnValue({
        data: mockDashboardData,
        isLoading: false,
        error: null,
      });
      settings.set("dashboard.layout", {
        data: {
          value: {
            name: "Mine",
            widgetIds: ["top-consumers"],
            layouts: [{ i: "top-consumers", x: 0, y: 0, w: 12, h: 5 }],
          },
        },
      });
      settings.set("dashboard.offered_widgets", { data: { value: [] } });
    });

    it.each([
      { button: "Add to my layout", layoutSaved: true },
      { button: "Dismiss", layoutSaved: false },
    ])("$button records the offer and closes it", async (c) => {
      const user = userEvent.setup();
      renderWithProviders(<DashboardPage />);
      expect(screen.getByText(/does not have Fleet/)).toBeInTheDocument();
      expect(mockMutate).not.toHaveBeenCalled();

      await user.click(screen.getByRole("button", { name: c.button }));

      expect(screen.queryByText(/does not have Fleet/)).not.toBeInTheDocument();
      const [save, ...more] = saves();
      expect(more).toEqual([]);
      expect(save).toContainEqual(
        expect.objectContaining({
          key: "dashboard.offered_widgets",
          value: expect.arrayContaining(["fleet-board"]) as unknown,
        }),
      );
      expect(save?.some((w) => w.key === "dashboard.layout")).toBe(
        c.layoutSaved,
      );
    });
    // A widget removed on purpose and locked away is not announced as new
    // on the next visit.
    it("does not offer a default widget the person removed and locked", async () => {
      settings.set("dashboard.layout", {
        data: {
          value: {
            name: "Mine",
            widgetIds: ["fleet-board", "top-consumers"],
            layouts: [
              { i: "fleet-board", x: 0, y: 0, w: 12, h: 10 },
              { i: "top-consumers", x: 0, y: 10, w: 12, h: 5 },
            ],
          },
        },
      });
      const user = userEvent.setup();
      const view = renderWithProviders(<DashboardPage />);
      await user.click(screen.getByRole("button", { name: "Customize" }));
      await user.click(screen.getByRole("button", { name: "Remove Fleet" }));
      await user.click(screen.getByRole("button", { name: "Lock" }));

      // Reload on what was written.
      view.unmount();
      for (const w of saves().flat()) settings.set(w.key, { data: w });
      renderWithProviders(<DashboardPage />);
      expect(screen.queryByText(/does not have Fleet/)).not.toBeInTheDocument();
    });
  });
});
