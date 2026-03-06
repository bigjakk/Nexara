import { describe, it, expect, vi } from "vitest";
import { screen, render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { DRSPage } from "./DRSPage";

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

vi.mock("../api/drs-queries", () => ({
  useDRSConfig: () => ({ data: null, isLoading: false, error: null }),
  useUpdateDRSConfig: () => ({ mutate: vi.fn(), isPending: false }),
  useDRSRules: () => ({ data: [], isLoading: false, error: null }),
  useCreateDRSRule: () => ({ mutate: vi.fn(), isPending: false }),
  useDeleteDRSRule: () => ({ mutate: vi.fn(), isPending: false }),
  useTriggerEvaluation: () => ({ mutate: vi.fn(), isPending: false }),
  useDRSHistory: () => ({ data: [], isLoading: false, error: null }),
}));

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <DRSPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("DRSPage", () => {
  it("renders page title", () => {
    renderPage();
    expect(
      screen.getByText("Distributed Resource Scheduler"),
    ).toBeInTheDocument();
  });

  it("shows cluster selector", () => {
    renderPage();
    expect(screen.getByText("Select a cluster")).toBeInTheDocument();
  });

  it("shows prompt to select cluster when none selected", () => {
    renderPage();
    expect(
      screen.getByText("Select a cluster to configure DRS."),
    ).toBeInTheDocument();
  });
});
