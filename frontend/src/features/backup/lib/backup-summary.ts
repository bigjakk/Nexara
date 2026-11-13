import { formatBytes, formatPercent } from "@/lib/format";
import type {
  BackupCoverageEntry,
  PBSDatastoreStatus,
  VeeamServer,
} from "../types/backup";
import { veeamServerStatus } from "../veeam/veeam-server-status";

function counted(n: number, one: string, many: string): string {
  return `${String(n)} ${n === 1 ? one : many}`;
}

export interface PbsSummaryInput {
  serverCount: number;
  /** The server the figures are read from: named when there are several. */
  activeName: string;
  /** Undefined until the active server's datastores have been read. */
  datastores: readonly PBSDatastoreStatus[] | undefined;
  /** Undefined until its snapshots have been read. */
  snapshotCount: number | undefined;
}

/**
 * The Backup page's sentence for the Proxmox Backup Server tab, from what is
 * loaded: "1 Proxmox Backup Server, 120 snapshots, 40.0% of 2.0 TiB used".
 * Null without a server, where the page says what it is for instead.
 */
export function pbsSummaryLine({
  serverCount,
  activeName,
  datastores,
  snapshotCount,
}: PbsSummaryInput): string | null {
  if (serverCount === 0) return null;
  const servers = counted(
    serverCount,
    "Proxmox Backup Server",
    "Proxmox Backup Servers",
  );
  const figures: string[] = [];
  if (snapshotCount !== undefined) {
    figures.push(counted(snapshotCount, "snapshot", "snapshots"));
  }
  const total = (datastores ?? []).reduce((sum, ds) => sum + ds.total, 0);
  if (total > 0) {
    const used = (datastores ?? []).reduce((sum, ds) => sum + ds.used, 0);
    figures.push(
      `${formatPercent((used / total) * 100)} of ${formatBytes(total)} used`,
    );
  }
  if (figures.length === 0) return servers;
  // The figures are one server's: with several, say whose.
  return serverCount === 1
    ? [servers, ...figures].join(", ")
    : `${servers}. ${activeName}: ${figures.join(", ")}`;
}

/** The sentence for the Veeam tab. Null without a server. */
export function veeamSummaryLine(
  servers: readonly VeeamServer[],
): string | null {
  if (servers.length === 0) return null;
  const states = servers.map(veeamServerStatus);
  const errors = states.filter((s) => s === "Error").length;
  const syncing = states.filter((s) => s === "Syncing").length;
  const disabled = states.filter((s) => s === "Disabled").length;
  const parts = [counted(servers.length, "Veeam server", "Veeam servers")];
  if (errors > 0) parts.push(`${String(errors)} failing to sync`);
  if (syncing > 0) parts.push(`${String(syncing)} waiting for a first sync`);
  if (disabled > 0) parts.push(`${String(disabled)} disabled`);
  if (parts.length === 1) {
    parts.push(servers.length === 1 ? "connected" : "all connected");
  }
  return parts.join(", ");
}

export interface CoverageStats {
  /** Backup targets: the rows that are not Veeam's own infrastructure. */
  total: number;
  recent: number;
  stale: number;
  none: number;
  notEligible: number;
}

export function coverageStats(
  entries: readonly BackupCoverageEntry[],
): CoverageStats {
  const tally = (status: BackupCoverageEntry["coverage_status"]) =>
    entries.filter((e) => e.coverage_status === status).length;
  const notEligible = tally("not_eligible");
  return {
    // Targets, not rows: counting Veeam's own appliances would make the
    // figures disagree with each other and overstate the estate.
    total: entries.length - notEligible,
    recent: tally("recent"),
    stale: tally("stale"),
    none: tally("none"),
    notEligible,
  };
}

/** The sentence for the Coverage tab. */
export function coverageSummaryLine(stats: CoverageStats): string {
  if (stats.total === 0) return "No guests to back up yet";
  const targets = counted(stats.total, "backup target", "backup targets");
  if (stats.stale === 0 && stats.none === 0) {
    return `${targets}, all backed up in the last 24 hours`;
  }
  const parts = [`${String(stats.recent)} backed up in the last 24 hours`];
  if (stats.stale > 0) parts.push(`${String(stats.stale)} stale`);
  if (stats.none > 0) parts.push(`${String(stats.none)} with no backup`);
  return `${targets}: ${parts.join(", ")}`;
}
