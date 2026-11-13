import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { makeCluster, makeNode, makeVM } from "@/test/cluster.fixtures";
import { FleetBoard } from "./FleetBoard";
import type { DashboardData } from "../api/dashboard-queries";

describe("FleetBoard", () => {
  it("draws a row per node and a linked cell per guest", () => {
    const data = {
      clusters: [
        {
          cluster: makeCluster({ id: "c1", name: "cluster01" }),
          storageTotalBytes: 0,
          storageUsedBytes: 0,
        },
      ],
      nodesByCluster: new Map([
        ["c1", [makeNode({ id: "n1", name: "pve-01" })]],
      ]),
      vmsByCluster: new Map([
        [
          "c1",
          [makeVM({ id: "v1", node_id: "n1", vmid: 100, name: "linux01" })],
        ],
      ]),
    } as unknown as DashboardData;

    renderWithProviders(<FleetBoard data={data} metrics={new Map()} />);

    expect(screen.getByRole("link", { name: "cluster01" })).toHaveAttribute(
      "href",
      "/clusters/c1",
    );
    expect(screen.getByRole("link", { name: /pve-01/ })).toHaveAttribute(
      "href",
      "/clusters/c1/nodes/n1",
    );
    expect(
      screen.getByRole("link", { name: "100 linux01, running" }),
    ).toHaveAttribute("href", "/inventory/vm/c1/v1");
  });
});
