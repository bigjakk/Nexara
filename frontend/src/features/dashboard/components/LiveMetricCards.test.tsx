import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { LiveMetricCards } from "./LiveMetricCards";
import type { AggregatedMetrics } from "@/types/ws";

const metrics: AggregatedMetrics = {
  cpuPercent: 12.5,
  memPercent: 50,
  memUsed: 34359738368,
  memTotal: 68719476736,
  diskReadBps: 1048576,
  diskWriteBps: 2097152,
  netInBps: 524288,
  netOutBps: 262144,
  nodeCount: 3,
  vmCount: 10,
  history: [],
  topConsumers: [],
  vmMetrics: new Map(),
  nodeMetrics: new Map(),
};

describe("LiveMetricCards", () => {
  it("renders all four metric cards with values", () => {
    renderWithProviders(<LiveMetricCards metrics={metrics} />);

    expect(screen.getByText("CPU Usage")).toBeInTheDocument();
    expect(screen.getByText("12.5%")).toBeInTheDocument();
    expect(screen.getByText("Memory Usage")).toBeInTheDocument();
    expect(screen.getByText("50.0%")).toBeInTheDocument();
    expect(screen.getByText("Disk I/O")).toBeInTheDocument();
    expect(screen.getByText("1.0 MB/s / 2.0 MB/s")).toBeInTheDocument();
    expect(screen.getByText("Network")).toBeInTheDocument();
    expect(screen.getByText("512.0 KB/s / 256.0 KB/s")).toBeInTheDocument();
  });

  it("shows used/total bytes under the memory card", () => {
    renderWithProviders(<LiveMetricCards metrics={metrics} />);

    expect(screen.getByText("32.0 GB of 64.0 GB used")).toBeInTheDocument();
  });

  it("renders a sparkline per card once history has 2+ points", () => {
    const point = {
      timestamp: 0,
      cpuPercent: 10,
      memPercent: 40,
      diskReadBps: 100,
      diskWriteBps: 200,
      netInBps: 300,
      netOutBps: 400,
    };
    renderWithProviders(
      <LiveMetricCards
        metrics={{
          ...metrics,
          history: [point, { ...point, timestamp: 1, cpuPercent: 20 }],
        }}
      />,
    );

    expect(screen.getAllByTestId("sparkline")).toHaveLength(4);
  });

  it("renders no sparklines without history", () => {
    renderWithProviders(<LiveMetricCards metrics={metrics} />);

    expect(screen.queryByTestId("sparkline")).not.toBeInTheDocument();
  });

  it("renders no sparklines with a single history point", () => {
    renderWithProviders(
      <LiveMetricCards
        metrics={{
          ...metrics,
          history: [
            {
              timestamp: 0,
              cpuPercent: 10,
              memPercent: 40,
              diskReadBps: 100,
              diskWriteBps: 200,
              netInBps: 300,
              netOutBps: 400,
            },
          ],
        }}
      />,
    );

    expect(screen.queryByTestId("sparkline")).not.toBeInTheDocument();
  });

  it("hides the memory sub-line when memTotal is zero", () => {
    renderWithProviders(
      <LiveMetricCards metrics={{ ...metrics, memUsed: 0, memTotal: 0 }} />,
    );

    expect(screen.queryByText(/of .* used/)).not.toBeInTheDocument();
  });

  it("shows skeletons while metrics are undefined", () => {
    renderWithProviders(<LiveMetricCards metrics={undefined} />);

    expect(screen.getAllByTestId("metric-skeleton")).toHaveLength(4);
  });
});
