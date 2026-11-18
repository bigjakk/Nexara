import type { ReactElement } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { setupUser } from "@/test/user";
import { CreateSDNControllerDialog } from "./CreateSDNControllerDialog";
import { CreateSDNDNSDialog } from "./CreateSDNDNSDialog";
import { CreateSDNIPAMDialog } from "./CreateSDNIPAMDialog";
import { CreateSDNSubnetDialog } from "./CreateSDNSubnetDialog";
import { CreateSDNVNetDialog } from "./CreateSDNVNetDialog";
import { CreateSDNZoneDialog } from "./CreateSDNZoneDialog";
import { InterfaceFormDialog } from "./InterfaceFormDialog";

afterEach(() => {
  vi.unstubAllGlobals();
});

const close = () => undefined;

// For a role without manage:network these dialogs are the only view of an
// object's settings: they open read-only, every value on show and nothing
// that changes or sends one. A Radix Select must say it is disabled itself
// (data-disabled): it opens on pointerdown, which a disabled fieldset may not
// stop.
it.each<{
  name: string;
  ui: ReactElement;
  opener?: string;
  /** Values the fields must show. */
  shows: string[];
  /** How many Selects the dialog draws, once its reads are in. */
  selects: number;
  hides?: string[];
}>([
  {
    name: "zone",
    ui: (
      <CreateSDNZoneDialog
        clusterId="c1"
        initialData={{
          zone: "zone01",
          type: "evpn",
          controller: "ctl01",
          peers: "192.0.2.1,192.0.2.2",
          exitnodes: "pve-01",
          "advertise-subnets": 1,
        }}
        readOnly
      />
    ),
    opener: "View Zone zone01",
    shows: ["192.0.2.1,192.0.2.2", "pve-01"],
    selects: 1,
  },
  {
    name: "VNet",
    ui: (
      <CreateSDNVNetDialog
        clusterId="c1"
        initialData={{ vnet: "vnet01", zone: "zone01", tag: 100, isolate: 1 }}
        readOnly
      />
    ),
    opener: "View VNet vnet01",
    shows: ["100"],
    selects: 1,
  },
  {
    name: "subnet",
    ui: (
      <CreateSDNSubnetDialog
        clusterId="c1"
        vnet="vnet01"
        initialData={{
          subnet: "zone01-192.0.2.0-26",
          gateway: "192.0.2.1",
          "dhcp-range": "start-address=192.0.2.10,end-address=192.0.2.20",
        }}
        readOnly
      />
    ),
    opener: "View Subnet zone01-192.0.2.0-26",
    shows: ["192.0.2.1", "start-address=192.0.2.10,end-address=192.0.2.20"],
    selects: 0,
  },
  {
    name: "controller",
    ui: (
      <CreateSDNControllerDialog
        clusterId="c1"
        initialData={{
          controller: "ctl01",
          type: "isis",
          "isis-domain": "example.com",
          "isis-net": "49.0001.0000.0000.0001.00",
        }}
        readOnly
      />
    ),
    opener: "View Controller ctl01",
    shows: ["example.com", "49.0001.0000.0000.0001.00"],
    selects: 1,
  },
  {
    name: "IPAM",
    ui: (
      <CreateSDNIPAMDialog
        clusterId="c1"
        initialData={{
          ipam: "ipam01",
          type: "netbox",
          url: "https://ipam.example.com/api",
        }}
        readOnly
      />
    ),
    opener: "View IPAM ipam01",
    shows: ["https://ipam.example.com/api"],
    selects: 1,
  },
  {
    name: "DNS",
    ui: (
      <CreateSDNDNSDialog
        clusterId="c1"
        initialData={{
          dns: "dns01",
          type: "powerdns",
          url: "https://dns.example.com/api",
        }}
        readOnly
      />
    ),
    opener: "View DNS dns01",
    shows: ["https://dns.example.com/api"],
    selects: 1,
  },
  // A bond in a mode that takes a hash policy, so only read-only disables
  // that Select; the MTU sits under Advanced, which read-only shows open.
  {
    name: "bond interface",
    ui: (
      <InterfaceFormDialog
        clusterId="c1"
        nodeName="pve-01"
        open
        onOpenChange={close}
        existing={{
          iface: "bond0",
          type: "bond",
          active: 1,
          autostart: 1,
          slaves: "eno1 eno2",
          bond_mode: "802.3ad",
          bond_xmit_hash_policy: "layer3+4",
          mtu: 9000,
        }}
        readOnly
      />
    ),
    shows: ["eno1 eno2", "9000"],
    selects: 2,
    hides: ["Show advanced", "Changes are staged"],
  },
  // Its bridge is a Select once the node's bridges are read.
  {
    name: "OVS interface",
    ui: (
      <InterfaceFormDialog
        clusterId="c1"
        nodeName="pve-01"
        open
        onOpenChange={close}
        existing={{
          iface: "ovsint0",
          type: "OVSIntPort",
          active: 1,
          autostart: 0,
          ovs_bridge: "vmbr1",
          ovs_tag: 100,
        }}
        readOnly
      />
    ),
    shows: ["100"],
    selects: 1,
  },
])(
  "$name: every field is disabled, the values show and nothing saves",
  async ({ ui, opener, shows, selects, hides = [] }) => {
    stubApi({
      "/api/v1/clusters/c1/sdn/zones": listOf([
        { zone: "zone01", type: "simple" },
      ]),
      "/api/v1/clusters/c1/networks/pve-01": listOf([
        { iface: "vmbr1", type: "OVSBridge", active: 1, autostart: 1 },
      ]),
    });
    const user = setupUser();
    renderWithProviders(ui);
    if (opener !== undefined) {
      await user.click(screen.getByRole("button", { name: opener }));
    }
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => {
      expect(within(dialog).queryAllByRole("combobox")).toHaveLength(selects);
    });
    for (const value of shows) {
      expect(within(dialog).getByDisplayValue(value)).toBeInTheDocument();
    }

    const fields = [
      ...dialog.querySelectorAll("input"),
      ...within(dialog).queryAllByRole("combobox"),
      ...within(dialog).queryAllByRole("checkbox"),
    ];
    expect(fields.length).toBeGreaterThan(shows.length);
    for (const field of fields) expect(field).toBeDisabled();
    for (const select of within(dialog).queryAllByRole("combobox")) {
      expect(select).toHaveAttribute("data-disabled");
    }
    expect(
      within(dialog).queryByRole("button", { name: /^(Save|Creat)/ }),
    ).toBeNull();
    for (const button of within(dialog).getAllByRole("button", {
      name: "Close",
    })) {
      expect(button).toBeEnabled();
    }
    for (const text of hides) expect(dialog).not.toHaveTextContent(text);
  },
);
