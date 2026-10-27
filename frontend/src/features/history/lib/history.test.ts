/// <reference types="vite/client" />
import { describe, it, expect } from "vitest";
import i18n, { resources } from "@/lib/i18n";
import { formatTimestampLong, formatTimestampShort } from "@/lib/format";
import { makeHistoryEntry as entry } from "@/test/history.fixtures";
import type { HistoryEntry } from "../types/history";
import {
  historyActor,
  historyDisplayStatus,
  historyEventLabel,
  historyKind,
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
