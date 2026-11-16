import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { BackupPanel } from "./BackupPanel";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// PBS groups snapshots by VMID alone: the panel asks for its own cluster's,
// which the server filters by the cluster's PBS storages, and says what it
// cannot tell apart.
describe("BackupPanel", () => {
  // With the cluster's storages unread, the server filters by its last reading
  // and says so; the panel says that instead, and offers no delete or restore.
  it.each([
    [false, /PBS cannot tell them apart/],
    [true, /read-only until it answers/],
  ])(
    "lists this cluster's snapshots (storages unread: %s), with its note",
    async (unread, note) => {
      const snapshot = {
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
      // The snapshot list alone; the storage and cluster lists the dialogs read are empty.
      vi.mocked(apiClient.get).mockImplementation((path: string) =>
        path.includes("/pbs-snapshots")
          ? Promise.resolve({
              items: [snapshot],
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
