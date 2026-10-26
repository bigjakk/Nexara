import { describe, it, expect } from "vitest";
import type { GuestHistoryEntry } from "../api/guest-history-queries";
import {
  historyActor,
  historyDisplayStatus,
  historyEventLabel,
  historySummary,
} from "./guest-history";

const SYSTEM = "00000000-0000-0000-0000-000000000001";

function entry(over: Partial<GuestHistoryEntry>): GuestHistoryEntry {
  return {
    id: "audit:1",
    kind: "audit",
    occurred_at: "2026-01-01T00:00:00Z",
    category: "other",
    source: "nexara",
    action: "",
    resource_type: "vm",
    vmid: 100,
    user_id: "u1",
    user_email: "ops@example.com",
    user_display_name: "Ops",
    details: "{}",
    upid: "",
    node: "pve-01",
    description: "",
    task_type: "",
    outcome: "",
    exit_status: "",
    finished_at: null,
    ...over,
  };
}

const upid = (type: string) =>
  `UPID:pve-01:00001A2B:00003344:5F2E1A00:${type}:100:root@pam:`;

describe("historyEventLabel", () => {
  it.each([
    // Nexara's own action names say more than the task behind them.
    [
      { action: "drs_migrate_running", upid: upid("hamigrate") },
      "DRS migration",
    ],
    [{ action: "snapshot_create", upid: upid("qmsnapshot") }, "Take snapshot"],
    // A Proxmox row is named by the task Proxmox ran.
    [
      {
        source: "proxmox" as const,
        action: "hamigrate",
        upid: upid("hamigrate"),
      },
      "Migrate (HA)",
    ],
    [
      {
        kind: "task" as const,
        source: "proxmox" as const,
        action: "qmshutdown",
        upid: upid("qmshutdown"),
      },
      "Shutdown",
    ],
    [
      {
        source: "proxmox" as const,
        action: "qmfuture",
        upid: upid("qmfuture"),
      },
      "Qmfuture",
    ],
    // A clone INTO this guest names its source.
    [
      { category: "clone" as const, action: "clone", vmid: 50 },
      "Cloned from VM 50",
    ],
    [
      { resource_type: "ha_resource", action: "created" },
      "HA resource created",
    ],
    [
      { kind: "status" as const, from_status: "running", to_status: "stopped" },
      "Stopped, no task",
    ],
    [
      { kind: "status" as const, from_status: "running", to_status: "unknown" },
      "Status unknown",
    ],
    [
      { kind: "status" as const, from_status: "unknown", to_status: "running" },
      "Reported running again",
    ],
  ])("labels %o as %s", (over, want) => {
    expect(historyEventLabel(entry(over), 100)).toBe(want);
  });
});

describe("historySummary", () => {
  it.each([
    [
      {
        details: JSON.stringify({
          source_node: "pve-01",
          target_node: "pve-02",
          reason: "CPU",
        }),
      },
      "pve-01 → pve-02 · CPU",
    ],
    [{ details: JSON.stringify({ target: "pve-03" }) }, "→ pve-03"],
    [
      { details: JSON.stringify({ snap_name: "before-upgrade" }) },
      "before-upgrade",
    ],
    [
      {
        details: JSON.stringify({
          fields: { cores: "4", memory: "8192", delete: "balloon,shares" },
        }),
      },
      "cores, memory · removed balloon, shares",
    ],
    [
      { details: JSON.stringify({ disk: "scsi0", target_storage: "store02" }) },
      "scsi0 → store02",
    ],
    // A clone from this guest says where it went; the clone it was made
    // from names this guest, which the label already says.
    [{ details: JSON.stringify({ new_id: 105 }) }, "→ VM 105"],
    [{ vmid: 50, details: JSON.stringify({ new_id: 100 }) }, ""],
    [
      {
        kind: "task" as const,
        description: "DRS Migrate qemu 100: pve-01 → pve-02",
      },
      "DRS Migrate qemu 100: pve-01 → pve-02",
    ],
  ])("summarises %o", (over, want) => {
    expect(historySummary(entry(over), 100)).toBe(want);
  });

  // Only a stop has an everyday cause no task records.
  it.each([
    [
      "running",
      "stopped",
      "running → stopped on pve-01. No task explains it: a shutdown or crash inside the guest, or the host.",
    ],
    ["stopped", "running", "stopped → running on pve-01. No task explains it."],
    [
      "running",
      "unknown",
      "Was running on pve-01; the node stopped reporting the guest.",
    ],
  ])("says what a %s → %s change no task explains means", (from, to, want) => {
    expect(
      historySummary(
        entry({ kind: "status", from_status: from, to_status: to }),
        100,
      ),
    ).toBe(want);
  });
});

describe("historyActor", () => {
  it.each([
    [
      {
        source: "proxmox" as const,
        user_id: SYSTEM,
        details: JSON.stringify({ proxmox_user: "root@pam" }),
      },
      "root@pam",
    ],
    [
      { kind: "task" as const, source: "proxmox" as const, user_id: SYSTEM },
      "Proxmox",
    ],
    [{ user_id: SYSTEM }, "System"],
    [{}, "Ops"],
    [{ kind: "status" as const }, ""],
  ])("credits %o to %s", (over, want) => {
    expect(historyActor(entry(over))).toBe(want);
  });
});

describe("historyDisplayStatus", () => {
  it.each([
    ["ok", undefined, "ok"],
    ["failed", undefined, "failed"],
    ["", undefined, null],
    ["running", undefined, "running"],
    // A poll finishes a row the server still calls running…
    ["running", { status: "stopped", exit_status: "WARNINGS: 1" }, "ok"],
    ["running", { status: "stopped", exit_status: "unable to lock" }, "failed"],
    // …and never overrides the server's own verdict.
    ["ok", { status: "running", exit_status: "" }, "ok"],
  ] as const)("outcome %s with poll %o shows %s", (outcome, live, want) => {
    expect(historyDisplayStatus(entry({ outcome }), live)).toBe(want);
  });
});
