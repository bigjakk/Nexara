import { describe, expect, it } from "vitest";
import type { BackupCoverageEntry, VeeamServer } from "../types/backup";
import {
  coverageStats,
  coverageSummaryLine,
  pbsSummaryLine,
  veeamSummaryLine,
} from "./backup-summary";

const GIB = 1024 ** 3;
const store = { store: "datastore01", total: 500 * GIB, used: 25 * GIB };
const datastores = [{ ...store, avail: 475 * GIB }];

describe("pbsSummaryLine", () => {
  it.each([
    ["no server", { serverCount: 0 }, null],
    [
      "one server, everything read",
      { serverCount: 1, datastores, snapshotCount: 120 },
      "1 Proxmox Backup Server, 120 snapshots, 5.0% of 500.0 GiB used",
    ],
    [
      "several servers: the figures are named as one server's",
      { serverCount: 2, datastores, snapshotCount: 1 },
      "2 Proxmox Backup Servers. pbs01: 1 snapshot, 5.0% of 500.0 GiB used",
    ],
    [
      "nothing read from the server yet",
      { serverCount: 1 },
      "1 Proxmox Backup Server",
    ],
    [
      "datastores of no size are not a percentage",
      { serverCount: 1, datastores: [], snapshotCount: 0 },
      "1 Proxmox Backup Server, 0 snapshots",
    ],
  ])("%s", (_, input, expected) => {
    expect(
      pbsSummaryLine({
        activeName: "pbs01",
        datastores: undefined,
        snapshotCount: undefined,
        ...input,
      }),
    ).toBe(expected);
  });
});

function veeam(over: Partial<VeeamServer>): VeeamServer {
  return {
    id: "s",
    name: "vbr01",
    base_url: "https://192.0.2.10:9419",
    username: "",
    api_revision: "",
    product_version: "",
    license_edition: "",
    tls_fingerprint: "",
    verify_tls: true,
    enabled: true,
    last_sync_at: "2026-08-26T19:56:00Z",
    last_sync_error: "",
    created_at: "2026-08-26T19:56:00Z",
    updated_at: "2026-08-26T19:56:00Z",
    ...over,
  };
}

describe("veeamSummaryLine", () => {
  it.each([
    ["no server", [], null],
    ["one healthy server", [veeam({})], "1 Veeam server, connected"],
    [
      "each state that is not connected is counted",
      [
        veeam({}),
        veeam({ last_sync_error: "refused" }),
        veeam({ last_sync_at: null }),
        veeam({ enabled: false }),
      ],
      "4 Veeam servers, 1 failing to sync, 1 waiting for a first sync, 1 disabled",
    ],
  ])("%s", (_, servers, expected) => {
    expect(veeamSummaryLine(servers)).toBe(expected);
  });
});

function guests(
  ...statuses: BackupCoverageEntry["coverage_status"][]
): BackupCoverageEntry[] {
  return statuses.map(
    (coverage_status, vmid) =>
      ({ vmid, coverage_status }) as BackupCoverageEntry,
  );
}

describe("coverageSummaryLine", () => {
  it.each([
    ["no guests", guests(), "No guests to back up yet"],
    [
      "all fresh: Veeam's own guests are not targets",
      guests("recent", "recent", "not_eligible"),
      "2 backup targets, all backed up in the last 24 hours",
    ],
    [
      "problems are counted",
      guests("recent", "stale", "none", "none"),
      "4 backup targets: 1 backed up in the last 24 hours, 1 stale, 2 with no backup",
    ],
  ])("%s", (_, entries, expected) => {
    expect(coverageSummaryLine(coverageStats(entries))).toBe(expected);
  });
});
