import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
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
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
  },
  nodeCount: 3,
  vmCount: 12,
  containerCount: 5,
  storageTotalBytes: 5497558138880,
};

describe("ClusterCard", () => {
  it("renders cluster name", () => {
    renderWithProviders(<ClusterCard summary={mockSummary} />);
    expect(screen.getByText("Production Cluster")).toBeInTheDocument();
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

  it("renders resource counts", () => {
    renderWithProviders(<ClusterCard summary={mockSummary} />);
    expect(screen.getByText("3 nodes")).toBeInTheDocument();
    expect(screen.getByText("12 VMs")).toBeInTheDocument();
    expect(screen.getByText("5 CTs")).toBeInTheDocument();
    expect(screen.getByText("5.0 TB")).toBeInTheDocument();
  });

  it("uses singular form for count of 1", () => {
    const single: ClusterSummary = {
      ...mockSummary,
      nodeCount: 1,
      vmCount: 1,
      containerCount: 1,
    };
    renderWithProviders(<ClusterCard summary={single} />);
    expect(screen.getByText("1 node")).toBeInTheDocument();
    expect(screen.getByText("1 VM")).toBeInTheDocument();
    expect(screen.getByText("1 CT")).toBeInTheDocument();
  });
});
