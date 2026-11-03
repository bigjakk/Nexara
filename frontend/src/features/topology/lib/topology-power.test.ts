import { describe, expect, it } from "vitest";
import type { Node } from "@xyflow/react";
import { upsDevice, upsFeed } from "@/features/power/power.fixtures";
import type { UpsDevice } from "@/features/power/types/ups";
import { buildPowerOverlay, withPower } from "./topology-power";

// cluster01's pve-01 and pve-02 are on the canvas; c2's pve-01 is filtered out.
const hostIdByName = new Map([
  ["c1:pve-01", "host-n1"],
  ["c1:pve-02", "host-n2"],
  ["c2:pve-01", "host-n3"],
]);
const onCanvas = new Set(["host-n1", "host-n2"]);
const canvas: Node[] = [
  { id: "cluster-c1", type: "clusterNode", position: { x: 120, y: 0 }, data: { kind: "cluster" } },
  { id: "host-n1", type: "hostNode", position: { x: 0, y: 230 }, data: { kind: "host" } },
  { id: "host-n2", type: "hostNode", position: { x: 260, y: 230 }, data: { kind: "host" } },
];
const overlayOf = (devices: UpsDevice[], direction: "TB" | "LR" = "TB") =>
  buildPowerOverlay(devices, hostIdByName, onCanvas, direction);

describe("buildPowerOverlay", () => {
  it.each([
    ["on mains", upsDevice({ feeds: [upsFeed()] }), undefined],
    ["on battery", upsDevice({ state: "on_battery", flags: ["OB"], feeds: [upsFeed()] }), "6 4"],
  ])("draws the edge to a host %s", (_, device, dash) => {
    const { edges } = overlayOf([device]);
    expect(edges).toHaveLength(1);
    expect(edges[0]).toMatchObject({ source: "ups-u1", target: "host-n1" });
    expect(edges[0]?.style?.strokeDasharray).toBe(dash);
  });

  it("marks the hosts no upsmon would shut down and leaves out what the canvas does not show", () => {
    const overlay = overlayOf([
      upsDevice({
        id: "u1",
        name: "ups01",
        feeds: [
          upsFeed({ id: "f1", protection: "not_protected" }),
          upsFeed({ id: "f2", node_name: "pve-02" }),
          upsFeed({ id: "f3", cluster_id: "c2" }),
          upsFeed({ id: "f4", kind: "label", node_name: "", label: "Switch" }),
        ],
      }),
      // Its server is not polled, so its verdict is not shown, as on the pill.
      upsDevice({ id: "u2", name: "ups02", server_enabled: false, feeds: [upsFeed({ id: "f5", node_name: "pve-02", protection: "not_protected" })] }),
      // It powers only a host the canvas does not show.
      upsDevice({ id: "u3", name: "ups03", feeds: [upsFeed({ id: "f6", cluster_id: "c2" })] }),
    ]);
    expect(overlay.ups.map((u) => [u.id, u.hosts])).toEqual([
      ["ups-u1", ["host-n1", "host-n2"]],
      ["ups-u2", ["host-n2"]],
    ]);
    expect([...overlay.noUpsmon]).toEqual([["host-n1", ["ups01"]]]);
  });
});

describe("withPower", () => {
  const two = [
    upsDevice({ id: "u1", name: "ups01", feeds: [upsFeed({ id: "f1" }), upsFeed({ id: "f2", node_name: "pve-02" })] }),
    upsDevice({ id: "u2", name: "ups02", feeds: [upsFeed({ id: "f3", node_name: "pve-02" })] }),
  ];

  // Under the cluster cards top-down, before the cluster column left-right;
  // centred on the hosts each powers, pushed apart, and top-down stepped
  // right of the line cluster-c1 drops to its hosts (x 230).
  it.each([
    ["TB", { x: 242, y: 76 }, { x: 502, y: 76 }],
    ["LR", { x: -340, y: 273 }, { x: -340, y: 377 }],
  ] as const)("places new UPS nodes in a lane %s", (direction, first, second) => {
    const nodes = withPower(canvas, overlayOf(two, direction), direction);
    expect(nodes.find((n) => n.id === "ups-u1")?.position).toEqual(first);
    expect(nodes.find((n) => n.id === "ups-u2")?.position).toEqual(second);
  });

  it("keeps a dragged UPS node where it is through a poll, and removes the overlay when it goes", () => {
    const device = (state: UpsDevice["state"]) => [upsDevice({ name: "ups01", state, feeds: [upsFeed({ protection: "not_protected" })] })];
    const dragged = withPower(canvas, overlayOf(device("online")), "TB").map((n) =>
      n.id === "ups-u1" ? { ...n, position: { x: 999, y: 999 } } : n,
    );

    const polled = withPower(dragged, overlayOf(device("on_battery")), "TB");
    expect(polled.find((n) => n.id === "ups-u1")).toMatchObject({ position: { x: 999, y: 999 }, data: { status: "On battery" } });
    expect(polled.find((n) => n.id === "host-n1")?.data).toMatchObject({ noUpsmon: ["ups01"] });
    expect(polled.find((n) => n.id === "host-n2")).toBe(dragged.find((n) => n.id === "host-n2"));

    const off = withPower(polled, null, "TB");
    expect(off.map((n) => n.id)).toEqual(["cluster-c1", "host-n1", "host-n2"]);
    expect(off.find((n) => n.id === "host-n1")?.data["noUpsmon"]).toBeUndefined();
  });
});
