import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { createTestQueryClient, renderWithProviders } from "@/test/test-utils";
import { stubApi } from "@/test/fetch-stub";
import { setupUser } from "@/test/user";
import { useAuthStore } from "@/stores/auth-store";
import { NodePowerActions } from "./NodePowerActions";

const CONFIGURED = "/api/v1/clusters/c1/ssh-credentials/configured";

afterEach(() => {
  vi.unstubAllGlobals();
  useAuthStore.setState({ user: null, permissions: [] });
});

function renderActions(client = createTestQueryClient()) {
  renderWithProviders(
    <NodePowerActions
      clusterId="c1"
      nodeName="pve-01"
      otherNodes={["pve-02"]}
      inMaintenance={false}
      canReport={false}
    />,
    { client },
  );
}

// Maintenance runs over SSH. Whether the cluster has a credential comes from
// the boolean route a built-in Operator may read, not from the credential read
// (left unstubbed here, so it fails as an Operator's 403 would).
describe("NodePowerActions", () => {
  it("offers maintenance when the cluster says it has SSH credentials", async () => {
    useAuthStore.setState({ user: null, permissions: ["manage:node"] });
    stubApi({ [CONFIGURED]: { configured: true } });
    renderActions();

    expect(
      await screen.findByRole("button", { name: "Enter maintenance" }),
    ).toBeInTheDocument();
  });

  // A Viewer's check could only 403, which reads as "could not check": it is
  // not sent, and maintenance, which takes manage:node, is not offered at all.
  it("offers no maintenance, and asks nothing, without manage:node", () => {
    stubApi({ [CONFIGURED]: { configured: true } });
    const client = createTestQueryClient();
    renderActions(client);

    expect(screen.queryByRole("button", { name: /maintenance/i })).toBeNull();
    // Read once mounted: a sent check is in flight by now, a disabled one idle.
    expect(client.isFetching({ queryKey: ["ssh-credentials", "c1"] })).toBe(0);
  });

  // Every item of More actions takes manage:node: without it there is no menu.
  it.each([
    { granted: [] as string[], items: [] as string[] },
    { granted: ["manage:node"], items: ["Evacuate", "Reboot", "Shutdown"] },
  ])("More actions with $granted offers $items", async ({ granted, items }) => {
    useAuthStore.setState({ user: null, permissions: granted });
    stubApi({ [CONFIGURED]: { configured: true } });
    renderActions();

    const menu = screen.queryByRole("button", { name: "More actions" });
    if (menu !== null) await setupUser().click(menu);
    await waitFor(() => {
      expect(
        screen.queryAllByRole("menuitem").map((item) => item.textContent),
      ).toEqual(items);
    });
  });

  // With no SSH credential, only a role that may set one (manage:ssh_credentials)
  // is told how to.
  it.each([
    [
      ["manage:node"],
      "Node maintenance needs SSH credentials for this cluster. Use Evacuate instead.",
    ],
    [
      ["manage:node", "manage:ssh_credentials"],
      "Configure SSH credentials (Security → Rolling updates → SSH access) to use node maintenance, or use Evacuate.",
    ],
  ])(
    "with %j and no SSH credential, Maintenance says %j",
    async (permissions, title) => {
      useAuthStore.setState({ user: null, permissions });
      stubApi({ [CONFIGURED]: { configured: false } });
      renderActions();

      expect(
        await screen.findByRole("button", { name: "Maintenance" }),
      ).toHaveAttribute("title", title);
    },
  );
});
