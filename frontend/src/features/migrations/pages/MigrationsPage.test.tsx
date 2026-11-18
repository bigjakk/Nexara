import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { createTestQueryClient, renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { useAuthStore } from "@/stores/auth-store";
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
const mockCancel = vi.fn();

vi.mock("../api/migration-queries", () => ({
  useMigrationJobs: () => ({ data: mockJobs, isLoading: false, error: null }),
  useMigrationGuestNames: () => mockGuestNames,
  useMigrationJob: () => ({ data: null, isLoading: false, error: null }),
  useCreateMigration: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useRunPreFlightCheck: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useExecuteMigration: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useCancelMigration: () => ({ mutate: mockCancel, isPending: false }),
  useMigrationJobsByCluster: () => ({
    data: [],
    isLoading: false,
    error: null,
  }),
}));

function job(
  id: string,
  vmid: number,
  status: MigrationJob["status"],
): MigrationJob {
  return {
    id,
    source_cluster_id: "c1",
    target_cluster_id: "c1",
    source_node: "pve-01",
    target_node: "pve-02",
    vmid,
    vm_type: "qemu",
    migration_type: "intra-cluster",
    status,
    progress: 0,
    created_at: "2026-11-17T23:15:43Z",
  } as unknown as MigrationJob;
}

function renderPage() {
  return renderWithProviders(<MigrationsPage />, {
    client: createTestQueryClient({ keepCache: true }),
  });
}

describe("MigrationsPage", () => {
  afterEach(() => {
    mockJobs = [];
    mockGuestNames = new Map();
    mockCancel.mockReset();
    useAuthStore.setState({ permissions: [] });
  });

  // A job keeps only the VMID; the Guest column read "9901 VM", no name.
  it("names a job's guest from its cluster's guest list", () => {
    mockJobs = [job("j1", 9901, "completed")];
    mockGuestNames = new Map([["c1:9901", "linux01"]]);
    renderPage();
    expect(screen.getByText("linux01")).toBeTruthy();
  });

  // The page had no Cancel, so a job created and never run stayed pending.
  it("offers Cancel only on a job that has not started", async () => {
    useAuthStore.setState({ permissions: ["manage:migration"] });
    mockJobs = [job("j1", 9901, "pending"), job("j2", 9902, "migrating")];
    renderPage();
    await setupUser().click(
      screen.getByRole("button", { name: /^Cancel the migration of/ }),
    );
    expect(mockCancel).toHaveBeenCalledWith("j1");
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
