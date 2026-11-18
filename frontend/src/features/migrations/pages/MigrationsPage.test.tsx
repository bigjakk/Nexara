import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { createTestQueryClient, renderWithProviders } from "@/test/test-utils";
import { MigrationsPage } from "./MigrationsPage";
import type { MigrationJob } from "../types/migration";

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({
    isAdmin: true,
    user: { id: "1", username: "admin", role: "admin" },
  }),
}));

vi.mock("@/features/dashboard/api/dashboard-queries", () => ({
  useClusters: () => ({
    data: [{ id: "c1", name: "Test Cluster", api_url: "https://test:8006" }],
    isLoading: false,
    error: null,
  }),
}));

// What the jobs and guest-name hooks serve; a test that needs rows sets them.
let mockJobs: MigrationJob[] = [];
let mockGuestNames = new Map<string, string>();

vi.mock("../api/migration-queries", () => ({
  useMigrationJobs: () => ({ data: mockJobs, isLoading: false, error: null }),
  useMigrationGuestNames: () => mockGuestNames,
  useMigrationJob: () => ({ data: null, isLoading: false, error: null }),
  useCreateMigration: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useRunPreFlightCheck: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useExecuteMigration: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useCancelMigration: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useMigrationJobsByCluster: () => ({
    data: [],
    isLoading: false,
    error: null,
  }),
}));

function renderPage() {
  return renderWithProviders(<MigrationsPage />, {
    client: createTestQueryClient({ keepCache: true }),
  });
}

describe("MigrationsPage", () => {
  afterEach(() => {
    mockJobs = [];
    mockGuestNames = new Map();
  });

  // A job keeps only the VMID; the Guest column read "9901 VM", no name.
  it("names a job's guest from its cluster's guest list", () => {
    mockJobs = [
      {
        id: "j1",
        source_cluster_id: "c1",
        target_cluster_id: "c1",
        source_node: "pve-01",
        target_node: "pve-02",
        vmid: 9901,
        vm_type: "qemu",
        migration_type: "intra-cluster",
        status: "completed",
        progress: 100,
        created_at: "2026-11-17T23:15:43Z",
      } as unknown as MigrationJob,
    ];
    mockGuestNames = new Map([["c1:9901", "linux01"]]);
    renderPage();
    expect(screen.getByText("linux01")).toBeTruthy();
  });

  it("renders page title", () => {
    renderPage();
    expect(screen.getByText("Migrations")).toBeInTheDocument();
  });

  it("shows new migration button", () => {
    renderPage();
    expect(screen.getByText("New migration")).toBeInTheDocument();
  });

  it("shows empty state when no jobs", () => {
    renderPage();
    expect(screen.getByText(/No migration jobs yet/)).toBeInTheDocument();
  });
});
