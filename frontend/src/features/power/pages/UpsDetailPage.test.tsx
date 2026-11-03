import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { upsDevice, upsFeed } from "../power.fixtures";
import { UpsDetailPage } from "./UpsDetailPage";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());

describe("UpsDetailPage", () => {
  it("lists upsd's variables with their descriptions and filters them", async () => {
    vi.mocked(apiClient.get).mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path).includes("/metrics")
          ? { range: "1h", bucket_seconds: 10, points: [] }
          : upsDevice({
              description: "Rack UPS",
              vars: { "battery.charge": "100", "input.voltage": "230.4", "ups.status": "OL" },
              var_descriptions: { "input.voltage": "Input voltage (V)" },
            }),
      ),
    );
    vi.mocked(apiClient.list).mockResolvedValue([]);
    const user = userEvent.setup();
    renderWithProviders(
      <Routes>
        <Route path="/power/:upsId" element={<UpsDetailPage />} />
      </Routes>,
      { router: { initialEntries: ["/power/u1"] } },
    );

    await user.click(await screen.findByRole("tab", { name: "Variables (3)" }));
    expect(screen.getByText("battery.charge")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Filter variables"), "input");
    expect(screen.queryByText("battery.charge")).not.toBeInTheDocument();
    expect(screen.getByText("Input voltage (V)")).toBeInTheDocument();
  });

  beforeEach(() => {
    useAuthStore.setState({ globalPermissions: [] });
  });

  // The Powers tab judges each node, and removing one says what it costs: its
  // login will not add it back.
  it("shows what the UPS powers and removes a node with its consequence spelled out", async () => {
    useAuthStore.setState({ globalPermissions: ["view:ups", "manage:ups"] });
    vi.mocked(apiClient.get).mockImplementation((path: unknown) =>
      Promise.resolve(
        String(path).includes("/metrics")
          ? { range: "1h", bucket_seconds: 10, points: [] }
          : upsDevice({
              feeds: [
                upsFeed(),
                upsFeed({ id: "f2", node_name: "pve-03", protection: "not_protected", login_address: "" }),
                upsFeed({ id: "f3", kind: "label", label: "Core switch", node_name: "", cluster_id: null, node_id: null, protection: "", login_address: "", source: "manual" }),
              ],
              clients: [{ address: "192.0.2.11", connections: 1, missed: 0, first_seen_at: "", last_seen_at: "", cluster_id: "c1", node_name: "pve-01" }],
              clients_listed_at: new Date().toISOString(),
            }),
      ),
    );
    vi.mocked(apiClient.list).mockResolvedValue([]);
    vi.mocked(apiClient.delete).mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderWithProviders(
      <Routes>
        <Route path="/power/:upsId" element={<UpsDetailPage />} />
      </Routes>,
      { router: { initialEntries: ["/power/u1"] } },
    );

    await user.click(await screen.findByRole("tab", { name: /Powers \(3\)/ }));
    expect(screen.getByText("Protected")).toBeInTheDocument();
    expect(screen.getByText("Not protected")).toBeInTheDocument();
    expect(screen.getByText("Core switch")).toBeInTheDocument();
    expect(screen.getAllByText("192.0.2.11").length).toBeGreaterThan(0);

    await user.click(screen.getByRole("button", { name: "Remove pve-03 (cluster01)" }));
    expect(await screen.findByText(/A upsmon login from it will not add it back/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove" }));
    expect(apiClient.delete).toHaveBeenCalledWith(expect.stringContaining("/ups-devices/u1/feeds/f2"));
  });
});
