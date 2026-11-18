import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import { useAuthStore } from "@/stores/auth-store";
import { BackupPanel } from "./BackupPanel";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const SNAPSHOT = {
  id: "s1",
  pbs_server_id: "p1",
  datastore: "datastore01",
  backup_type: "vm",
  backup_id: "101",
  backup_time: 1700000000,
  size: 1024,
  verified: true,
  protected: false,
  comment: "",
  owner: "",
};

// PBS groups snapshots by VMID alone: the panel asks for its own cluster's,
// which the server filters by the cluster's PBS storages, and says what it
// cannot tell apart.
describe("BackupPanel", () => {
  beforeEach(() => {
    grantPermissions("manage:backup", "delete:backup");
  });

  // The tab offered Back up now, Restore and Delete backup whatever the role.
  it.each([
    [[], false, false],
    [["manage:backup"], true, false],
    [["manage:backup", "delete:backup"], true, true],
  ])(
    "with %j offers Back up now and Restore: %s, Delete backup: %s",
    async (permissions, manage, del) => {
      grantPermissions(...permissions);
      vi.mocked(apiClient.get).mockResolvedValue({
        items: [SNAPSHOT],
        total: 1,
        storage_unread: false,
      });
      vi.mocked(apiClient.list).mockResolvedValue([]);
      renderWithProviders(
        <BackupPanel vmid={101} clusterId="c1" nodeName="pve-01" kind="vm" />,
      );
      expect(await screen.findByText("datastore01")).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "Back up now" }) !== null,
      ).toBe(manage);
      expect(screen.queryByRole("button", { name: /^Restore/ }) !== null).toBe(
        manage,
      );
      expect(
        screen.queryByRole("button", { name: "Delete backup" }) !== null,
      ).toBe(del);
    },
  );

  // A delete takes delete:backup on the PBS server's cluster, or across the
  // install for a server attached to none; a grant on one cluster showed it on
  // a standalone server's backups, and the delete answered 403.
  it("with delete:backup on one cluster offers Delete backup only where the server is attached", async () => {
    useAuthStore.setState({
      user: null,
      permissions: ["delete:backup"],
      globalPermissions: [],
    });
    vi.mocked(apiClient.get).mockResolvedValue({
      items: [
        SNAPSHOT,
        {
          ...SNAPSHOT,
          id: "s2",
          pbs_server_id: "p2",
          datastore: "datastore02",
        },
      ],
      total: 2,
      storage_unread: false,
    });
    vi.mocked(apiClient.list).mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path) === "/api/v1/pbs-servers"
          ? [
              { id: "p1", cluster_id: "c1" },
              { id: "p2", cluster_id: null },
            ]
          : [],
      ),
    );
    renderWithProviders(
      <BackupPanel vmid={101} clusterId="c1" nodeName="pve-01" kind="vm" />,
    );
    // The attached server's Delete shows once the server list has arrived.
    const attached = (await screen.findByText("datastore01")).closest("tr");
    expect(
      await within(attached as HTMLElement).findByRole("button", {
        name: "Delete backup",
      }),
    ).toBeInTheDocument();
    const standalone = screen.getByText("datastore02").closest("tr");
    expect(
      within(standalone as HTMLElement).queryByRole("button", {
        name: "Delete backup",
      }),
    ).toBeNull();
  });

  // With the cluster's storages unread, the server filters by its last reading
  // and says so; the panel says that instead, and offers no delete or restore.
  it.each([
    [false, /PBS cannot tell them apart/],
    [true, /read-only until it answers/],
  ])(
    "lists this cluster's snapshots (storages unread: %s), with its note",
    async (unread, note) => {
      // The snapshot list alone; the storage and cluster lists the dialogs read are empty.
      vi.mocked(apiClient.get).mockImplementation((path: string) =>
        path.includes("/pbs-snapshots")
          ? Promise.resolve({
              items: [SNAPSHOT],
              total: 1,
              storage_unread: unread,
            })
          : Promise.reject(new Error(`unexpected GET ${path}`)),
      );
      vi.mocked(apiClient.list).mockResolvedValue([]);
      renderWithProviders(
        <BackupPanel vmid={101} clusterId="c1" nodeName="pve-01" kind="vm" />,
      );

      expect(await screen.findByText(note)).toBeInTheDocument();
      expect(screen.getByText("datastore01")).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "Delete backup" }) === null,
      ).toBe(unread);
      expect(vi.mocked(apiClient.get)).toHaveBeenCalledWith(
        "/api/v1/clusters/c1/pbs-snapshots?backup_id=101",
      );
    },
  );
});
