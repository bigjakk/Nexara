import { beforeEach, expect, it, vi } from "vitest";
import { fireEvent, screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { GUEST_ACTION_PERMISSIONS, grantPermissions } from "@/test/permissions";
import { apiClient } from "@/lib/api-client";
import type { VMResponse } from "@/types/api";
import type { StorageContentItem } from "../types/storage";
import { StorageGuestTable } from "./StorageGuestTable";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

function volume(content: string, vmid: number): StorageContentItem {
  return {
    volid: `store01:vm-${String(vmid)}-disk-0`,
    format: "raw",
    size: 1024,
    ctime: 1_700_000_000,
    content,
    vmid,
  };
}

function guest(id: string, vmid: number, name: string): VMResponse {
  return { id, vmid, name, status: "stopped", node_id: "n1" } as VMResponse;
}

beforeEach(() => {
  vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
    const p = String(path);
    if (p.endsWith("/vms")) {
      return Promise.resolve([guest("v1", 101, "linux01")]);
    }
    if (p.endsWith("/containers")) {
      return Promise.resolve([guest("t1", 102, "linux02")]);
    }
    return Promise.resolve([]);
  });
});

// A VM's disk move takes manage:vm and a container's volume move
// execute:container, so each row offers its migration (button, selection,
// context menu) only for its kind.
it.each<[string, string[], boolean, boolean]>([
  ["no grant", [], false, false],
  ["manage:vm", ["manage:vm"], true, false],
  ["execute:container", ["execute:container"], false, true],
])("offers a migration by the guest's kind: %s", async (_, grants, vm, ct) => {
  grantPermissions("view:storage", ...grants);
  renderWithProviders(
    <StorageGuestTable
      items={[volume("images", 101), volume("rootdir", 102)]}
      clusterId="c1"
      migrateTargets={["store02"]}
    />,
  );

  for (const [name, offered] of [
    ["linux01", vm],
    ["linux02", ct],
  ] as const) {
    const row = (await screen.findByText(name)).closest("tr");
    if (row === null) throw new Error(`no row for ${name}`);
    expect(
      within(row).queryByRole("button", { name: /^Migrate/ }) !== null,
      name,
    ).toBe(offered);
    expect(
      screen.queryByRole("checkbox", { name: `Select ${name}` }) !== null,
      name,
    ).toBe(offered);
  }
  expect(
    screen.queryByRole("checkbox", { name: "Select all guests" }) !== null,
  ).toBe(vm || ct);

  fireEvent.contextMenu(screen.getByText("linux01"));
  expect(
    screen.queryByRole("menuitem", { name: /^Migrate disk/ }) !== null,
  ).toBe(vm);
});

// A row's menu is the tree's guest menu, led by this store's disk migration,
// from a right click or the row's actions button. A volume no guest in the
// inventory owns has no guest to act on, so it has neither.
it.each([
  ["a right click", () => fireEvent.contextMenu(screen.getByText("linux01"))],
  [
    "the actions button",
    () =>
      fireEvent.click(
        screen.getByRole("button", { name: "Actions for linux01" }),
      ),
  ],
])("opens the guest menu on %s", async (_, open) => {
  grantPermissions("view:storage", ...GUEST_ACTION_PERMISSIONS);
  renderWithProviders(
    <StorageGuestTable
      items={[volume("images", 101), volume("images", 109)]}
      clusterId="c1"
      migrateTargets={["store02"]}
    />,
  );
  await screen.findByText("linux01");

  fireEvent.contextMenu(screen.getByText("(unknown VM)"));
  expect(screen.queryAllByRole("menuitem")).toHaveLength(0);
  expect(
    screen.queryByRole("button", { name: "Actions for (unknown VM)" }),
  ).toBeNull();

  open();
  const items = screen.getAllByRole("menuitem").map((i) => i.textContent);
  expect(items[0]).toBe("Migrate disk");
  expect(items).toEqual(expect.arrayContaining(["Start", "Clone"]));
});
