import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
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
});
