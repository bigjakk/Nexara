import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { grantPermissions } from "@/test/permissions";
import type { VMImportJob } from "@/types/api";
import { ImportHistoryTable } from "./ImportHistoryTable";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const job: VMImportJob = {
  id: "imp-1",
  cluster_id: "c1",
  source_acquisition: "upload",
  source_format: "ova",
  source_ref: "linux01.ova",
  target_node: "pve-01",
  target_storage: "store01",
  target_vmid: 101,
  name: "linux01",
  status: "running",
  warnings: [],
  options: {},
  created_by: "u1",
  created_at: "2026-11-18T00:00:00Z",
  updated_at: "2026-11-18T00:00:00Z",
};

// Cancelling an import takes manage:vm_import; the Imports page, which needs
// only view, offered it on every running import.
it.each([
  [["view:vm_import", "manage:vm_import"], true],
  [["view:vm_import"], false],
])(
  "with %j offers to cancel a running import: %s",
  async (permissions, offered) => {
    grantPermissions(...permissions);
    vi.mocked(apiClient.list).mockResolvedValue([job]);
    renderWithProviders(<ImportHistoryTable clusterId="c1" />);

    expect(await screen.findByText("linux01")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Cancel import of linux01" }) !==
        null,
    ).toBe(offered);
  },
);

// Deleting the VM as well destroys it, which takes delete:vm, as the VM's own
// delete does.
it.each([
  [["manage:vm_import", "delete:vm"], true],
  [["manage:vm_import"], false],
])(
  "with %j offers to delete the VM when cancelling: %s",
  async (permissions, offered) => {
    grantPermissions(...permissions);
    vi.mocked(apiClient.list).mockResolvedValue([job]);
    renderWithProviders(<ImportHistoryTable clusterId="c1" />);

    await userEvent.click(
      await screen.findByRole("button", { name: "Cancel import of linux01" }),
    );
    expect(
      await screen.findByRole("dialog", { name: "Cancel import?" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("checkbox", {
        name: /Also delete the partially created VM/,
      }) !== null,
    ).toBe(offered);
  },
);
