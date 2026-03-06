import { describe, it, expect, vi } from "vitest";
import { screen, render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { MigrationsPage } from "./MigrationsPage";

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({
    isAdmin: true,
    user: { id: "1", username: "admin", role: "admin" },
  }),
}));

vi.mock("@/features/dashboard/api/dashboard-queries", () => ({
  useClusters: () => ({
    data: [
      { id: "c1", name: "Test Cluster", api_url: "https://test:8006" },
    ],
    isLoading: false,
    error: null,
  }),
}));

vi.mock("../api/migration-queries", () => ({
  useMigrationJobs: () => ({ data: [], isLoading: false, error: null }),
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
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <MigrationsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("MigrationsPage", () => {
  it("renders page title", () => {
    renderPage();
    expect(screen.getByText("Migrations")).toBeInTheDocument();
  });

  it("shows new migration button", () => {
    renderPage();
    expect(screen.getByText("New Migration")).toBeInTheDocument();
  });

  it("shows empty state when no jobs", () => {
    renderPage();
    expect(
      screen.getByText(/No migration jobs yet/),
    ).toBeInTheDocument();
  });
});
