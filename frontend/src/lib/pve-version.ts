/**
 * isPVEAtLeast returns true if `current` is >= `min` per a lax major.minor[.patch]
 * compare. Empty or unparseable versions return false. Used to feature-gate UI on
 * Proxmox VE capabilities (see PVE_FEATURES).
 */
export function isPVEAtLeast(current: string, min: string): boolean {
  const a = parseVersion(current);
  const b = parseVersion(min);
  if (!a || !b) return false;
  if (a[0] !== b[0]) return a[0] > b[0];
  if (a[1] !== b[1]) return a[1] > b[1];
  return a[2] >= b[2];
}

/**
 * isPVEVersionKnown reports whether `v` is a version isPVEAtLeast can compare.
 * A gate that has to tell "too old" from "not known yet" needs it: an empty or
 * unparseable version makes every isPVEAtLeast answer false, which reads as
 * the oldest release.
 */
export function isPVEVersionKnown(v: string): boolean {
  return parseVersion(v) !== null;
}

function parseVersion(v: string): [number, number, number] | null {
  if (!v) return null;
  const match = /^(\d+)(?:\.(\d+))?(?:\.(\d+))?/.exec(v.trim());
  if (!match) return null;
  return [Number(match[1] ?? 0), Number(match[2] ?? 0), Number(match[3] ?? 0)];
}

/**
 * PVE_FEATURES maps a capability to the minimum Proxmox VE release that
 * introduced it. Pair with isPVEAtLeast, e.g.:
 *   isPVEAtLeast(cluster.pve_version, PVE_FEATURES.CRS_DYNAMIC)
 */
export const PVE_FEATURES = {
  /** HA node/resource affinity rules (HA groups deprecated). */
  HA_RULES: "9.0",
  /** OCI registry image pull. */
  OCI_IMAGES: "9.1",
  /** CRS dynamic load balancer — native DRS. */
  CRS_DYNAMIC: "9.2",
  /** Cluster-wide Arm/Disarm HA maintenance. */
  HA_ARM_DISARM: "9.2",
} as const;

/**
 * nodePVEVersion reduces what Nexara stores for a node — the pve-manager
 * package string, "pve-manager/9.2.20/<build hash>" — to the version number
 * "9.2.20". Anything not in that form is returned trimmed, as it is: an empty
 * string stays unknown, and a bare "9.2.20" passes through.
 */
export function nodePVEVersion(pveVersion: string): string {
  const trimmed = pveVersion.trim();
  const match = /^pve-manager\/([^/]+)/.exec(trimmed);
  return match?.[1] ?? trimmed;
}

/**
 * isNodePVEAtLeast is isPVEAtLeast for a NODE's own version string, as the
 * node list carries it (see nodePVEVersion). Pair it with PVE_NODE_FEATURES.
 */
export function isNodePVEAtLeast(nodePveVersion: string, min: string): boolean {
  return isPVEAtLeast(nodePVEVersion(nodePveVersion), min);
}

/**
 * PVE_NODE_FEATURES maps a per-node capability to the first pve-manager
 * release that has it, at PATCH precision. It is compared against a node's own
 * version (isNodePVEAtLeast), not the way PVE_FEATURES is, against the
 * cluster's `release`: that is "9.2", with no patch (the collector stores
 * /version's `release` as the cluster's pve_version, internal/collector
 * sync.go), which is too coarse for a key that arrived in 9.1.13.
 *
 * Each is the first release after the commit that added the key to
 * PVE/NodeConfig.pm in pve-manager:
 */
export const PVE_NODE_FEATURES = {
  /** "node: options: add config option for ballooning target": 8.3.6. */
  BALLOONING_TARGET: "8.3.6",
  /**
   * Wake-on-LAN's bind-interface ("fix #5255: node: wol: add optional bind
   * interface") and broadcast-address ("… configurable broadcast address"),
   * both: 8.1.9.
   */
  WOL_BIND_BROADCAST: "8.1.9",
  /** "node config: add location property": 9.1.13, five days before 9.2.0. */
  LOCATION: "9.1.13",
} as const;
