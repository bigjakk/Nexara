import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { bodiesOf, stubApi } from "@/test/fetch-stub";
import { setupUser } from "@/test/user";
import { FirewallOptionsCard } from "./FirewallOptionsCard";

const OPTIONS_PATH = "/api/v1/clusters/c1/firewall/options";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("FirewallOptionsCard — Disable Firewall", () => {
  // Turning the cluster firewall off stops every rule at once, so it asks
  // first and sends nothing until confirmed.
  it("asks first and turns the firewall off only on confirm", async () => {
    const api = stubApi({ [OPTIONS_PATH]: { enable: 1 } });
    const user = setupUser();
    renderWithProviders(<FirewallOptionsCard clusterId="c1" />);

    await user.click(
      await screen.findByRole("button", { name: "Disable Firewall" }),
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
      within(dialog).getByRole("button", { name: "Disable Firewall" }),
    );
    await waitFor(() => {
      expect(bodiesOf("PUT", OPTIONS_PATH)).toEqual([{ enable: 0 }]);
    });
  });
});
