import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { MetricChart } from "./MetricChart";
import type { MetricDataPoint } from "@/types/ws";

// Recharts uses SVG, and ResponsiveContainer needs size — mock to render children.
vi.mock("recharts", async () => {
  const actual = await vi.importActual<typeof import("recharts")>("recharts");
  return {
    ...actual,
    ResponsiveContainer: ({ children }: { children: ReactNode }) => (
      <div style={{ width: 500, height: 200 }}>{children}</div>
    ),
  };
});

const sampleData: MetricDataPoint[] = [
  {
    timestamp: Date.now() - 30000,
    cpuPercent: 45,
    memPercent: 60,
    diskReadBps: 1024000,
    diskWriteBps: 512000,
    netInBps: 2048000,
    netOutBps: 1024000,
  },
  {
    timestamp: Date.now(),
    cpuPercent: 50,
    memPercent: 65,
    diskReadBps: 1100000,
    diskWriteBps: 550000,
    netInBps: 2100000,
    netOutBps: 1100000,
  },
];

describe("MetricChart", () => {
  it("renders with data", () => {
    renderWithProviders(
      <MetricChart
        title="CPU Usage"
        data={sampleData}
        dataKey="cpuPercent"
        color="#3b82f6"
      />,
    );

    expect(screen.getByText("CPU Usage")).toBeInTheDocument();
  });

  it("shows waiting message when empty", () => {
    renderWithProviders(
      <MetricChart
        title="CPU Usage"
        data={[]}
        dataKey="cpuPercent"
        color="#3b82f6"
      />,
    );

    expect(screen.getByText("Waiting for data...")).toBeInTheDocument();
  });

  it("renders chart title for different metrics", () => {
    renderWithProviders(
      <MetricChart
        title="Memory Usage"
        data={sampleData}
        dataKey="memPercent"
        color="#8b5cf6"
      />,
    );

    expect(screen.getByText("Memory Usage")).toBeInTheDocument();
  });
});
