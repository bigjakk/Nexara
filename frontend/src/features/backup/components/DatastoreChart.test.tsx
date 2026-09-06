import { describe, it, expect, vi, beforeEach } from "vitest";
import { cloneElement, type ReactElement } from "react";
import { renderWithProviders } from "@/test/test-utils";
import { DatastoreChart } from "./DatastoreChart";
import type { PBSDatastoreMetric } from "../types/backup";

// ResponsiveContainer measures its parent, and jsdom computes no layout — a
// wrapper div with a style is not enough, because Recharts would still see
// width 0 and skip drawing every series. Cloning explicit numeric dimensions
// onto the chart is what makes the SVG actually render, which this file needs:
// counting the areas is the only way to prove one series per datastore.
vi.mock("recharts", async () => {
  const actual = await vi.importActual<typeof import("recharts")>("recharts");
  return {
    ...actual,
    ResponsiveContainer: ({
      children,
    }: {
      children: ReactElement<{ width?: number; height?: number }>;
    }) => cloneElement(children, { width: 500, height: 300 }),
  };
});

const mockUseMetrics = vi.fn();
vi.mock("../api/backup-queries", () => ({
  usePBSDatastoreMetrics: (...args: unknown[]) =>
    mockUseMetrics(...args) as unknown,
}));

function metric(
  time: string,
  datastore: string,
  used: number,
): PBSDatastoreMetric {
  return {
    time,
    pbs_server_id: "pbs-1",
    datastore,
    total: 1_000,
    used,
    avail: 1_000 - used,
  };
}

const T1 = "2026-09-06T10:00:00Z";
const T2 = "2026-09-06T11:00:00Z";

function areaCount(container: HTMLElement): number {
  return container.querySelectorAll(".recharts-area").length;
}

/**
 * Counting <Area> layers proves the structure but not the data: Recharts emits
 * one point per row whether or not the dataKey resolves, so a broken key still
 * yields N layers of empty chart. The curve only gets a "d" once real values
 * come back, so this is what actually pins the series to its datastore.
 */
function drawnCurveCount(container: HTMLElement): number {
  return [...container.querySelectorAll(".recharts-area-curve")].filter((c) =>
    Boolean(c.getAttribute("d")),
  ).length;
}

describe("DatastoreChart", () => {
  beforeEach(() => {
    mockUseMetrics.mockReset();
  });

  it("draws one area per datastore", () => {
    // The regression this guards: three datastores used to collapse into a
    // single series that jumped between their values at every timestamp. The
    // dev PBS server has one datastore, so only a test can prove this.
    mockUseMetrics.mockReturnValue({
      data: [
        metric(T1, "store-a", 10),
        metric(T1, "store-b", 20),
        metric(T1, "store-c", 30),
        metric(T2, "store-a", 11),
        metric(T2, "store-b", 21),
        metric(T2, "store-c", 31),
      ],
      isLoading: false,
    });

    const { container } = renderWithProviders(<DatastoreChart pbsId="pbs-1" />);
    expect(areaCount(container)).toBe(3);
    expect(drawnCurveCount(container)).toBe(3);
  });

  it("still draws a single area for a single datastore", () => {
    mockUseMetrics.mockReturnValue({
      data: [metric(T1, "store-a", 10), metric(T2, "store-a", 11)],
      isLoading: false,
    });

    const { container } = renderWithProviders(<DatastoreChart pbsId="pbs-1" />);
    expect(areaCount(container)).toBe(1);
    // One series names itself in the card title; a legend would be noise.
    expect(container.querySelector(".recharts-legend-wrapper")).toBeNull();
  });

  it("shows a legend only once there is more than one series to tell apart", () => {
    mockUseMetrics.mockReturnValue({
      data: [metric(T1, "store-a", 10), metric(T1, "store-b", 20)],
      isLoading: false,
    });

    const { container } = renderWithProviders(<DatastoreChart pbsId="pbs-1" />);
    expect(container.querySelector(".recharts-legend-wrapper")).not.toBeNull();
  });

  it("draws a series for a datastore whose name contains a dot", () => {
    // Recharts resolves a string dataKey with a lodash-compatible `get`, which
    // treats "." as a path separator and only falls back to it after missing
    // the literal key. If that precedence ever changes, this store would
    // silently render as an empty area instead of failing loudly.
    mockUseMetrics.mockReturnValue({
      data: [
        metric(T1, "backup.daily", 10),
        metric(T1, "store-a", 20),
        metric(T2, "backup.daily", 11),
        metric(T2, "store-a", 21),
      ],
      isLoading: false,
    });

    const { container } = renderWithProviders(<DatastoreChart pbsId="pbs-1" />);
    expect(areaCount(container)).toBe(2);
    expect(drawnCurveCount(container)).toBe(2);
  });

  it("renders nothing while loading or without samples", () => {
    mockUseMetrics.mockReturnValue({ data: undefined, isLoading: true });
    const { container: loading } = renderWithProviders(
      <DatastoreChart pbsId="pbs-1" />,
    );
    expect(loading).toBeEmptyDOMElement();

    mockUseMetrics.mockReturnValue({ data: [], isLoading: false });
    const { container: empty } = renderWithProviders(
      <DatastoreChart pbsId="pbs-1" />,
    );
    expect(empty).toBeEmptyDOMElement();
  });
});
