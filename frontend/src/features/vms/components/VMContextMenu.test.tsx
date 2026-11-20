import { describe, expect, it } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { GUEST_ACTION_PERMISSIONS, grantPermissions } from "@/test/permissions";
import { VMContextMenu } from "./VMContextMenu";

// The tree's right-click menu offered every guest action whatever the role.
describe("VMContextMenu", () => {
  it.each([
    [[...GUEST_ACTION_PERMISSIONS, "manage:vm_folder"], true],
    [["view:vm"], false],
  ])("with %j offers the guest actions: %s", (permissions, offered) => {
    grantPermissions(...permissions);
    renderWithProviders(
      <VMContextMenu
        target={{
          clusterId: "c1",
          resourceId: "vm-1",
          vmid: 101,
          name: "linux01",
          kind: "vm",
          status: "running",
          currentNode: "pve-01",
        }}
      >
        <div>linux01</div>
      </VMContextMenu>,
    );
    fireEvent.contextMenu(screen.getByText("linux01"));
    const items = screen.queryAllByRole("menuitem").map((i) => i.textContent);
    for (const name of ["Shutdown", "Clone", "Migrate", "Move to folder"]) {
      expect(items.some((t) => t.includes(name))).toBe(offered);
    }
  });

  // A console and a migration start from the guest's node, which a host may not
  // know (a role refused the node list): neither is offered without it.
  it.each([
    ["pve-01", true],
    ["", false],
  ])("with node %j offers Console and Migrate: %s", (node, offered) => {
    grantPermissions(...GUEST_ACTION_PERMISSIONS, "console:vm");
    renderWithProviders(
      <VMContextMenu
        target={{
          clusterId: "c1",
          resourceId: "vm-1",
          vmid: 101,
          name: "linux01",
          kind: "vm",
          status: "running",
          currentNode: node,
        }}
      >
        <div>linux01</div>
      </VMContextMenu>,
    );
    fireEvent.contextMenu(screen.getByText("linux01"));
    const items = screen.queryAllByRole("menuitem").map((i) => i.textContent);
    expect(items).toContain("Shutdown");
    expect(items.includes("Console")).toBe(offered);
    expect(items.includes("Migrate")).toBe(offered);
  });
});
