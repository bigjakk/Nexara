import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { formatTimestamp } from "@/lib/format";
import { ApiClientError } from "@/lib/api-client";
import type { CephStatus } from "@/features/ceph/types/ceph";
import { ClusterCephTab } from "./ClusterCephTab";

const mockUseCephStatus = vi.fn();
const mockUseCephOSDs = vi.fn();
const mockUseCephPools = vi.fn();
const mockUseCephMonitors = vi.fn();
const mockUseCephFS = vi.fn();
const mockUseCephCrushRules = vi.fn();

vi.mock("@/features/ceph/api/ceph-queries", () => ({
  useCephStatus: (...args: unknown[]) => mockUseCephStatus(...args) as unknown,
  useCephOSDs: (...args: unknown[]) => mockUseCephOSDs(...args) as unknown,
  useCephPools: (...args: unknown[]) => mockUseCephPools(...args) as unknown,
  useCephMonitors: (...args: unknown[]) =>
    mockUseCephMonitors(...args) as unknown,
  useCephFS: (...args: unknown[]) => mockUseCephFS(...args) as unknown,
  useCephCrushRules: (...args: unknown[]) =>
    mockUseCephCrushRules(...args) as unknown,
}));

// Recharts needs a laid-out container jsdom never gives it, and the chart is
// not what these tests are about.
vi.mock("@/features/ceph/components/CephMetricsChart", () => ({
  CephMetricsChart: () => <div data-testid="ceph-metrics-chart" />,
}));

/**
 * A query result in whatever state the test needs. The defaults spell out the
 * whole settled-pending shape rather than only the fields the component reads
 * today, so a guard rewritten in terms of `status` or `isPending` still meets
 * a realistic fake instead of a silently falsy `undefined`.
 */
function queryResult(overrides: Record<string, unknown> = {}) {
  return {
    data: undefined,
    status: "pending",
    fetchStatus: "idle",
    isPending: true,
    isSuccess: false,
    isLoading: false,
    isFetching: false,
    isPaused: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    ...overrides,
  };
}

/** A query that failed with `error` and has no data to fall back on. */
function failedResult(error: unknown) {
  return queryResult({
    status: "error",
    isPending: false,
    isError: true,
    error,
  });
}

/** Puts the five non-status queries in a settled, empty state. */
function settleSupportingQueries() {
  const empty = queryResult({
    data: [],
    status: "success",
    isPending: false,
    isSuccess: true,
  });
  mockUseCephOSDs.mockReturnValue(empty);
  mockUseCephPools.mockReturnValue(empty);
  mockUseCephMonitors.mockReturnValue(empty);
  mockUseCephFS.mockReturnValue(empty);
  mockUseCephCrushRules.mockReturnValue(empty);
}

/** Fixed so the banner's rendered age is deterministic. */
const STALE_AT = new Date("2026-09-06T12:34:56").getTime();

const healthyStatus: CephStatus = {
  health: { status: "HEALTH_OK", checks: [] },
  pgmap: {
    bytes_used: 1024,
    bytes_avail: 2048,
    bytes_total: 3072,
    read_bytes_sec: 0,
    write_bytes_sec: 0,
    read_op_per_sec: 0,
    write_op_per_sec: 0,
    num_pgs: 129,
  },
  osdmap: {
    num_osds: 6,
    num_up_osds: 6,
    num_in_osds: 6,
    full: false,
    nearfull: false,
  },
  monmap: { num_mons: 3 },
};

function succeededResult(data: CephStatus) {
  return queryResult({
    data,
    status: "success",
    isPending: false,
    isSuccess: true,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  settleSupportingQueries();
});

describe("ClusterCephTab", () => {
  it("renders the skeleton grid while the status query is loading", () => {
    mockUseCephStatus.mockReturnValue(
      queryResult({
        isLoading: true,
        isFetching: true,
        fetchStatus: "fetching",
      }),
    );

    const { container } = renderWithProviders(
      <ClusterCephTab clusterId="test-cluster-id" />,
    );

    expect(container.querySelectorAll(".h-32")).toHaveLength(4);
  });

  it.each([404, 500, 502])(
    "shows the Ceph Not Available card with the server's message on a %i",
    (httpStatus) => {
      mockUseCephStatus.mockReturnValue(
        failedResult(
          new ApiClientError(httpStatus, {
            error: "internal_server_error",
            message: "Failed to connect to Proxmox",
          }),
        ),
      );

      renderWithProviders(<ClusterCephTab clusterId="test-cluster-id" />);

      expect(screen.getByText("Ceph Not Available")).toBeInTheDocument();
      // 404/500/502 all mean "no Ceph status", but mapProxmoxError gives an
      // unreachable cluster and a Proxmox-side error the same 502, so the
      // message is the operator's only way to tell them apart.
      expect(
        screen.getByText("Failed to connect to Proxmox"),
      ).toBeInTheDocument();
    },
  );

  it("does not blame connectivity for a 403 from the view:ceph check", () => {
    mockUseCephStatus.mockReturnValue(
      failedResult(
        new ApiClientError(403, {
          error: "forbidden",
          message: "Insufficient permissions",
        }),
      ),
    );

    renderWithProviders(<ClusterCephTab clusterId="test-cluster-id" />);

    expect(screen.getByText("Failed to Load Ceph Status")).toBeInTheDocument();
    expect(screen.getByText("Insufficient permissions")).toBeInTheDocument();
    expect(screen.queryByText(/connectivity/i)).not.toBeInTheDocument();
  });

  it("names the status in the Ceph Not Available card when the body carried no message", () => {
    mockUseCephStatus.mockReturnValue(
      failedResult(new ApiClientError(502, { error: "unknown", message: "" })),
    );

    renderWithProviders(<ClusterCephTab clusterId="test-cluster-id" />);

    expect(screen.getByText("HTTP 502")).toBeInTheDocument();
  });

  // TanStack keeps the last good `data` through a failed refetch, so the error
  // branches must not take over a dashboard the operator is already reading.
  it("keeps the dashboard up when a refresh fails but data is still held", () => {
    mockUseCephStatus.mockReturnValue(
      queryResult({
        data: healthyStatus,
        dataUpdatedAt: STALE_AT,
        status: "error",
        isPending: false,
        isError: true,
        error: new ApiClientError(502, {
          error: "internal_server_error",
          message: "Failed to connect to Proxmox",
        }),
      }),
    );

    renderWithProviders(<ClusterCephTab clusterId="test-cluster-id" />);

    expect(screen.getByRole("tab", { name: /OSDs \(6\)/ })).toBeInTheDocument();
    expect(screen.queryByText("Ceph Not Available")).not.toBeInTheDocument();
    // Exact, not a regex: the age and the ": message" join are one template
    // literal precisely so JSX cannot eat the spacing around them.
    expect(
      screen.getByText(
        `Ceph status as of ${formatTimestamp(STALE_AT)} — the most recent refresh failed: Failed to connect to Proxmox`,
      ),
    ).toBeInTheDocument();
  });

  // The state the blank tab panel was actually in. TanStack derives isLoading
  // as isPending && isFetching, and a paused fetch is not fetching, so a paused
  // retry reports isLoading false, isError false and data undefined together —
  // past all the guards and into the old `if (!status) return null`.
  it("explains a paused retry instead of rendering nothing, and offers no dud Retry", () => {
    mockUseCephStatus.mockReturnValue(
      queryResult({ fetchStatus: "paused", isPaused: true }),
    );

    const { container } = renderWithProviders(
      <ClusterCephTab clusterId="test-cluster-id" />,
    );

    expect(container.innerHTML).not.toBe("");
    expect(screen.getByText("Ceph Status Unavailable")).toBeInTheDocument();
    // Names both gates: neither re-renders this tree when it shuts, so a
    // single deduced cause could be stale by the time it is painted.
    expect(
      screen.getByText(/foreground and the browser is online/i),
    ).toBeInTheDocument();
    // Query.fetch() early-returns into continueRetry() while fetchStatus is
    // not "idle", so a button here would issue no request at all.
    expect(
      screen.queryByRole("button", { name: "Retry" }),
    ).not.toBeInTheDocument();
  });

  it("retries from the error card, where fetchStatus is idle", async () => {
    const refetch = vi.fn();
    mockUseCephStatus.mockReturnValue({
      ...failedResult(
        new ApiClientError(502, {
          error: "internal_server_error",
          message: "Failed to connect to Proxmox",
        }),
      ),
      refetch,
    });

    renderWithProviders(<ClusterCephTab clusterId="test-cluster-id" />);

    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it("names the status in the generic card when the body carried no message", () => {
    mockUseCephStatus.mockReturnValue(
      failedResult(
        new ApiClientError(403, { error: "forbidden", message: "" }),
      ),
    );

    renderWithProviders(<ClusterCephTab clusterId="test-cluster-id" />);

    expect(screen.getByText("Failed to Load Ceph Status")).toBeInTheDocument();
    expect(screen.getByText("HTTP 403")).toBeInTheDocument();
  });

  it("offers a working retry when the query is idle with no data and no error", async () => {
    const refetch = vi.fn();
    mockUseCephStatus.mockReturnValue(queryResult({ refetch }));

    const { container } = renderWithProviders(
      <ClusterCephTab clusterId="test-cluster-id" />,
    );

    expect(container.innerHTML).not.toBe("");
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it("renders the Ceph dashboard once status resolves", () => {
    mockUseCephStatus.mockReturnValue(succeededResult(healthyStatus));

    renderWithProviders(<ClusterCephTab clusterId="test-cluster-id" />);

    expect(screen.getByRole("tab", { name: /OSDs \(6\)/ })).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: /Monitors \(3\)/ }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/last known Ceph status/i),
    ).not.toBeInTheDocument();
  });
});
