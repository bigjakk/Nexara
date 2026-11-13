/**
 * The sentences the cluster pages are headed by, built from what they have
 * loaded. Pure, so the wording rules are tested without rendering anything.
 */
import { nodePVEVersion } from "@/lib/pve-version";
import type { ClusterResponse, NodeResponse } from "@/types/api";

function count(n: number, one: string, many: string): string {
  return `${String(n)} ${n === 1 ? one : many}`;
}

/**
 * The note beside the Nodes title: how many there are, whether they run one
 * Proxmox VE version, and how many are not online. Undefined with no nodes.
 */
export function nodesNote(
  nodes: readonly Pick<NodeResponse, "status" | "pve_version">[],
): string | undefined {
  if (nodes.length === 0) return undefined;
  const versions = new Set(
    nodes.map((n) => nodePVEVersion(n.pve_version)).filter((v) => v !== ""),
  );
  const [only] = versions;
  let note = count(nodes.length, "node", "nodes");
  if (versions.size === 1 && only !== undefined) {
    note += nodes.length === 1 ? ` on ${only}` : `, all on ${only}`;
  } else if (versions.size > 1) {
    note += ` on ${String(versions.size)} versions`;
  }
  const offline = nodes.filter((n) => n.status !== "online").length;
  if (offline > 0) note += `, ${String(offline)} offline`;
  return note;
}

/** What the Clusters page knows of one cluster when it writes its summary. */
export interface ClusterTally {
  status: ClusterResponse["status"];
  /** Undefined while the cluster's nodes or guests have not been read. */
  nodes: number | undefined;
  guests: number | undefined;
}

const STATUS_ORDER: readonly Exclude<ClusterResponse["status"], "online">[] = [
  "offline",
  "degraded",
  "inactive",
  "unknown",
];

/**
 * The Clusters page's summary: "3 clusters, 9 nodes, 120 guests, all online".
 * A count is left out until every cluster has reported it, so the sentence
 * never totals a partial read. Undefined with no clusters.
 */
export function clustersSummary(
  tallies: readonly ClusterTally[],
): string | undefined {
  if (tallies.length === 0) return undefined;
  const parts = [count(tallies.length, "cluster", "clusters")];
  const total = (pick: (t: ClusterTally) => number | undefined) => {
    let sum = 0;
    for (const t of tallies) {
      const n = pick(t);
      if (n === undefined) return undefined;
      sum += n;
    }
    return sum;
  };
  const nodes = total((t) => t.nodes);
  if (nodes !== undefined) parts.push(count(nodes, "node", "nodes"));
  const guests = total((t) => t.guests);
  if (guests !== undefined) parts.push(count(guests, "guest", "guests"));
  const states = STATUS_ORDER.map((status) => ({
    status,
    n: tallies.filter((t) => t.status === status).length,
  })).filter((s) => s.n > 0);
  if (states.length === 0) {
    parts.push(tallies.length === 1 ? "online" : "all online");
  } else {
    for (const s of states) parts.push(`${String(s.n)} ${s.status}`);
  }
  return parts.join(", ");
}
