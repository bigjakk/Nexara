import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { createTestQueryClient, renderWithProviders } from "@/test/test-utils";
import { stubApi } from "@/test/fetch-stub";
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
  // not sent, and the action stays disabled rather than confirm and then fail.
  it("keeps maintenance disabled, and asks nothing, without manage:node", () => {
    stubApi({ [CONFIGURED]: { configured: true } });
    const client = createTestQueryClient();
    renderActions(client);

    expect(screen.getByRole("button", { name: "Maintenance" })).toBeDisabled();
    // Read once mounted: a sent check is in flight by now, a disabled one idle.
    expect(client.isFetching({ queryKey: ["ssh-credentials", "c1"] })).toBe(0);
  });
});
