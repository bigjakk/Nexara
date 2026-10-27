/// <reference types="vite/client" />
import { describe, it, expect } from "vitest";
import i18n, { resources } from "@/lib/i18n";
import { formatTimestampLong, formatTimestampShort } from "@/lib/format";
import { makeHistoryEntry as entry } from "@/test/history.fixtures";
import type { HistoryEntry } from "../types/history";
import {
  historyActor,
  historyDisplayStatus,
  historyExclusions,
  historyEventLabel,
  historyKind,
  historySourceLabel,
  historySummary,
} from "./history";

// A kind's strings — and the shared timeline's — reach it only once
// lib/i18n.ts registers them; unregistered, every label shows its key and
// nothing fails.
it("registers every History namespace", () => {
  const files = Object.keys(
    import.meta.glob([
      "/src/locales/en/history.json",
      "/src/locales/en/history-*.json",
    ]),
  );
  expect(files.length).toBeGreaterThan(1);
  for (const file of files) {
    const ns = file.slice(file.lastIndexOf("/") + 1, -".json".length);
    expect(Object.keys(resources.en)).toContain(ns);
  }
});

const SYSTEM = "00000000-0000-0000-0000-000000000001";

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

// What Nexara saw change in Proxmox reads the same in every kind: the shared
// namespace, whatever the kind's own.
describe("an observed change", () => {
  const kind = historyKind(
    "probe",
    { categories: { other: "Other" } },
    { icons: {} },
  );
  const t = i18n.getFixedT("en", "history-probe");
  const observed = (over: Parameters<typeof entry>[0]) =>
    entry({ kind: "observed", source: "detected", ...over });
  it.each<[Partial<HistoryEntry>, string]>([
    [{ action: "added" }, "Created in Proxmox"],
    [{ action: "removed" }, "Removed in Proxmox"],
    [
      { action: "changed", fields: ["cores", "memory"] },
      "Changed in Proxmox: cores, memory",
    ],
    [{ action: "changed", fields: [] }, "Changed in Proxmox"],
    [
      {
        action: "changed",
        fields: ["rules"],
        details: '{"added":2,"removed":1}',
      },
      "Changed in Proxmox: 2 rules added, 1 removed",
    ],
    [
      {
        action: "changed",
        fields: ["comment", "firewall_rules", "members"],
        details:
          '{"firewall_rules":{"added":1,"removed":0},"members":{"added":0,"removed":3}}',
      },
      "Changed in Proxmox: comment, 1 rule added, 0 removed, members: 0 added, 3 removed",
    ],
  ])("%o reads %s", (over, want) => {
    expect(historyEventLabel(kind, observed(over), t)).toBe(want);
  });

  it.each<[Partial<HistoryEntry>, string]>([
    [{ kind: "observed", source: "detected" }, "Detected in Proxmox"],
    [{ kind: "status", source: "detected" }, "No task"],
    [{ source: "proxmox" }, "Proxmox"],
  ])("comes from %o: %s", (over, want) => {
    expect(historySourceLabel(entry(over), t)).toBe(want);
  });

  it("is placed between the sighting before and the one that saw it", () => {
    const sameDay = observed({
      last_seen_at: "2026-03-02T10:05:00Z",
      occurred_at: "2026-03-02T10:10:00Z",
    });
    const apart = observed({
      last_seen_at: "2026-03-01T10:05:00Z",
      occurred_at: "2026-03-04T10:10:00Z",
    });
    const at = (iso: string, f: (ts: number) => string) => f(Date.parse(iso));
    expect(historySummary(kind, sameDay, t)).toBe(
      `Between ${at("2026-03-02T10:05:00Z", formatTimestampShort)} and ${at("2026-03-02T10:10:00Z", formatTimestampShort)}`,
    );
    expect(historySummary(kind, apart, t)).toBe(
      `Between ${at("2026-03-01T10:05:00Z", formatTimestampLong)} and ${at("2026-03-04T10:10:00Z", formatTimestampLong)}`,
    );
  });
});

// A run is its row's event and how many times, from its first to its last,
// with the last one's error.
describe("a run", () => {
  const kind = historyKind(
    "probe",
    { categories: { other: "Other" } },
    { icons: {} },
  );
  const t = i18n.getFixedT("en", "history-probe");
  const run = (count: number) =>
    entry({
      kind: "run",
      action: "sync_failed",
      run_count: count,
      last_seen_at: "2026-03-02T10:05:00Z",
      occurred_at: "2026-03-02T10:10:00Z",
      details: '{"error":"connection refused"}',
    });
  it.each([
    [1, "Sync Failed, once"],
    [412, "Sync Failed, 412 times"],
  ])("of %d reads %s", (count, want) => {
    expect(historyEventLabel(kind, run(count), t)).toBe(want);
  });
  it("is placed between its first row and its last", () => {
    const at = (iso: string) => formatTimestampShort(Date.parse(iso));
    expect(historySummary(kind, run(2), t)).toBe(
      `Between ${at("2026-03-02T10:05:00Z")} and ${at("2026-03-02T10:10:00Z")} · Error: connection refused`,
    );
  });
});

describe("historyExclusions", () => {
  const kind = { routine: ["updates", "hardware"] };
  it.each([
    [false, "", ["updates", "hardware"]],
    [true, "", []],
    // Filtering to a routine category shows it.
    [false, "updates", ["hardware"]],
  ] as const)(
    "show routine %s, category %o: leaves out %o",
    (show, category, want) => {
      expect(historyExclusions(kind, show, category)).toEqual(want);
    },
  );
});
