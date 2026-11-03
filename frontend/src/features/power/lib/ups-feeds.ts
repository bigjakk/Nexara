import type { UpsDevice, UpsFeed } from "../types/ups";

export type ProtectionTone = "ok" | "warn" | "muted";

export interface ProtectionView {
  label: string;
  tone: ProtectionTone;
  /** One line on what the verdict rests on. */
  hint: string;
}

/** How a node feed's verdict reads. Only "Not protected" is a warning. */
export function protectionView(f: UpsFeed): ProtectionView {
  switch (f.protection) {
    case "protected":
      return {
        label: "Protected",
        tone: "ok",
        hint: f.login_address === "" ? "A upsmon on it is logged in" : `upsmon logged in from ${f.login_address}`,
      };
    case "not_protected":
      return {
        label: "Not protected",
        tone: "warn",
        hint: "No upsmon on it is logged in to this UPS, so it would lose power without shutting down",
      };
    case "checked_elsewhere":
      return { label: "Checked elsewhere", tone: "muted", hint: "Marked as shutting down some way Nexara cannot see" };
    case "node_offline":
      return { label: "Node offline", tone: "muted", hint: "The node is down, so there is nothing to judge" };
    default:
      return {
        label: "Not checked yet",
        tone: "muted",
        hint: "Nexara has not listed this UPS's logins on mains yet, or cannot tell this node's logins apart",
      };
  }
}

/** A feed's name: a node with its cluster, as the health pill names it, or the label. */
export function feedName(f: UpsFeed): string {
  if (f.kind === "label") return f.label;
  return f.cluster_name === "" ? f.node_name : `${f.node_name} (${f.cluster_name})`;
}

/**
 * Across the UPSes: how many node feeds have a verdict, how many of them are
 * protected, and the names of those that are not. A node two UPSes power
 * counts once per UPS, since each must find its upsmon. A UPS whose server is
 * not polled is left out, as the health pill leaves it out.
 */
export function protectionSummary(devices: UpsDevice[]): { judged: number; protected: number; unprotected: string[] } {
  let judged = 0;
  let protectedCount = 0;
  const unprotected = new Set<string>();
  for (const d of devices) {
    if (!d.server_enabled) continue;
    for (const f of d.feeds) {
      if (f.protection === "protected") {
        judged++;
        protectedCount++;
      } else if (f.protection === "not_protected") {
        judged++;
        unprotected.add(feedName(f));
      }
    }
  }
  return { judged, protected: protectedCount, unprotected: [...unprotected] };
}
