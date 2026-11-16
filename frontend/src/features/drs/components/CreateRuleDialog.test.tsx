import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { makeCluster } from "@/test/cluster.fixtures";
import { CreateRuleDialog } from "./CreateRuleDialog";
import { useCluster } from "@/features/clusters/api/cluster-queries";

vi.mock("../api/drs-queries", () => ({
  useCreateDRSRule: () => ({ mutate: vi.fn(), isPending: false }),
  useCreateHARule: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@/features/clusters/api/cluster-queries", () => ({
  useCluster: vi.fn(),
  useClusterVMs: () => ({ data: [] }),
  useClusterNodes: () => ({ data: [] }),
}));

// Proxmox HA rules exist from PVE 9.0; on PVE 8, or before the version is
// known, the dialog offers only a Nexara rule (the API would answer 400).
describe("CreateRuleDialog", () => {
  it.each([
    ["9.0", true],
    ["8.4", false],
    ["", false],
  ])("on PVE %j offers a Proxmox HA target: %s", async (version, offered) => {
    vi.mocked(useCluster).mockReturnValue({
      data: makeCluster({ pve_version: version }),
    } as unknown as ReturnType<typeof useCluster>);
    renderWithProviders(<CreateRuleDialog clusterId="c1" />);
    await userEvent.click(screen.getByRole("button", { name: /add rule/i }));
    expect(screen.queryByText("Target") !== null).toBe(offered);
  });
});
