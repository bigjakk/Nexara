import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
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
    expect(screen.getByText("1.0 TB")).toBeInTheDocument();
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
    expect(screen.getByText("50% used")).toBeInTheDocument();
  });

  it("shows a placeholder on the CPU card until live metrics arrive", () => {
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

    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.getByText("Waiting for data...")).toBeInTheDocument();
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
    expect(skeletons).toHaveLength(4);
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
    expect(screen.getByText("Datacenter CPU")).toBeInTheDocument();
    expect(screen.getByText("Total Storage")).toBeInTheDocument();
  });
});
