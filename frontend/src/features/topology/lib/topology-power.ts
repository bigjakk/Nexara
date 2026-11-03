import type { Edge, Node } from "@xyflow/react";
import { upsOnBattery, upsStatus } from "@/features/power/lib/ups-state";
import type { UpsTone } from "@/features/power/lib/ups-state";
import type { UpsDevice } from "@/features/power/types/ups";
import { CLUSTER_WIDTH, HORIZONTAL_GAP, HOST_HEIGHT, HOST_WIDTH } from "./topology-layout";
import type { HostNodeData, UpsNodeData } from "./topology-transform";

// UpsNode is a fixed 220 px wide; ClusterNode is 220 px (CLUSTER_WIDTH) by two fixed lines.
const UPS_WIDTH = 220;
const UPS_HEIGHT = 64;
const UPS_GAP = 40;
const CLUSTER_CARD_HEIGHT = 64;
const CLEARANCE = 12;

export const TONE_COLOR: Record<UpsTone, string> = {
  ok: "#10b981",
  warn: "#f59e0b",
  err: "#ef4444",
  idle: "#6b7280",
};

export interface PowerOverlay {
  /** The UPSes that power a host on the canvas, with those hosts' flow ids. */
  ups: { id: string; hosts: string[]; data: UpsNodeData }[];
  edges: Edge[];
  /** A host's flow id to the UPSes that power it with no upsmon on it. */
  noUpsmon: Map<string, string[]>;
}

/**
 * The power overlay for the hosts on the canvas: a node for each UPS that
 * powers one, an edge from it to each (solid on mains, dashed on battery), and
 * the hosts no upsmon would shut down. hostIdByName maps
 * "<clusterId>:<node name>" to a host's flow id.
 */
export function buildPowerOverlay(
  devices: UpsDevice[],
  hostIdByName: Map<string, string>,
  onCanvas: Set<string>,
  direction: "TB" | "LR",
): PowerOverlay {
  const overlay: PowerOverlay = { ups: [], edges: [], noUpsmon: new Map() };
  for (const d of devices) {
    const id = `ups-${d.id}`;
    const status = upsStatus(d);
    const hosts: string[] = [];
    for (const f of d.feeds) {
      if (f.kind !== "node" || f.cluster_id === null) continue;
      const host = hostIdByName.get(`${f.cluster_id}:${f.node_name}`);
      if (host === undefined || !onCanvas.has(host)) continue;
      hosts.push(host);
      overlay.edges.push({
        id: `power-${d.id}-${host}`,
        source: id,
        target: host,
        targetHandle: "power",
        // A curve, unlike the tree's right-angled edges, and one that needs
        // no room for corners in the short drop to the host.
        type: "default",
        style: {
          stroke: TONE_COLOR[status.tone],
          strokeWidth: 1.5,
          strokeDasharray: upsOnBattery(d) ? "6 4" : undefined,
        },
      });
      // As the health pill reads it: a UPS whose server is not polled is left out.
      if (f.protection === "not_protected" && d.server_enabled) {
        overlay.noUpsmon.set(host, [...(overlay.noUpsmon.get(host) ?? []), d.name]);
      }
    }
    if (hosts.length === 0) continue;
    overlay.ups.push({
      id,
      hosts,
      data: {
        kind: "ups",
        label: d.name,
        deviceId: d.id,
        serverName: d.server_name,
        status: status.label,
        tone: status.tone,
        batteryCharge: d.battery_charge,
        batteryRuntime: d.battery_runtime,
        direction,
      },
    });
  }
  return overlay;
}

/**
 * The canvas nodes with the overlay applied, or with it removed when null. A
 * UPS node already there keeps its position, which may have been dragged, and
 * takes the fresh data. A new one goes in a lane (just above the host row
 * top-down, left of the first column left-right), centred on the hosts it
 * powers and pushed along the lane past the one before. Each host's noUpsmon
 * is set or cleared. Nodes the overlay leaves alone keep their identity.
 */
export function withPower(current: Node[], overlay: PowerOverlay | null, direction: "TB" | "LR"): Node[] {
  const before = new Map(current.filter((n) => n.type === "upsNode").map((n) => [n.id, n]));
  const out: Node[] = [];
  for (const n of current) {
    if (n.type === "upsNode") continue;
    if (n.type !== "hostNode") {
      out.push(n);
      continue;
    }
    const data = n.data as HostNodeData;
    const want = overlay?.noUpsmon.get(n.id);
    out.push(sameNames(data.noUpsmon, want) ? n : { ...n, data: { ...data, noUpsmon: want } });
  }
  if (overlay === null) return out;

  const lane = placeInLane(overlay.ups.filter((u) => !before.has(u.id)), out, direction);
  for (const u of overlay.ups) {
    const prev = before.get(u.id);
    if (prev === undefined) {
      out.push({ id: u.id, type: "upsNode", position: lane.get(u.id) ?? { x: 0, y: 0 }, data: u.data });
    } else {
      out.push(sameData(prev.data, u.data) ? prev : { ...prev, data: u.data });
    }
  }
  return out;
}

function placeInLane(
  ups: { id: string; hosts: string[] }[],
  nodes: Node[],
  direction: "TB" | "LR",
): Map<string, { x: number; y: number }> {
  const out = new Map<string, { x: number; y: number }>();
  if (ups.length === 0 || nodes.length === 0) return out;
  const tb = direction === "TB";
  const pos = new Map(nodes.map((n) => [n.id, n.position]));
  const clusters = nodes.filter((n) => n.type === "clusterNode");
  // Top-down, just under the cluster cards: the layout gives a cluster a
  // host's height, which its card does not fill, and the tree's edges turn
  // towards the hosts halfway down, below this lane. So no tree edge runs
  // behind a UPS, once each UPS also steps clear of the line a cluster drops
  // from its foot. Left-right the depth step leaves no such room, so the lane
  // goes before the cluster column. (Without a cluster, which the graph never
  // has, just above the hosts.)
  const hostTop = Math.min(...ups.flatMap((u) => u.hosts.map((h) => pos.get(h)?.y ?? 0)));
  const lane = !tb
    ? Math.min(...nodes.map((n) => n.position.x)) - UPS_WIDTH - HORIZONTAL_GAP * 2
    : clusters.length > 0
      ? Math.min(...clusters.map((c) => c.position.y)) + CLUSTER_CARD_HEIGHT + CLEARANCE
      : hostTop - UPS_HEIGHT - CLEARANCE;
  const drops = tb ? clusters.map((c) => c.position.x + CLUSTER_WIDTH / 2).sort((a, b) => a - b) : [];
  const size = tb ? UPS_WIDTH : UPS_HEIGHT;
  const wanted = ups
    .map((u) => {
      const centres = u.hosts.map((h) => {
        const p = pos.get(h) ?? { x: 0, y: 0 };
        return tb ? p.x + HOST_WIDTH / 2 : p.y + HOST_HEIGHT / 2;
      });
      return { id: u.id, at: centres.reduce((a, b) => a + b, 0) / centres.length - size / 2 };
    })
    .sort((a, b) => a.at - b.at);
  let next = -Infinity;
  for (const w of wanted) {
    let at = Math.max(w.at, next);
    for (const x of drops) {
      if (at - CLEARANCE < x && x < at + size + CLEARANCE) at = x + CLEARANCE;
    }
    next = at + size + UPS_GAP;
    out.set(w.id, tb ? { x: at, y: lane } : { x: lane, y: at });
  }
  return out;
}

function sameNames(a: string[] | undefined, b: string[] | undefined): boolean {
  if (a === undefined || b === undefined) return a === b;
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

function sameData(a: Record<string, unknown>, b: UpsNodeData): boolean {
  const keys = Object.keys(b);
  return keys.length === Object.keys(a).length && keys.every((k) => a[k] === b[k]);
}
