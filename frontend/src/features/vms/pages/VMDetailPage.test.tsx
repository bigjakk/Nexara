import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { VMDetailPage } from "./VMDetailPage";
import type { VMResponse } from "@/types/api";

const mockVM: VMResponse = {
  id: "vm-uuid-1",
  cluster_id: "cluster-1",
  node_id: "node-1",
  vmid: 100,
  name: "test-vm-1",
  type: "qemu",
  status: "running",
  cpu_count: 4,
  mem_total: 8589934592,
  disk_total: 107374182400,
  uptime: 86400,
  template: false,
  tags: "web,prod",
  ha_state: "",
  pool: "",
  last_seen_at: "2024-01-01T00:00:00Z",
  created_at: "2024-01-01T00:00:00Z",
  updated_at: "2024-01-01T00:00:00Z",
};

vi.mock("../api/vm-queries", () => ({
  useVM: () => ({
    data: mockVM,
    isLoading: false,
    error: null,
  }),
  useVMAction: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useCloneVM: () => ({
    mutate: vi.fn(),
    isPending: false,
    reset: vi.fn(),
  }),
  useDestroyVM: () => ({
    mutate: vi.fn(),
    isPending: false,
    reset: vi.fn(),
  }),
  useMigrateContainer: () => ({
    mutate: vi.fn(),
    isPending: false,
    reset: vi.fn(),
  }),
  useTaskStatus: () => ({
    data: null,
  }),
  useClusterVMIDs: () => ({
    data: new Set<number>(),
  }),
  useAddTaskHistory: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useUpdateTaskHistory: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useSetResourceConfig: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useSetVMConfig: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useVMConfig: () => ({
    data: null,
    isLoading: false,
  }),
  useSnapshots: () => ({
    data: [],
    isLoading: false,
  }),
  useCreateSnapshot: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useDeleteSnapshot: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useRollbackSnapshot: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useResizeDisk: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useCreateVM: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useCreateContainer: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useResourcePools: () => ({
    data: [],
    isLoading: false,
  }),
  useTaskHistory: () => ({
    data: [],
    isLoading: false,
  }),
  useClearTaskHistory: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useAttachDisk: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useDetachDisk: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useScheduledTasks: () => ({
    data: [],
    isLoading: false,
  }),
  useCreateSchedule: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useDeleteSchedule: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useTaskLog: () => ({
    data: null,
    isLoading: false,
  }),
}));

vi.mock("@/features/clusters/api/cluster-queries", () => ({
  useClusterNodes: () => ({
    data: [],
  }),
  useClusterStorage: () => ({
    data: [],
  }),
}));

function renderPage(kind: string = "vm") {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });

  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[`/inventory/${kind}/cluster-1/vm-uuid-1`]}>
        <Routes>
          <Route
            path="/inventory/:kind/:clusterId/:vmId"
            element={<VMDetailPage />}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("VMDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders VM name and status", () => {
    renderPage();
    expect(screen.getByText("test-vm-1")).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
  });

  it("renders overview tab with resource info", () => {
    renderPage();
    expect(screen.getByText("100")).toBeInTheDocument(); // VMID
    expect(screen.getByText("4")).toBeInTheDocument(); // CPUs
    expect(screen.getByText("QEMU VM")).toBeInTheDocument();
  });

  it("renders tabs for Overview, Metrics, Console", () => {
    renderPage();
    expect(screen.getByRole("tab", { name: /overview/i })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /metrics/i })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /console/i })).toBeInTheDocument();
  });

  it("shows action buttons for running VM", () => {
    renderPage();
    expect(screen.getByRole("button", { name: /shutdown/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /clone/i })).toBeInTheDocument();
  });
});
