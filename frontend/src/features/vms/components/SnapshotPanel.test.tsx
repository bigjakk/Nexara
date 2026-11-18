import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createTestQueryClient, renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { GUEST_ACTION_PERMISSIONS, grantPermissions } from "@/test/permissions";
import { SnapshotPanel } from "./SnapshotPanel";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedList = vi.mocked(apiClient.list);

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const VM = "d0000000-0000-4000-8000-000000000001";

describe("SnapshotPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    grantPermissions(...GUEST_ACTION_PERMISSIONS);
  });

  // The tab offered Take snapshot, Rollback and Delete whatever the role.
  it("offers a user without the permissions no snapshot action", async () => {
    grantPermissions("view:vm");
    mockedList.mockResolvedValue([
      { name: "before-upgrade", snap_time: 1767225600, description: "clean" },
    ]);
    renderWithProviders(
      <SnapshotPanel
        clusterId={CLUSTER}
        resourceId={VM}
        kind="vm"
        resourceName="linux01"
      />,
    );
    expect(await screen.findByText("before-upgrade")).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
  });

  // Proxmox lists the running state as a snapshot named "current". It is not
  // one: it is not counted, and it cannot be rolled back to or deleted.
  it("lists the tree, counts only real snapshots and offers nothing on the current state", async () => {
    mockedList.mockResolvedValue([
      { name: "before-upgrade", snap_time: 1767225600, description: "clean" },
      {
        name: "after-upgrade",
        snap_time: 1767312000,
        parent: "before-upgrade",
      },
      {
        name: "current",
        parent: "after-upgrade",
        description: "You are here!",
      },
    ]);
    renderWithProviders(
      <SnapshotPanel
        clusterId={CLUSTER}
        resourceId={VM}
        kind="vm"
        resourceName="linux01"
      />,
    );

    expect(await screen.findByText("before-upgrade")).toBeInTheDocument();
    expect(screen.getByText(/^2 taken, the newest on /)).toBeInTheDocument();
    const rows = screen.getAllByRole("listitem");
    expect(rows).toHaveLength(3);
    for (const [index, actions] of [2, 2, 0].entries()) {
      const row = rows[index];
      if (!row) throw new Error(`no row ${String(index)}`);
      expect(within(row).queryAllByRole("button")).toHaveLength(actions);
    }
    expect(screen.getByText("Current state")).toBeInTheDocument();
  });

  // A failed rollback's banner went as its task ended, and Proxmox's reason
  // with it.
  it("keeps a failed rollback on its banner", async () => {
    mockedList.mockResolvedValue([
      { name: "before-upgrade", snap_time: 1767225600, description: "clean" },
    ]);
    const upid = "UPID:pve-01:00000001:00000002:00000003:qmrollback:101:x:";
    vi.mocked(apiClient.post).mockResolvedValue({ upid, status: "OK" });
    vi.mocked(apiClient.get).mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path).endsWith("/snapshot-capability")
          ? { supported: true, blocking_volumes: [] }
          : { upid, status: "stopped", exit_status: "snapshot is locked" },
      ),
    );
    const user = userEvent.setup();
    const client = createTestQueryClient();
    const invalidate = vi.spyOn(client, "invalidateQueries");
    renderWithProviders(
      <SnapshotPanel
        clusterId={CLUSTER}
        resourceId={VM}
        kind="vm"
        resourceName="linux01"
      />,
      { client },
    );
    await user.click(await screen.findByRole("button", { name: "Rollback" }));
    await user.click(screen.getByRole("button", { name: "Confirm" }));

    // The banner refreshes recent activity as it reports the task's end.
    await waitFor(() => {
      expect(invalidate).toHaveBeenCalledWith({
        queryKey: ["recent-activity"],
      });
    });
    expect(
      screen.getByText("Task failed: snapshot is locked"),
    ).toBeInTheDocument();
    // Until the next operation: Take snapshot follows its own task.
    await user.click(screen.getByRole("button", { name: "Take snapshot" }));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.queryByText("Task failed: snapshot is locked")).toBeNull();
  });
});
