import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { nutServer, upsDevice as device, upsFeed } from "../power.fixtures";
import { PowerPage } from "./PowerPage";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());

describe("PowerPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useAuthStore.setState({ permissions: ["view:ups"], globalPermissions: ["view:ups"] });
  });

  it("shows each UPS by what Nexara could read, and the outage it can see", async () => {
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.includes("nut-servers")) return Promise.resolve([nutServer()]);
      if (p.includes("ups-devices")) {
        return Promise.resolve([
          device({
            state: "on_battery", status: "OB DISCHRG", flags: ["OB", "DISCHRG"], battery_runtime: 580,
            feeds: [upsFeed(), upsFeed({ id: "f2", node_name: "pve-03", protection: "not_protected", login_address: "" })],
          }),
          // Last seen on battery, now unreadable: shown as unreachable, and
          // left out of the outage banner.
          device({ id: "u2", name: "ups02", state: "unknown", last_known_state: "on_battery", flags: ["OB"], reachability: "unreachable" }),
        ]);
      }
      return Promise.resolve([]);
    });

    renderWithProviders(<PowerPage />);

    expect(await screen.findByText(/ups01 is on battery/)).toBeInTheDocument();
    expect(screen.getByText(/Shortest runtime: ups01, 9m 40s/)).toBeInTheDocument();
    // What the outage will cost, in the banner, the summary and the card.
    expect(screen.getByText("Will lose power without shutting down: pve-03 (cluster01).")).toBeInTheDocument();
    expect(screen.getByText("1 of 2 nodes")).toBeInTheDocument();
    const cards = screen.getAllByRole("link", { name: /^ups0[12]$/ }).map((a) => a.closest("div.rounded-xl") as HTMLElement);
    expect(within(cards[0] as HTMLElement).getByText("On battery")).toBeInTheDocument();
    expect(within(cards[0] as HTMLElement).getByText("Not protected")).toBeInTheDocument();
    expect(within(cards[1] as HTMLElement).getByText("Unreachable")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /add nut server/i })).not.toBeInTheDocument();
  });
});
