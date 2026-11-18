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

// An import whose create has not reached Proxmox yet: no UPID.
const unsent: VMImportJob = {
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
const job: VMImportJob = {
  ...unsent,
  upid: "UPID:pve-01:0000A1B2:00C0FFEE:66F1A2B3:qmcreate:101:user@pam!test:",
};
const live: VMImportJob = { ...job, options: { live_import: true } };

// Cancelling an import takes manage:vm_import (the Imports page, which needs
// only view, offered it on every running import), and cancelling a live one
// stops its VM, which takes execute:vm as well.
it.each([
  ["an import", true, job, ["view:vm_import", "manage:vm_import"]],
  ["an import, without manage:vm_import", false, job, ["view:vm_import"]],
  [
    "a live import, without execute:vm",
    false,
    live,
    ["view:vm_import", "manage:vm_import"],
  ],
  [
    "a live import",
    true,
    live,
    ["view:vm_import", "manage:vm_import", "execute:vm"],
  ],
])("offers to cancel %s: %s", async (_name, offered, shown, permissions) => {
  grantPermissions(...permissions);
  vi.mocked(apiClient.list).mockResolvedValue([shown]);
  renderWithProviders(<ImportHistoryTable clusterId="c1" />);

  expect(await screen.findByText("linux01")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Cancel import of linux01" }) !== null,
  ).toBe(offered);
});

// Deleting the VM as well destroys it, which takes delete:vm, as the VM's own
// delete does; an import that never reached Proxmox has no VM of its own.
it.each([
  ["with delete:vm", true, job, ["manage:vm_import", "delete:vm"]],
  ["without delete:vm", false, job, ["manage:vm_import"]],
  [
    "for an import that never reached Proxmox",
    false,
    unsent,
    ["manage:vm_import", "delete:vm"],
  ],
])(
  "offers to delete the VM when cancelling %s: %s",
  async (_name, offered, shown, permissions) => {
    grantPermissions(...permissions);
    vi.mocked(apiClient.list).mockResolvedValue([shown]);
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
