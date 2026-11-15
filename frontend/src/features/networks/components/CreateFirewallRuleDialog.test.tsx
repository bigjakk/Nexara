import { describe, it, expect, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { CreateFirewallRuleDialog } from "./CreateFirewallRuleDialog";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

describe("CreateFirewallRuleDialog", () => {
  // pve-firewall names a group rule's security group in `action`.
  it("offers the cluster's security groups for a Group rule and sends the one chosen as the action", async () => {
    vi.mocked(apiClient.list).mockResolvedValue([
      { group: "webservers" },
      { group: "dbservers" },
    ]);
    vi.mocked(apiClient.post).mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    renderWithProviders(<CreateFirewallRuleDialog clusterId="c1" />);

    await user.click(screen.getByRole("button", { name: /add rule/i }));
    await user.click(screen.getByRole("combobox", { name: "Direction" }));
    await user.click(await screen.findByRole("option", { name: "Group" }));
    await user.click(screen.getByRole("combobox", { name: "Security group" }));
    await user.click(await screen.findByRole("option", { name: "dbservers" }));
    await user.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => {
      expect(apiClient.post).toHaveBeenCalledWith(
        "/api/v1/clusters/c1/firewall/rules",
        { type: "group", action: "dbservers", enable: 1 },
      );
    });
  });
});
