import { expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { makeCluster, makeVM } from "@/test/cluster.fixtures";
import { grantPermissions } from "@/test/permissions";
import { useSidebarStore } from "@/stores/sidebar-store";
import { VMTree } from "./VMTree";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// Filing a guest and every folder action take manage:vm_folder: a Viewer
// could drag a guest, its drop was taken, and the folder menus opened, each
// answering 403.
it.each([
  [["view:vm", "manage:vm_folder"], true],
  [["view:vm"], false],
])(
  "with %j files guests and manages folders: %s",
  async (permissions, taken) => {
    grantPermissions(...permissions);
    vi.mocked(apiClient.list).mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path) === "/api/v1/clusters"
          ? [makeCluster()]
          : String(path) === "/api/v1/clusters/c1/vms"
            ? [makeVM({ name: "linux01" })]
            : [],
      ),
    );
    vi.mocked(apiClient.get).mockResolvedValue({
      folders: [
        { id: "f1", cluster_id: "c1", parent_id: null, name: "folder01" },
      ],
      memberships: [],
    });
    useSidebarStore.setState({
      expandedNodes: new Set(["vm-cluster:c1", "vm-folder-unassigned:c1"]),
    });
    renderWithProviders(<VMTree />);

    const toggle = await screen.findByRole("button", {
      name: /^(Expand|Collapse) Discovered$/,
    });
    // fireEvent answers false when the handler took the drop (preventDefault).
    const refused = fireEvent.dragOver(toggle.parentElement as HTMLElement, {
      dataTransfer: { types: ["application/x-nexara-vm"] },
    });
    expect(refused).toBe(!taken);
    const guest = (await screen.findByText(/linux01/)).closest("[draggable]");
    expect(guest?.getAttribute("draggable")).toBe(String(taken));

    // A folder's menu, then the cluster's (opening one closes the other).
    const offered = (name: string) =>
      screen.queryByRole("menuitem", { name }) !== null;
    fireEvent.contextMenu(screen.getByText("folder01"));
    expect([
      offered("New subfolder"),
      offered("Rename"),
      offered("Delete"),
    ]).toEqual([taken, taken, taken]);
    fireEvent.contextMenu(
      screen.getByRole("button", { name: "Collapse cluster test-cluster" }),
    );
    expect(offered("New folder")).toBe(taken);
  },
);
