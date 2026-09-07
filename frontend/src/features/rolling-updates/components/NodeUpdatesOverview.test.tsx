import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { UseQueryResult } from "@tanstack/react-query";
import { NodeUpdatesOverview } from "./NodeUpdatesOverview";
import { ApiClientError } from "@/lib/api-client";
import type { AptPackage } from "@/types/api";

const useClusterNodes = vi.hoisted(() => vi.fn());
const useNodePackagePreview = vi.hoisted(() => vi.fn());

vi.mock("@/features/clusters/api/cluster-queries", () => ({ useClusterNodes }));
vi.mock("../api/rolling-update-queries", () => ({ useNodePackagePreview }));

/** A settled query result, shaped as the two hooks above return one. */
function result<T>(over: Partial<UseQueryResult<T>>) {
  return {
    data: undefined,
    isLoading: false,
    isError: false,
    isSuccess: false,
    isPaused: false,
    error: null,
    errorUpdatedAt: 0,
    fetchStatus: "idle",
    refetch: vi.fn(),
    ...over,
  } as UseQueryResult<T>;
}

describe("NodeUpdatesOverview", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useClusterNodes.mockReturnValue(
      result({
        data: [{ id: "n1", name: "pve-01" }],
        isSuccess: true,
      }),
    );
  });

  it("never reports a node as up to date when the update check failed", () => {
    // The bug this guards: the row read only { data, isLoading }, so a token
    // missing Sys.Audit on the node fell through `count === 0` into a green
    // "Up to date" badge for a node whose patch level nobody knows.
    useNodePackagePreview.mockReturnValue(
      result<AptPackage[]>({
        isError: true,
        error: new ApiClientError(403, {
          error: "forbidden",
          message: "Permission check failed (/nodes/pve-01, Sys.Audit)",
        }),
        errorUpdatedAt: 1000,
      }),
    );

    render(<NodeUpdatesOverview clusterId="c1" />);

    expect(screen.queryByText("Up to date")).toBeNull();
    expect(screen.getByText("Update check failed")).toBeInTheDocument();
  });

  it("does not claim a node is up to date before the check has run", () => {
    // A paused retry reports isLoading false, isError false and data undefined
    // all at once — the same fall-through, without an error to notice.
    useNodePackagePreview.mockReturnValue(
      result<AptPackage[]>({ isPaused: true, fetchStatus: "paused" }),
    );

    render(<NodeUpdatesOverview clusterId="c1" />);

    expect(screen.queryByText("Up to date")).toBeNull();
    expect(screen.getByText("Not checked")).toBeInTheDocument();
  });

  it("still reports a node with no pending packages as up to date", () => {
    useNodePackagePreview.mockReturnValue(
      result<AptPackage[]>({ data: [], isSuccess: true }),
    );

    render(<NodeUpdatesOverview clusterId="c1" />);

    expect(screen.getByText("Up to date")).toBeInTheDocument();
  });

  it("explains the failure when the row is expanded", () => {
    useNodePackagePreview.mockReturnValue(
      result<AptPackage[]>({
        isError: true,
        error: new ApiClientError(403, {
          error: "forbidden",
          message: "Permission check failed (/nodes/pve-01, Sys.Audit)",
        }),
        errorUpdatedAt: 1000,
      }),
    );

    render(<NodeUpdatesOverview clusterId="c1" />);
    fireEvent.click(screen.getByRole("button", { name: /pve-01/ }));

    expect(screen.getByText(/patch level is unknown/)).toBeInTheDocument();
    expect(
      screen.getByText("Permission check failed (/nodes/pve-01, Sys.Audit)"),
    ).toBeInTheDocument();
  });

  it("does not call the patch level unknown while it still holds a reading", () => {
    // TanStack keeps the last good rows through a failed refetch. Printing
    // "its patch level is unknown" directly above that list contradicts it —
    // what is unknown is whether the list is still current.
    useNodePackagePreview.mockReturnValue(
      result<AptPackage[]>({
        data: [
          { Package: "openssh-server", Version: "1:9.2p1-2" } as AptPackage,
        ],
        isError: true,
        error: new ApiClientError(502, {
          error: "bad_gateway",
          message: "node unreachable",
        }),
        errorUpdatedAt: 1000,
      }),
    );

    render(<NodeUpdatesOverview clusterId="c1" />);
    fireEvent.click(screen.getByRole("button", { name: /pve-01/ }));

    expect(screen.queryByText(/patch level is unknown/)).toBeNull();
    expect(screen.getByText(/may be out of date/)).toBeInTheDocument();
    expect(screen.getByText("openssh-server")).toBeInTheDocument();
  });

  it("never opens an empty box when a row with nothing to list is expanded", () => {
    useNodePackagePreview.mockReturnValue(
      result<AptPackage[]>({ isPaused: true, fetchStatus: "paused" }),
    );

    render(<NodeUpdatesOverview clusterId="c1" />);
    fireEvent.click(screen.getByRole("button", { name: /pve-01/ }));

    expect(screen.getByText(/has not checked this node/)).toBeInTheDocument();
  });

  it("reports a failure to list the cluster's nodes instead of vanishing", () => {
    useClusterNodes.mockReturnValue(
      result({
        isError: true,
        error: new ApiClientError(502, {
          error: "bad_gateway",
          message: "cluster unreachable",
        }),
        errorUpdatedAt: 1000,
      }),
    );
    useNodePackagePreview.mockReturnValue(result<AptPackage[]>({}));

    render(<NodeUpdatesOverview clusterId="c1" />);

    expect(
      screen.getByText("Could not load this cluster's nodes."),
    ).toBeInTheDocument();
    expect(screen.getByText("cluster unreachable")).toBeInTheDocument();
  });
});
