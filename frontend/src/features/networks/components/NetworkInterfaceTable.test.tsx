import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { grantPermissions } from "@/test/permissions";
import { fill, setupUser } from "@/test/user";
import { NetworkInterfaceTable } from "./NetworkInterfaceTable";
import type { NodeInterfaces } from "../types/network";

const CLUSTER = "c1";
const NETWORKS_PATH = `/api/v1/clusters/${CLUSTER}/networks`;

// The same interface name on two nodes, so a delete sent to the wrong node
// is caught.
const NODES: NodeInterfaces[] = [
  {
    node: "pve-01",
    interfaces: [
      { iface: "eno1", type: "eth", active: 1, autostart: 1 },
      { iface: "vmbr0", type: "bridge", active: 1, autostart: 1 },
      { iface: "bond0", type: "bond", active: 1, autostart: 1 },
      { iface: "vmbr1", type: "bridge", active: 1, autostart: 1 },
    ],
  },
  {
    node: "pve-02",
    interfaces: [{ iface: "vmbr1", type: "bridge", active: 1, autostart: 1 }],
  },
];

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  grantPermissions("manage:network", "delete:network");
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// Create, edit, apply and revert take manage:network; delete takes
// delete:network. A Viewer was offered all five, and the server refused them.
// Without manage:network a row's View opens the form read-only instead.
describe("NetworkInterfaceTable — what a role is offered", () => {
  it.each([
    { permissions: [], manage: false, remove: false },
    { permissions: ["manage:network"], manage: true, remove: false },
    { permissions: ["delete:network"], manage: false, remove: true },
  ])(
    "with $permissions offers manage: $manage, delete: $remove",
    async ({ permissions, manage, remove }) => {
      grantPermissions(...permissions);
      stubApi({ [NETWORKS_PATH]: listOf(NODES) });
      const user = setupUser();
      renderWithProviders(<NetworkInterfaceTable clusterId={CLUSTER} />);
      const filter = await screen.findByRole("combobox", {
        name: "Filter by node",
      });
      expect(
        screen.queryByText(/Pick a node to add an interface/) !== null,
      ).toBe(manage);
      fill(filter, "pve-02");

      const offered = (name: string) =>
        screen.queryByRole("button", { name }) !== null;
      expect({
        create: offered("Create interface"),
        apply: offered("Apply config"),
        revert: offered("Revert"),
        edit: offered("Edit vmbr1 on pve-02"),
        view: offered("View vmbr1 on pve-02"),
        delete: offered("Delete vmbr1 on pve-02"),
      }).toEqual({
        create: manage,
        apply: manage,
        revert: manage,
        edit: manage,
        view: !manage,
        delete: remove,
      });

      await user.click(
        screen.getByRole("button", {
          name: `${manage ? "Edit" : "View"} vmbr1 on pve-02`,
        }),
      );
      const dialog = await screen.findByRole("dialog");
      expect(
        within(dialog).queryByRole("button", { name: "Save" }) !== null,
      ).toBe(manage);
    },
  );
});

async function openDeleteFor(label: string) {
  api = stubApi({ [NETWORKS_PATH]: listOf(NODES) });
  const user = userEvent.setup();
  renderWithProviders(<NetworkInterfaceTable clusterId={CLUSTER} />);
  await user.click(await screen.findByRole("button", { name: label }));
  return { user, dialog: await screen.findByRole("alertdialog") };
}

describe("NetworkInterfaceTable — deleting an interface", () => {
  it("asks first, naming the interface and node, and sends nothing", async () => {
    const { dialog } = await openDeleteFor("Delete vmbr1 on pve-02");

    expect(
      within(dialog).getByRole("heading", {
        name: "Delete interface vmbr1 on pve-02?",
      }),
    ).toBeInTheDocument();
    // A delete is a pending change (interfaces.new), not a live one.
    expect(dialog).toHaveTextContent(
      "Proxmox removes the configuration of vmbr1 from the pending network configuration of pve-02. The running network does not change yet",
    );
    // Proxmox's ifupdown2 fork (pve/0003) leaves a bridge up while a guest
    // tap/veth/fwpr port is plugged into it.
    expect(dialog).toHaveTextContent(
      "Once the change takes effect, vmbr1 is taken down with every address on it, unless a running guest is still plugged into it: then Apply leaves it up until pve-02 reboots, after which it is gone and guests configured on it cannot use it. Proxmox does not check guest configs.",
    );
    expect(dialog).not.toHaveTextContent("physical NIC");
    expect(api.writes()).toEqual([]);
  });

  it("says any other interface is taken down, with no bridge exception", async () => {
    const { dialog } = await openDeleteFor("Delete bond0 on pve-01");

    expect(dialog).toHaveTextContent(
      "Once the change takes effect, bond0 is taken down with every address on it. Proxmox does not check whether a guest's network device uses it.",
    );
    expect(dialog).not.toHaveTextContent("running guest");
  });

  // Proxmox lists every physical link the kernel reports, configured or
  // not, so deleting a NIC's stanza does not make the NIC go away.
  it("says a physical NIC stays, unconfigured", async () => {
    const { dialog } = await openDeleteFor("Delete eno1 on pve-01");

    expect(dialog).toHaveTextContent(
      "eno1 is a physical NIC, so it is not removed: once the change takes effect, its addresses and other settings are gone, and Proxmox lists it again with no configuration.",
    );
    expect(dialog).not.toHaveTextContent("taken down");
  });

  it("Cancel sends nothing", async () => {
    const { user, dialog } = await openDeleteFor("Delete vmbr1 on pve-02");

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming sends one DELETE for that interface on that node", async () => {
    const { user, dialog } = await openDeleteFor("Delete vmbr1 on pve-02");

    await user.click(
      within(dialog).getByRole("button", { name: "Delete interface" }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([`DELETE ${NETWORKS_PATH}/pve-02/vmbr1`]);
    });
  });
});

describe("NetworkInterfaceTable — Apply Config", () => {
  it("asks first, naming the node picked in the filter, and applies there only on confirm", async () => {
    api = stubApi({ [NETWORKS_PATH]: listOf(NODES) });
    const user = setupUser();
    renderWithProviders(<NetworkInterfaceTable clusterId={CLUSTER} />);
    fill(
      await screen.findByRole("combobox", { name: "Filter by node" }),
      "pve-02",
    );

    await user.click(screen.getByRole("button", { name: "Apply config" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByRole("heading", {
        name: "Apply the pending network changes on pve-02?",
      }),
    ).toBeInTheDocument();
    expect(api.writes()).toEqual([]);

    await user.click(
      within(dialog).getByRole("button", { name: "Apply config" }),
    );
    await waitFor(() => {
      expect(api.writes()).toEqual([`POST ${NETWORKS_PATH}/pve-02/apply`]);
    });
  });
});
