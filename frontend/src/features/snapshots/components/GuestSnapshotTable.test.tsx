import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { GuestSnapshotTable } from "./GuestSnapshotTable";
import { ageBucket, ageDays, formatAge } from "../lib/age";
import type { GuestSnapshotRow } from "../types/snapshots";

const deleteMutate = vi.hoisted(() => vi.fn());
const resyncMutate = vi.hoisted(() => vi.fn());
const permissionState = vi.hoisted(() => ({ canDelete: true }));

vi.mock("@/features/vms/api/vm-queries", () => ({
  useDeleteSnapshot: () => ({
    mutate: deleteMutate,
    isPending: false,
  }),
}));

vi.mock("../api/snapshot-queries", () => ({
  useResyncGuestSnapshots: () => ({
    mutate: resyncMutate,
    isPending: false,
  }),
}));

vi.mock("@/hooks/usePermissions", () => ({
  usePermissions: () => ({
    canDelete: () => permissionState.canDelete,
  }),
}));

// Render a stand-in banner whose button reports task completion, so the
// post-delete resync hand-off can be exercised without task polling.
vi.mock("@/features/vms/components/TaskProgressBanner", () => ({
  TaskProgressBanner: ({
    onComplete,
  }: {
    onComplete?: (ok: boolean) => void;
  }) => (
    <button
      type="button"
      data-testid="task-banner"
      onClick={() => onComplete?.(true)}
    >
      task
    </button>
  ),
}));

const DAY = 86400;
const NOW_SEC = Math.floor(Date.now() / 1000);

function makeRow(overrides: Partial<GuestSnapshotRow>): GuestSnapshotRow {
  return {
    cluster_id: "c1",
    cluster_name: "cluster02",
    vmid: 100,
    guest_type: "qemu",
    vm_id: "vm-uuid-1",
    vm_name: "web01",
    vm_status: "running",
    node: "pve1",
    name: "snapA",
    description: "",
    parent: "",
    vmstate: false,
    snap_time: NOW_SEC - 2 * DAY,
    last_seen_at: new Date().toISOString(),
    ...overrides,
  };
}

beforeEach(() => {
  deleteMutate.mockReset();
  resyncMutate.mockReset();
  permissionState.canDelete = true;
});

describe("age helpers", () => {
  it("buckets ages and refuses to compute from snap_time 0", () => {
    expect(ageDays(0, Date.now())).toBeNull();
    expect(ageBucket(null)).toBe("unknown");
    expect(ageBucket(3)).toBe("fresh");
    expect(ageBucket(8)).toBe("week");
    expect(ageBucket(31)).toBe("month");
    expect(formatAge(null)).toBe("—");
    expect(formatAge(2.6)).toBe("2d");
    expect(formatAge(0.5)).toBe("12h");
  });
});

describe("GuestSnapshotTable", () => {
  it("renders age badges and pins unknown ages last", () => {
    renderWithProviders(
      <GuestSnapshotTable
        rows={[
          makeRow({ name: "no-time", snap_time: 0 }),
          makeRow({ name: "ancient", snap_time: NOW_SEC - 40 * DAY }),
          makeRow({ name: "stale", snap_time: NOW_SEC - 10 * DAY }),
        ]}
      />,
    );
    expect(screen.getByText("40d")).toBeInTheDocument();
    expect(screen.getByText("10d")).toBeInTheDocument();

    const cells = screen.getAllByText(/ancient|stale|no-time/);
    // Oldest-first default: ancient, stale, then the unknown-age row.
    expect(cells[0]).toHaveTextContent("ancient");
    expect(cells[2]).toHaveTextContent("no-time");
  });

  it("disables the open-guest link and delete for orphaned rows", () => {
    renderWithProviders(
      <GuestSnapshotTable
        rows={[makeRow({ vm_id: null, vm_name: null, vm_status: null })]}
      />,
    );
    expect(screen.getByText("#100")).toBeInTheDocument();
    // Refresh, open-guest, and delete are all disabled for orphans (resync
    // and delete resolve via inventory; the guest is gone), and all three
    // explain themselves with the not-in-inventory tooltip.
    const unavailable = screen.getAllByTitle("Guest not in inventory");
    expect(unavailable).toHaveLength(3);
    for (const el of unavailable) {
      expect(el).toBeDisabled();
    }
  });

  it("blocks deletion without the delete permission", () => {
    permissionState.canDelete = false;
    renderWithProviders(<GuestSnapshotTable rows={[makeRow({})]} />);
    expect(screen.getByTitle("Requires delete permission")).toBeDisabled();
  });

  it("requires an explicit confirm, then dispatches and resyncs on completion", async () => {
    const user = userEvent.setup();
    deleteMutate.mockImplementation(
      (
        _vars: unknown,
        opts?: { onSuccess?: (data: { upid: string }) => void },
      ) => {
        opts?.onSuccess?.({
          upid: "UPID:pve1:0001:0002:0003:qmdelsnapshot:100:root@pam:",
        });
      },
    );

    renderWithProviders(<GuestSnapshotTable rows={[makeRow({})]} />);

    await user.click(screen.getByTitle("Delete snapshot"));
    expect(deleteMutate).not.toHaveBeenCalled();

    await user.click(screen.getByText("Confirm"));
    expect(deleteMutate).toHaveBeenCalledWith(
      expect.objectContaining({
        clusterId: "c1",
        resourceId: "vm-uuid-1",
        kind: "vm",
        snapName: "snapA",
      }),
      expect.anything(),
    );

    // Task banner appears; completing it triggers the per-guest resync.
    await user.click(screen.getByTestId("task-banner"));
    expect(resyncMutate).toHaveBeenCalledWith(
      expect.objectContaining({ clusterId: "c1", vmid: 100 }),
    );
  });

  it("routes container rows through the ct delete endpoint", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <GuestSnapshotTable
        rows={[makeRow({ guest_type: "lxc", vmid: 200, vm_id: "ct-uuid" })]}
      />,
    );
    await user.click(screen.getByTitle("Delete snapshot"));
    await user.click(screen.getByText("Confirm"));
    expect(deleteMutate).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "ct", resourceId: "ct-uuid" }),
      expect.anything(),
    );
  });
});
