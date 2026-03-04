import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { StatsOverview } from "./StatsOverview";

describe("StatsOverview", () => {
  it("renders stats values", () => {
    renderWithProviders(
      <StatsOverview
        totalNodes={5}
        totalVMs={20}
        totalContainers={10}
        totalStorageBytes={1099511627776}
        isLoading={false}
      />,
    );

    expect(screen.getByText("5")).toBeInTheDocument();
    expect(screen.getByText("20")).toBeInTheDocument();
    expect(screen.getByText("10")).toBeInTheDocument();
    expect(screen.getByText("1.0 TB")).toBeInTheDocument();
  });

  it("shows skeletons when loading", () => {
    renderWithProviders(
      <StatsOverview
        totalNodes={0}
        totalVMs={0}
        totalContainers={0}
        totalStorageBytes={0}
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
        totalVMs={0}
        totalContainers={0}
        totalStorageBytes={0}
        isLoading={false}
      />,
    );

    expect(screen.getByText("Nodes")).toBeInTheDocument();
    expect(screen.getByText("Virtual Machines")).toBeInTheDocument();
    expect(screen.getByText("Containers")).toBeInTheDocument();
    expect(screen.getByText("Total Storage")).toBeInTheDocument();
  });
});
