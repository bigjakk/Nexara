import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { grantPermissions } from "@/test/permissions";
import { setupUser } from "@/test/user";
import { SDNTable } from "./SDNTable";
import type { SDNSubnet, SDNVNet, SDNZone } from "../types/network";

const CLUSTER = "c1";
const SDN = `/api/v1/clusters/${CLUSTER}/sdn`;

const ZONES: SDNZone[] = [{ zone: "zone01", type: "simple" }];
const VNETS: SDNVNet[] = [
  { vnet: "vnet01", zone: "zone01" },
  { vnet: "vnet02", zone: "zone01" },
];
// Two VNets with subnets of their own, so a delete sent under the wrong VNet,
// or for the wrong subnet, is caught.
const VNET01_SUBNETS: SDNSubnet[] = [
  { subnet: "zone01-192.0.2.0-26", gateway: "192.0.2.1" },
];
const VNET02_SUBNETS: SDNSubnet[] = [
  { subnet: "zone01-192.0.2.64-26", gateway: "192.0.2.65" },
  { subnet: "zone01-192.0.2.128-26", gateway: "192.0.2.129" },
];

const TARGET = "zone01-192.0.2.128-26";

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  grantPermissions("manage:network", "delete:network");
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// Creating, editing and applying SDN objects take manage:network; deleting
// one takes delete:network. A Viewer was offered every one of them. Without
// manage:network each row's View opens its dialog read-only instead.
describe("SDNTable — what a role is offered", () => {
  // Each kind's view, the create button it adds, and its one fixture row.
  const KINDS = [
    { view: "Zones", create: "Create zone", kind: "Zone", name: "zone01" },
    { view: "VNets", create: "Create VNet", kind: "VNet", name: "vnet01" },
    {
      view: "Controllers",
      create: "Create controller",
      kind: "Controller",
      name: "ctl01",
    },
    { view: "IPAM", create: "Create IPAM", kind: "IPAM", name: "ipam01" },
    { view: "DNS", create: "Create DNS", kind: "DNS", name: "dns01" },
  ];
  const SUBNET = "zone01-192.0.2.0-26";

  it.each([
    { permissions: [], manage: false, remove: false },
    { permissions: ["manage:network"], manage: true, remove: false },
    { permissions: ["delete:network"], manage: false, remove: true },
  ])(
    "with $permissions offers create, edit and apply: $manage, delete: $remove",
    async ({ permissions, manage, remove }) => {
      grantPermissions(...permissions);
      stubApi({
        [`${SDN}/zones`]: listOf(ZONES),
        [`${SDN}/vnets`]: listOf(VNETS),
        [`${SDN}/vnets/vnet01/subnets`]: listOf(VNET01_SUBNETS),
        [`${SDN}/controllers`]: listOf([{ controller: "ctl01", type: "evpn" }]),
        [`${SDN}/ipams`]: listOf([{ ipam: "ipam01", type: "pve" }]),
        [`${SDN}/dns`]: listOf([{ dns: "dns01", type: "powerdns" }]),
      });
      const user = setupUser();
      renderWithProviders(<SDNTable clusterId={CLUSTER} />);
      const seen: Record<string, boolean> = {};
      const look = (...names: string[]) => {
        for (const name of names) {
          seen[name] = screen.queryByRole("button", { name }) !== null;
        }
      };

      const row = (kind: string, name: string) =>
        ["Edit", "View", "Delete"].map((verb) => `${verb} ${kind} ${name}`);
      for (const { view, create, kind, name } of KINDS) {
        await user.click(await screen.findByRole("button", { name: view }));
        await screen.findByText(name);
        look(create, ...row(kind, name));
      }
      look("Apply changes");
      // A VNet's subnets show once it is expanded.
      await user.click(screen.getByRole("button", { name: "VNets" }));
      await user.click(await screen.findByText("vnet01"));
      await screen.findByText(SUBNET);
      look("Add subnet", ...row("Subnet", SUBNET));

      expect(seen).toEqual(
        Object.fromEntries(
          Object.keys(seen).map((name) => [
            name,
            name.startsWith("Delete ")
              ? remove
              : name.startsWith("View ")
                ? !manage
                : manage,
          ]),
        ),
      );
      await user.click(
        screen.getByRole("button", {
          name: `${manage ? "Edit" : "View"} Subnet ${SUBNET}`,
        }),
      );
      const dialog = await screen.findByRole("dialog");
      expect(
        within(dialog).queryByRole("button", { name: "Save" }) !== null,
      ).toBe(manage);
    },
  );
});

async function openDeleteFor(subnet: string) {
  api = stubApi({
    [`${SDN}/zones`]: listOf(ZONES),
    [`${SDN}/vnets`]: listOf(VNETS),
    [`${SDN}/vnets/vnet01/subnets`]: listOf(VNET01_SUBNETS),
    [`${SDN}/vnets/vnet02/subnets`]: listOf(VNET02_SUBNETS),
  });
  const user = userEvent.setup();
  renderWithProviders(<SDNTable clusterId={CLUSTER} />);
  await user.click(await screen.findByRole("button", { name: "VNets" }));
  // Expand both VNets, so both subnet lists are on screen.
  await user.click(await screen.findByText("vnet01"));
  await user.click(screen.getByText("vnet02"));
  await user.click(
    await screen.findByRole("button", { name: `Delete Subnet ${subnet}` }),
  );
  return { user, dialog: await screen.findByRole("alertdialog") };
}

describe("SDNTable — deleting a subnet", () => {
  it("asks first, naming the subnet and its VNet, and sends nothing", async () => {
    const { dialog } = await openDeleteFor(TARGET);

    expect(
      within(dialog).getByRole("heading", { name: `Delete subnet ${TARGET}?` }),
    ).toBeInTheDocument();
    expect(dialog).toHaveTextContent(
      `Proxmox removes subnet ${TARGET} from VNet vnet02 in the SDN configuration now.`,
    );
    expect(dialog).toHaveTextContent(
      "The nodes keep using the subnet until SDN changes are applied",
    );
    expect(api.writes()).toEqual([]);
  });

  it("names the VNet of the subnet it was opened for", async () => {
    const { user, dialog } = await openDeleteFor("zone01-192.0.2.0-26");

    expect(dialog).toHaveTextContent(
      "Proxmox removes subnet zone01-192.0.2.0-26 from VNet vnet01 in the SDN configuration now.",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Delete subnet" }),
    );
    await waitFor(() => {
      expect(api.writes()).toEqual([
        `DELETE ${SDN}/vnets/vnet01/subnets/zone01-192.0.2.0-26`,
      ]);
    });
  });

  it("Cancel sends nothing", async () => {
    const { user, dialog } = await openDeleteFor(TARGET);

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming sends one DELETE for that subnet under its own VNet", async () => {
    const { user, dialog } = await openDeleteFor(TARGET);

    await user.click(
      within(dialog).getByRole("button", { name: "Delete subnet" }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([
        `DELETE ${SDN}/vnets/vnet02/subnets/${TARGET}`,
      ]);
    });
  });
});

// The row's Delete button and its dialog live in the row, and go with it:
// the dialog unmounts with the row it deleted.
describe("SDNTable — focus after a delete", () => {
  it("puts focus on the VNets tab panel once the deleted VNet's row has gone", async () => {
    const reads: Record<string, unknown> = {
      [`${SDN}/zones`]: listOf(ZONES),
      [`${SDN}/vnets/vnet01/subnets`]: listOf(VNET01_SUBNETS),
      [`${SDN}/vnets/vnet02/subnets`]: listOf(VNET02_SUBNETS),
    };
    // Read back without vnet02 once it has been deleted.
    Object.defineProperty(reads, `${SDN}/vnets`, {
      enumerable: true,
      get: () =>
        listOf(
          api.writes().includes(`DELETE ${SDN}/vnets/vnet02`)
            ? VNETS.filter((v) => v.vnet !== "vnet02")
            : VNETS,
        ),
    });
    api = stubApi(reads);
    const user = userEvent.setup();
    renderWithProviders(<SDNTable clusterId={CLUSTER} />);
    await user.click(await screen.findByRole("button", { name: "VNets" }));
    await user.click(
      await screen.findByRole("button", { name: "Delete VNet vnet02" }),
    );
    const dialog = await screen.findByRole("dialog");

    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Delete VNet vnet02" }),
      ).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("tabpanel", { name: "VNets" }),
      );
    });
  });
});
