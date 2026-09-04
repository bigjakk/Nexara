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
  ostype: "",
  config_ostype: "",
  last_seen_at: "2024-01-01T00:00:00Z",
  created_at: "2024-01-01T00:00:00Z",
  updated_at: "2024-01-01T00:00:00Z",
};

// What useVM serves for the current test; renderPage sets it.
let mockCurrentVM: VMResponse = mockVM;

vi.mock("../api/vm-queries", () => ({
  useVM: () => ({
    data: mockCurrentVM,
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
  useContainerConfig: () => ({
    data: null,
    isLoading: false,
  }),
  useSnapshots: () => ({
    data: [],
    isLoading: false,
  }),
  useSnapshotCapability: () => ({
    data: { supported: true, blocking_volumes: [] },
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
  useSetVMPool: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useMoveDisk: () => ({
    mutate: vi.fn(),
    isPending: false,
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
  useGuestAgentInfo: () => ({
    data: { running: false },
    isLoading: false,
  }),
  useConvertToTemplate: () => ({
    mutate: vi.fn(),
    isPending: false,
    isError: false,
    reset: vi.fn(),
  }),
  useCloneToTemplate: () => ({
    mutate: vi.fn(),
    isPending: false,
    isError: false,
    reset: vi.fn(),
  }),
}));

vi.mock("@/features/clusters/api/cluster-queries", () => ({
  useClusterNodes: () => ({
    data: [],
  }),
  useClusterStorage: () => ({
    data: [],
  }),
  useClusterVMs: () => ({
    data: [],
  }),
  useNodeBridges: () => ({
    data: [],
  }),
  useMachineTypes: () => ({
    data: [],
  }),
  useCPUModels: () => ({
    data: [],
  }),
}));

vi.mock("@/features/storage/api/storage-queries", () => ({
  useClusterStorage: () => ({
    data: [],
  }),
}));

function renderPage(kind: string = "vm", vm: Partial<VMResponse> = {}) {
  mockCurrentVM = { ...mockVM, ...vm };
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
    mockCurrentVM = mockVM;
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
    expect(screen.getByText("QEMU")).toBeInTheDocument();
  });

  it("renders tabs for Overview and Snapshots", () => {
    renderPage();
    expect(screen.getByRole("tab", { name: /overview/i })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /snapshots/i })).toBeInTheDocument();
  });

  it("shows action buttons for running VM", () => {
    renderPage();
    expect(
      screen.getByRole("button", { name: /shutdown/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /^Clone$/i }),
    ).toBeInTheDocument();
  });

  // Guest tools are a Windows-only concept, and this page is the one place the
  // guest's OS is classified — VMGuestToolsCard trusts the gate rather than
  // re-deriving it. Drop the gate and every Linux VM's page fires a
  // cluster-wide guest-tools request, and 403s it without view:guest_tools.
  it("hides the Guest Tools tab for a non-Windows guest", () => {
    renderPage();
    expect(
      screen.queryByRole("tab", { name: /guest tools/i }),
    ).not.toBeInTheDocument();
  });

  // Both limbs of the OR, because either alone is enough to make a guest
  // Windows: config_ostype is the authoritative Proxmox setting, and the
  // agent-reported ostype covers guests whose config never named an OS.
  // Stopped so the Overview guest-agent panel — the one other guest-tools
  // reader — stays unmounted and no query fires.
  it.each([{ config_ostype: "win11" }, { ostype: "mswindows" }])(
    "shows the Guest Tools tab for a Windows guest (%o)",
    (os) => {
      renderPage("vm", { ...os, status: "stopped" });
      expect(
        screen.getByRole("tab", { name: /guest tools/i }),
      ).toBeInTheDocument();
    },
  );
});
