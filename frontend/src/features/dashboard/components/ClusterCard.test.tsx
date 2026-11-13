import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { makeVM } from "@/test/cluster.fixtures";
import { ClusterCard } from "./ClusterCard";
import type { ClusterSummary } from "../api/dashboard-queries";

const mockSummary: ClusterSummary = {
  cluster: {
    id: "test-id-1",
    name: "Production Cluster",
    api_url: "https://pve.example.com:8006",
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
  vmCount: 12,
  vmsRunning: 10,
  containerCount: 5,
  containersRunning: 4,
  storageTotalBytes: 5497558138880,
  storageUsedBytes: 2748779069440,
};

describe("ClusterCard", () => {
  it("names the cluster as the link that opens it", () => {
    renderWithProviders(<ClusterCard summary={mockSummary} />);
    expect(
      screen.getByRole("link", { name: "Production Cluster" }),
    ).toHaveAttribute("href", "/clusters/test-id-1");
  });

  it("shows online badge for online cluster", () => {
    renderWithProviders(<ClusterCard summary={mockSummary} />);
    expect(screen.getByText("Online")).toBeInTheDocument();
  });

  it("shows inactive badge for inactive cluster", () => {
    const inactive: ClusterSummary = {
      ...mockSummary,
      cluster: { ...mockSummary.cluster, is_active: false, status: "inactive" },
    };
    renderWithProviders(<ClusterCard summary={inactive} />);
    expect(screen.getByText("Inactive")).toBeInTheDocument();
  });

  it("says what the cluster holds", () => {
    renderWithProviders(<ClusterCard summary={mockSummary} />);
    expect(
      screen.getByText("3 of 3 nodes online · 12 VMs, 5 containers"),
    ).toBeInTheDocument();
    expect(screen.getByText("2.5 TiB of 5.0 TiB used")).toBeInTheDocument();
  });

  // The same rule as the header and the Fleet widget, so the page's guest
  // totals agree: the list wins over the summary, and a template is no guest.
  it("counts guests from the cluster's list, leaving templates out", () => {
    renderWithProviders(
      <ClusterCard
        summary={mockSummary}
        guests={[
          makeVM({ id: "v1", name: "linux01" }),
          makeVM({ id: "v2", name: "linux02", template: true }),
          makeVM({ id: "v3", name: "linux03", type: "lxc" }),
        ]}
      />,
    );
    expect(
      screen.getByText("3 of 3 nodes online · 1 VM, 1 container"),
    ).toBeInTheDocument();
  });

  it("uses singular form for count of 1", () => {
    const single: ClusterSummary = {
      ...mockSummary,
      nodeCount: 1,
      nodesOnline: 1,
      vmCount: 1,
      vmsRunning: 1,
      containerCount: 1,
      containersRunning: 1,
    };
    renderWithProviders(<ClusterCard summary={single} />);
    expect(
      screen.getByText("1 of 1 node online · 1 VM, 1 container"),
    ).toBeInTheDocument();
  });

  it("shows storage utilization from REST data", () => {
    renderWithProviders(<ClusterCard summary={mockSummary} />);
    expect(screen.getByText("50%")).toBeInTheDocument();
  });

  it("shows placeholders for CPU/memory until live metrics arrive", () => {
    renderWithProviders(<ClusterCard summary={mockSummary} />);
    expect(screen.getAllByText("—")).toHaveLength(2);
  });

  it("shows exact memory used of total once live metrics arrive", () => {
    renderWithProviders(
      <ClusterCard
        summary={mockSummary}
        metrics={{
          cpuPercent: 14,
          memPercent: 50,
          memUsed: 34359738368,
          memTotal: 68719476736,
          diskReadBps: 0,
          diskWriteBps: 0,
          netInBps: 0,
          netOutBps: 0,
          nodeCount: 3,
          vmCount: 12,
          history: [],
          topConsumers: [],
          vmMetrics: new Map(),
          nodeMetrics: new Map(),
        }}
      />,
    );
    expect(screen.getByText("32.0 GiB of 64.0 GiB used")).toBeInTheDocument();
  });

  it("leads the facts with the PVE version when it is known", () => {
    const versioned: ClusterSummary = {
      ...mockSummary,
      cluster: { ...mockSummary.cluster, pve_version: "8.4.1" },
    };
    renderWithProviders(<ClusterCard summary={versioned} />);
    expect(screen.getByText(/^PVE 8\.4\.1 · /)).toBeInTheDocument();
  });
});
