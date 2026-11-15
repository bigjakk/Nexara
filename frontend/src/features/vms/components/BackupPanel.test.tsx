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
  it("lists the snapshots of this cluster's datastores, with the shared-datastore note", async () => {
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
    vi.mocked(apiClient.list).mockImplementation((path: string) =>
      Promise.resolve(path.includes("/pbs-snapshots") ? [snapshot] : []),
    );
    renderWithProviders(
      <BackupPanel vmid={101} clusterId="c1" nodeName="pve-01" kind="vm" />,
    );

    expect(
      await screen.findByText(/PBS cannot tell them apart/),
    ).toBeInTheDocument();
    expect(vi.mocked(apiClient.list)).toHaveBeenCalledWith(
      "/api/v1/clusters/c1/pbs-snapshots?backup_id=101",
    );
  });
});
