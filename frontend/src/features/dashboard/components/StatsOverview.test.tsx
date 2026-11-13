import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { makeVM } from "@/test/cluster.fixtures";
import { StatsOverview } from "./StatsOverview";

describe("StatsOverview", () => {
  it("renders stats values", () => {
    renderWithProviders(
      <StatsOverview
        totalNodes={5}
        totalNodesOnline={5}
        totalVMs={20}
        totalVMsRunning={15}
        totalContainers={10}
        totalContainersRunning={8}
        totalStorageBytes={1099511627776}
        totalStorageUsedBytes={549755813888}
        isLoading={false}
      />,
    );

    expect(screen.getByText("5")).toBeInTheDocument();
    expect(screen.getByText("23")).toBeInTheDocument();
    expect(screen.getByText("/30")).toBeInTheDocument();
    expect(screen.getByText("512.0 GiB of 1.0 TiB used")).toBeInTheDocument();
  });

  // The same rule as the header and the Fleet widget, so the page's guest
  // totals agree: the lists win over the totals, and a template is no guest.
  it("counts guests from the cluster lists, leaving templates out", () => {
    renderWithProviders(
      <StatsOverview
        totalNodes={5}
        totalNodesOnline={5}
        totalVMs={20}
        totalVMsRunning={15}
        totalContainers={10}
        totalContainersRunning={8}
        totalStorageBytes={1099511627776}
        totalStorageUsedBytes={549755813888}
        isLoading={false}
        vmsByCluster={
          new Map([
            [
              "c1",
              [
                makeVM({ id: "v1", name: "linux01" }),
                makeVM({ id: "v2", name: "linux02", status: "stopped" }),
                makeVM({ id: "v3", name: "linux03", template: true }),
              ],
            ],
            ["c2", [makeVM({ id: "v4", name: "linux04", type: "lxc" })]],
          ])
        }
      />,
    );

    expect(screen.getByText("2")).toBeInTheDocument();
    expect(screen.getByText("/3")).toBeInTheDocument();
    expect(screen.getByText("2 VMs · 1 CTs")).toBeInTheDocument();
  });

  it("renders status sublines", () => {
    renderWithProviders(
      <StatsOverview
        totalNodes={5}
        totalNodesOnline={4}
        totalVMs={20}
        totalVMsRunning={15}
        totalContainers={10}
        totalContainersRunning={8}
        totalStorageBytes={1099511627776}
        totalStorageUsedBytes={549755813888}
        isLoading={false}
      />,
    );

    expect(screen.getByText("4/5 online")).toBeInTheDocument();
    expect(screen.getByText("20 VMs · 10 CTs")).toBeInTheDocument();
    expect(screen.getByText("50%")).toBeInTheDocument();
  });

  it("shows placeholders on the CPU and memory cards until live metrics arrive", () => {
    renderWithProviders(
      <StatsOverview
        totalNodes={5}
        totalNodesOnline={5}
        totalVMs={20}
        totalVMsRunning={15}
        totalContainers={10}
        totalContainersRunning={8}
        totalStorageBytes={1099511627776}
        totalStorageUsedBytes={549755813888}
        isLoading={false}
      />,
    );

    expect(screen.getAllByText("—")).toHaveLength(2);
    expect(screen.getAllByText("Waiting for data...")).toHaveLength(2);
  });

  it("renders datacenter CPU and memory from live metrics", () => {
    const metrics = new Map([
      [
        "cluster-1",
        {
          cpuPercent: 12.5,
          memPercent: 50,
          memUsed: 34359738368,
          memTotal: 68719476736,
          diskReadBps: 0,
          diskWriteBps: 0,
          netInBps: 0,
          netOutBps: 0,
          nodeCount: 3,
          vmCount: 10,
          history: [],
          topConsumers: [],
          vmMetrics: new Map(),
          nodeMetrics: new Map(),
        },
      ],
    ]);

    renderWithProviders(
      <StatsOverview
        totalNodes={3}
        totalNodesOnline={3}
        totalVMs={10}
        totalVMsRunning={8}
        totalContainers={0}
        totalContainersRunning={0}
        totalStorageBytes={1099511627776}
        totalStorageUsedBytes={549755813888}
        isLoading={false}
        metrics={metrics}
      />,
    );

    expect(screen.getByText("12.5%")).toBeInTheDocument();
    expect(screen.getByText("50.0%")).toBeInTheDocument();
    expect(screen.getByText("32.0 GiB of 64.0 GiB used")).toBeInTheDocument();
  });

  it("shows skeletons when loading", () => {
    renderWithProviders(
      <StatsOverview
        totalNodes={0}
        totalNodesOnline={0}
        totalVMs={0}
        totalVMsRunning={0}
        totalContainers={0}
        totalContainersRunning={0}
        totalStorageBytes={0}
        totalStorageUsedBytes={0}
        isLoading={true}
      />,
    );

    const skeletons = screen.getAllByTestId("stat-skeleton");
    expect(skeletons).toHaveLength(5);
  });

  it("renders all stat labels", () => {
    renderWithProviders(
      <StatsOverview
        totalNodes={0}
        totalNodesOnline={0}
        totalVMs={0}
        totalVMsRunning={0}
        totalContainers={0}
        totalContainersRunning={0}
        totalStorageBytes={0}
        totalStorageUsedBytes={0}
        isLoading={false}
      />,
    );

    expect(screen.getByText("Nodes")).toBeInTheDocument();
    expect(screen.getByText("Guests")).toBeInTheDocument();
    expect(screen.getByText("CPU")).toBeInTheDocument();
    expect(screen.getByText("Memory")).toBeInTheDocument();
    expect(screen.getByText("Storage")).toBeInTheDocument();
  });
});
