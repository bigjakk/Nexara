import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { bodiesOf, stubApi } from "@/test/fetch-stub";
import { setupUser } from "@/test/user";
import { CreateSDNVNetDialog } from "./CreateSDNVNetDialog";

const VNET_PATH = "/api/v1/clusters/c1/sdn/vnets/vnet01";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("CreateSDNVNetDialog — edit", () => {
  // Left out, an unticked box changed nothing on Proxmox while the save
  // reported success; 0 is what the update clears it with.
  it("sends 0 for a box unticked on an existing VNet", async () => {
    stubApi({ "/api/v1/clusters/c1/sdn/zones": { items: [], total: 0 } });
    const user = setupUser();
    renderWithProviders(
      <CreateSDNVNetDialog
        clusterId="c1"
        initialData={{
          vnet: "vnet01",
          zone: "zone01",
          vlanaware: 1,
          isolate: 1,
        }}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Edit VNet vnet01" }));
    await user.click(
      await screen.findByRole("checkbox", { name: "Isolate ports" }),
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(bodiesOf("PUT", VNET_PATH)).toEqual([
        { zone: "zone01", vlanaware: 1, isolate: 0 },
      ]);
    });
  });

  // Older pve-network has no isolate-ports, and a delete naming it fails the
  // whole edit; a flag never set must send no key at all.
  it("sends neither flag when neither was ever set", async () => {
    stubApi({ "/api/v1/clusters/c1/sdn/zones": { items: [], total: 0 } });
    const user = setupUser();
    renderWithProviders(
      <CreateSDNVNetDialog
        clusterId="c1"
        initialData={{ vnet: "vnet01", zone: "zone01", alias: "lab" }}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Edit VNet vnet01" }));
    await user.click(await screen.findByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(bodiesOf("PUT", VNET_PATH)).toEqual([
        { zone: "zone01", alias: "lab" },
      ]);
    });
  });
});
