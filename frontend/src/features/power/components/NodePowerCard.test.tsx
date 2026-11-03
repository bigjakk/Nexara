import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { nutServer, upsDevice, upsFeed } from "../power.fixtures";
import type { UpsDevice } from "../types/ups";
import { NodePowerCard } from "./NodePowerCard";

vi.mock("@/lib/api-client", async () => (await import("@/test/mocks")).apiClientMock());

function serve(devices: UpsDevice[]) {
  vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
    const p = String(path);
    if (p.includes("nut-servers")) return Promise.resolve([nutServer({ id: "s1", host: "192.0.2.40", port: 3493 })]);
    if (p.includes("ups-devices")) return Promise.resolve(devices);
    return Promise.resolve([]);
  });
}

// ups02 is on battery and pve-03 has no upsmon on it.
const outage = (feeds: UpsDevice["feeds"]) =>
  upsDevice({ name: "ups02", state: "on_battery", status: "OB DISCHRG", flags: ["OB", "DISCHRG"], feeds });
const unprotected = upsFeed({ id: "f1", node_name: "pve-03", protection: "not_protected", login_address: "" });

describe("NodePowerCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useAuthStore.setState({ globalPermissions: ["view:ups"], permissions: ["view:ups", "manage:node"] });
  });

  // The lines that make the node a secondary of the UPS's server, and the way
  // to shut it down now. ups03 powers a node of the same name on another
  // cluster, which is not this one.
  it("tells a node it is not protected, how to fix it, and offers a shutdown during an outage", async () => {
    serve([
      outage([unprotected, upsFeed({ id: "f2" })]),
      upsDevice({ id: "u3", name: "ups03", feeds: [upsFeed({ id: "f3", cluster_id: "c2", cluster_name: "cluster02", node_name: "pve-03" })] }),
    ]);

    renderWithProviders(<NodePowerCard clusterId="c1" nodeName="pve-03" online />);

    expect(await screen.findByRole("link", { name: "ups02" })).toBeInTheDocument();
    expect(screen.getByText("Not protected")).toBeInTheDocument();
    expect(screen.getByText(/Also powers pve-01 \(cluster01\)/)).toBeInTheDocument();
    expect(screen.getByText(/MONITOR ups02@192\.0\.2\.40 1 <user> <password> secondary/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Shut down pve-03 now/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "ups03" })).not.toBeInTheDocument();
  });

  // As the Power page's outage banner reads it: a second supply on mains keeps the node up.
  it("offers no shutdown while another UPS on mains also powers the node", async () => {
    serve([outage([unprotected]), upsDevice({ id: "u3", name: "ups03", feeds: [upsFeed({ id: "f3", node_name: "pve-03" })] })]);

    renderWithProviders(<NodePowerCard clusterId="c1" nodeName="pve-03" online />);

    expect(await screen.findByText(/ups03 also powers it and is on mains/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Shut down/ })).not.toBeInTheDocument();
  });
});
