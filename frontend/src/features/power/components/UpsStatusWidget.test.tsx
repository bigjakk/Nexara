import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { upsDevice } from "../power.fixtures";
import { UpsStatusWidget } from "./UpsStatusWidget";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());

describe("UpsStatusWidget", () => {
  it("lists each UPS with its status and what it has left, never a stale figure", async () => {
    vi.mocked(apiClient.list).mockResolvedValue([
      upsDevice({ state: "on_battery", flags: ["OB"], battery_charge: 52, battery_runtime: 580 }),
      upsDevice({ id: "u2", name: "ups02", state: "unknown", last_known_state: "on_battery", reachability: "unreachable" }),
    ]);
    renderWithProviders(<UpsStatusWidget />);

    expect(await screen.findByRole("link", { name: "ups01" })).toHaveAttribute("href", "/power/u1");
    expect(screen.getByText("On battery")).toBeInTheDocument();
    expect(screen.getByText("52% · 9m 40s")).toBeInTheDocument();
    expect(screen.getByText("Unreachable")).toBeInTheDocument();
    expect(screen.getByText("— · —")).toBeInTheDocument();
  });
});
