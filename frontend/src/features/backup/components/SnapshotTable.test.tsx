import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { SnapshotTable } from "./SnapshotTable";
import type { PBSSnapshot } from "../types/backup";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

function snapshot(backupTime: number): PBSSnapshot {
  return {
    id: `snap-${String(backupTime)}`,
    pbs_server_id: "pbs-1",
    datastore: "datastore01",
    backup_type: "vm",
    backup_id: "121",
    backup_time: backupTime,
    size: 1024,
    verified: false,
    protected: false,
    comment: "",
    owner: "root@pam",
    last_seen_at: "",
    created_at: "",
    updated_at: "",
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(apiClient.list).mockImplementation((path: unknown) =>
    Promise.resolve(
      String(path) === "/api/v1/pbs-servers"
        ? [{ id: "pbs-1", name: "pbs01", token_id: "backup@pbs!test" }]
        : [],
    ),
  );
  vi.mocked(apiClient.put).mockResolvedValue({ status: "ok" });
});

// A group's owner is changed from any of its snapshots, offering the server's
// own token, which is what a storage made with that token signs in as.
describe("SnapshotTable — change owner", () => {
  it("gives the group to the server's token", async () => {
    const user = setupUser();
    useAuthStore.setState({ user: null, permissions: ["manage:backup"] });
    renderWithProviders(
      <SnapshotTable
        snapshots={[snapshot(1700000000), snapshot(1700086400)]}
        pbsId="pbs-1"
      />,
    );

    await user.click(screen.getAllByText("121")[0] as HTMLElement);
    await user.click(
      screen.getByRole("button", { name: "Change owner of vm/121" }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent(
      "All 2 snapshots of vm/121 on datastore01",
    );
    await waitFor(() => {
      expect(screen.getByLabelText("New owner")).toHaveValue("backup@pbs!test");
    });

    await user.click(screen.getByRole("button", { name: "Change owner" }));
    await waitFor(() => {
      expect(apiClient.put).toHaveBeenCalledWith(
        "/api/v1/pbs-servers/pbs-1/datastores/datastore01/groups/owner",
        { backup_type: "vm", backup_id: "121", owner: "backup@pbs!test" },
      );
    });
  });

  // Filling in any other owner would make one unread click move the group off
  // the token a storage signs in as.
  it("fills in nothing when the server's token already owns the group", async () => {
    const user = setupUser();
    useAuthStore.setState({ user: null, permissions: ["manage:backup"] });
    const owned = { ...snapshot(1700000000), owner: "backup@pbs!test" };
    renderWithProviders(
      <SnapshotTable snapshots={[owned, snapshot(1700086400)]} pbsId="pbs-1" />,
    );

    await user.click(screen.getAllByText("121")[0] as HTMLElement);
    await user.click(
      screen.getByRole("button", { name: "Change owner of vm/121" }),
    );
    await screen.findByRole("dialog");
    expect(screen.getByLabelText("New owner")).toHaveValue("");
    expect(screen.getByRole("button", { name: "Change owner" })).toBeDisabled();
  });

  it("offers no change to a caller without manage:backup", async () => {
    const user = setupUser();
    useAuthStore.setState({ user: null, permissions: ["view:backup"] });
    renderWithProviders(
      <SnapshotTable snapshots={[snapshot(1700000000)]} pbsId="pbs-1" />,
    );

    await user.click(screen.getByText("121"));
    // The details are open: the change would sit beside their Owner.
    expect(screen.getByText("Last seen")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Change owner of vm/121" }),
    ).not.toBeInTheDocument();
  });
});
