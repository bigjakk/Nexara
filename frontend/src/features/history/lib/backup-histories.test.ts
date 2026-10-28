import { describe, expect, it } from "vitest";
import i18n from "@/lib/i18n";
import { makeHistoryEntry as entry } from "@/test/history.fixtures";
import { backupHistories, backupHistoryKinds } from "./backup-histories";

// Where each History is read, which the server's routes are (TestBackupHistory
// PathsAreTheSPAs, in Go, holds the two together), and the one page an id that
// the server decodes arrives on.
describe("backupHistories", () => {
  it.each([
    [
      backupHistories.storage("c1", "store01"),
      "c1",
      "storage",
      "/api/v1/clusters/c1/storages/store01/history",
    ],
    [
      backupHistories.storage("c1", "store01", "pve-01"),
      "c1",
      "storage",
      "/api/v1/clusters/c1/storages/store01/history?node=pve-01",
    ],
    [
      backupHistories.pbsServer("p1"),
      null,
      "pbs",
      "/api/v1/pbs-servers/p1/history",
    ],
    [
      backupHistories.pbsServer("p1", "datastore01"),
      null,
      "pbs",
      "/api/v1/pbs-servers/p1/history?datastore=datastore01",
    ],
    [
      backupHistories.veeamServer("s1"),
      null,
      "veeam",
      "/api/v1/veeam-servers/s1/history",
    ],
    [
      backupHistories.veeamJob("s1", "j1"),
      null,
      "veeam",
      "/api/v1/veeam-servers/s1/jobs/j1/history",
    ],
    [
      backupHistories.veeamSession("s1", "r1"),
      null,
      "veeam",
      "/api/v1/veeam-servers/s1/sessions/r1/history",
    ],
    [
      backupHistories.backupJob("c1", "backup-1a2b3c4d"),
      "c1",
      "backup",
      "/api/v1/clusters/c1/backup-jobs/backup-1a2b3c4d/history",
    ],
  ])("reads %#", (history, clusterId, permission, path) => {
    expect(history.target.clusterId).toBe(clusterId);
    // The view permission of the object's own page, which the route is gated on.
    expect(history.permission).toBe(permission);
    expect(String(history.target.path(new URLSearchParams()))).toBe(path);
  });
});

// A row says what it is about from its details, in its kind's words.
describe("a kind's Summary", () => {
  const t = (ns: string) => i18n.getFixedT("en", ns);
  it.each([
    [
      "storage",
      "history-storage",
      entry({
        resource_type: "vm",
        details: JSON.stringify({
          disk: "scsi0",
          from_storage: "store01",
          storage: "store02",
          vmid: 100,
        }),
      }),
      "scsi0: store01 → store02 · VM/CT 100",
    ],
    [
      "storage",
      "history-storage",
      entry({
        resource_type: "storage",
        details: JSON.stringify({ filename: "disk.iso" }),
      }),
      "disk.iso",
    ],
    [
      "pbs",
      "history-pbs",
      entry({
        resource_type: "backup",
        details: JSON.stringify({
          datastore: "datastore01",
          backup_type: "vm",
          backup_id: "100",
          vmid: 105,
        }),
      }),
      "datastore01 · vm/100 · → VM/CT 105",
    ],
    [
      "veeamServer",
      "history-veeam-server",
      entry({
        resource_type: "veeam_server",
        details: JSON.stringify({
          platform: "platform01",
          cluster_name: "cluster01",
        }),
      }),
      "platform01 → cluster01",
    ],
    [
      "veeamServer",
      "history-veeam-server",
      entry({
        resource_type: "veeam_server",
        details: JSON.stringify({ object: "linux01", guest_name: "linux02" }),
      }),
      "linux01 → linux02",
    ],
  ] as const)("of the %s kind (%#)", (kind, ns, row, want) => {
    expect(backupHistoryKinds[kind].summary?.(row, t(ns))).toBe(want);
  });
});
