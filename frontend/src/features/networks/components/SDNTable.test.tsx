import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
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

afterEach(() => {
  vi.unstubAllGlobals();
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
  await user.click(await screen.findByRole("tab", { name: "VNets" }));
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
      within(dialog).getByRole("button", { name: "Delete Subnet" }),
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
      within(dialog).getByRole("button", { name: "Delete Subnet" }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([
        `DELETE ${SDN}/vnets/vnet02/subnets/${TARGET}`,
      ]);
    });
  });
});
