import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { bodiesOf, stubApi } from "@/test/fetch-stub";
import { grantPermissions } from "@/test/permissions";
import { setupUser } from "@/test/user";
import { FirewallOptionsCard } from "./FirewallOptionsCard";

const OPTIONS_PATH = "/api/v1/clusters/c1/firewall/options";

afterEach(() => {
  vi.unstubAllGlobals();
});

// The options route takes manage:network. Without it the card is read-only:
// the state and both policies stay on show, and nothing on it changes them.
it.each([
  { permissions: [], editable: false },
  { permissions: ["manage:network"], editable: true },
])(
  "with $permissions the options are editable: $editable",
  async ({ permissions, editable }) => {
    grantPermissions(...permissions);
    stubApi({ [OPTIONS_PATH]: { enable: 1, policy_in: "DROP" } });
    renderWithProviders(<FirewallOptionsCard clusterId="c1" />);
    const inbound = await screen.findByRole("combobox", {
      name: "Inbound policy",
    });
    const outbound = screen.getByRole("combobox", { name: "Outbound policy" });

    expect(screen.getByText("Enabled")).toBeInTheDocument();
    expect(inbound).toHaveTextContent("DROP");
    expect({
      toggle:
        screen.queryByRole("button", { name: "Disable firewall" }) !== null,
      inbound: !inbound.hasAttribute("disabled"),
      outbound: !outbound.hasAttribute("disabled"),
      savedNote: screen.queryByText(/saved as soon as you make it/) !== null,
    }).toEqual({
      toggle: editable,
      inbound: editable,
      outbound: editable,
      savedNote: editable,
    });
  },
);

describe("FirewallOptionsCard — Disable Firewall", () => {
  // Turning the cluster firewall off stops every rule at once, so it asks
  // first and sends nothing until confirmed.
  it("asks first and turns the firewall off only on confirm", async () => {
    grantPermissions("manage:network");
    const api = stubApi({ [OPTIONS_PATH]: { enable: 1 } });
    const user = setupUser();
    renderWithProviders(<FirewallOptionsCard clusterId="c1" />);

    await user.click(
      await screen.findByRole("button", { name: "Disable firewall" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByRole("heading", {
        name: "Turn off the cluster firewall?",
      }),
    ).toBeInTheDocument();
    expect(dialog).toHaveTextContent(
      "the cluster's own rules and those of every node and guest",
    );
    expect(api.writes()).toEqual([]);

    await user.click(
      within(dialog).getByRole("button", { name: "Disable firewall" }),
    );
    await waitFor(() => {
      expect(bodiesOf("PUT", OPTIONS_PATH)).toEqual([{ enable: 0 }]);
    });
  });
});
