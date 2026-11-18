import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { makeNode, makeVM } from "@/test/cluster.fixtures";
import type { ClusterResponse } from "@/types/api";
import { grantPermissions } from "@/test/permissions";
import { InventoryTree } from "./InventoryTree";

function cluster(id: string, name: string): ClusterResponse {
  return {
    id,
    name,
    api_url: "https://192.0.2.10:8006",
    token_id: "user01@pve!token01",
    tls_fingerprint: "",
    sync_interval_seconds: 30,
    is_active: true,
    status: "online",
    pve_version: "9.0.6",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    credential_source: "manual",
  };
}

let api: ReturnType<typeof stubApi>;

afterEach(() => {
  vi.unstubAllGlobals();
});

// Delete cluster takes the cluster's whole branch away — the Actions button
// that opened it, and the dialog itself, which lives in the branch.
describe("InventoryTree — focus after deleting a cluster", () => {
  it("puts focus on the tree once the deleted cluster's branch has gone", async () => {
    const reads: Record<string, unknown> = {};
    // Read back without cluster02 once it has been deleted.
    Object.defineProperty(reads, "/api/v1/clusters", {
      enumerable: true,
      get: () =>
        listOf(
          api.sent.some((r) => r.startsWith("DELETE /api/v1/clusters/c2"))
            ? [cluster("c1", "cluster01")]
            : [cluster("c1", "cluster01"), cluster("c2", "cluster02")],
        ),
    });
    api = stubApi(reads);
    grantPermissions("view:cluster", "delete:cluster");
    const user = userEvent.setup();
    renderWithProviders(<InventoryTree />);
    await user.click(
      await screen.findByRole("button", { name: "Actions for cluster02" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(
      within(dialog).getByPlaceholderText("cluster02"),
      "cluster02",
    );

    await user.click(
      within(dialog).getByRole("button", { name: "Delete cluster" }),
    );

    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Actions for cluster02" }),
      ).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("group", { name: "Datacenter" }),
      );
    });
  });
});

// Edit takes manage:cluster and Delete delete:cluster, on the row's ⋯ menu and
// on its right-click menu alike; a Viewer got both, each answering 403.
it.each([
  [["view:cluster", "manage:cluster", "delete:cluster", "manage:vm"], true],
  [["view:cluster"], false],
])(
  "with %j offers a cluster's Edit and Delete: %s",
  async (permissions, offered) => {
    grantPermissions(...permissions);
    api = stubApi({ "/api/v1/clusters": listOf([cluster("c1", "cluster01")]) });
    const user = userEvent.setup();
    renderWithProviders(<InventoryTree />);
    const actions = await screen.findByRole("button", {
      name: "Actions for cluster01",
    });
    const items = () =>
      ["Edit", "Delete"].map(
        (name) => screen.queryByRole("menuitem", { name }) !== null,
      );
    await user.click(actions);
    expect(items()).toEqual([offered, offered]);
    await user.keyboard("{Escape}");
    fireEvent.contextMenu(actions);
    expect(items()).toEqual([offered, offered]);
    // A separator under Create VM, and one over Edit; a Viewer has neither.
    expect(
      within(screen.getByRole("menu")).queryAllByRole("separator"),
    ).toHaveLength(offered ? 2 : 0);
  },
);

describe("InventoryTree — heading", () => {
  it("links the Datacenter heading to the Clusters page", async () => {
    api = stubApi({ "/api/v1/clusters": listOf([cluster("c1", "cluster01")]) });
    renderWithProviders(<InventoryTree />);

    expect(
      await screen.findByRole("link", { name: "Datacenter" }),
    ).toHaveAttribute("href", "/clusters");
  });
});

describe("InventoryTree — name filter", () => {
  // A collapsed cluster used to stay dimmed and unsearched: the filter loads
  // its rows, and holds it open over the match.
  it("finds a guest in a cluster nobody has expanded", async () => {
    api = stubApi({
      "/api/v1/clusters": listOf([cluster("c1", "cluster01")]),
      "/api/v1/clusters/c1/nodes": listOf([
        makeNode({ id: "n1", name: "pve-01" }),
      ]),
      "/api/v1/clusters/c1/vms": listOf([
        makeVM({ id: "v1", node_id: "n1", vmid: 107, name: "linux07" }),
      ]),
    });
    renderWithProviders(<InventoryTree filter="linux07" />);

    expect(await screen.findByText(/linux07/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Collapse cluster cluster01" }),
    ).toHaveAttribute("aria-expanded", "true");
  });
});
