import { afterEach, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { grantPermissions } from "@/test/permissions";
import { setupUser } from "@/test/user";
import { NetworkTab } from "./NodeNetworkTab";

afterEach(() => {
  vi.unstubAllGlobals();
});

// An interface's edit takes manage:network and its delete delete:network; a
// Viewer was offered both on every row, and the server refused them. Without
// manage:network the row's View opens the form read-only instead.
it.each([
  { permissions: [], edit: false, remove: false },
  { permissions: ["manage:network"], edit: true, remove: false },
  { permissions: ["delete:network"], edit: false, remove: true },
])(
  "with $permissions offers a row's edit: $edit, delete: $remove",
  async ({ permissions, edit, remove }) => {
    grantPermissions(...permissions);
    stubApi({
      "/api/v1/clusters/c1/networks/pve-01": listOf([
        { iface: "vmbr0", type: "bridge", active: 1, autostart: 1 },
      ]),
    });
    const user = setupUser();
    renderWithProviders(<NetworkTab clusterId="c1" nodeName="pve-01" />);
    await screen.findByText("vmbr0");

    const offered = (name: string) =>
      screen.queryByRole("button", { name }) !== null;
    expect({
      edit: offered("Edit vmbr0"),
      view: offered("View vmbr0"),
      remove: offered("Delete vmbr0"),
    }).toEqual({ edit, view: !edit, remove });

    await user.click(
      screen.getByRole("button", { name: `${edit ? "Edit" : "View"} vmbr0` }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).queryByRole("button", { name: "Save" }) !== null,
    ).toBe(edit);
  },
);
