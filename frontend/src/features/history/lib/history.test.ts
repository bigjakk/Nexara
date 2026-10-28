/// <reference types="vite/client" />
import { describe, it, expect } from "vitest";
import i18n, { resources } from "@/lib/i18n";
import { formatTimestampLong, formatTimestampShort } from "@/lib/format";
import { makeHistoryEntry as entry } from "@/test/history.fixtures";
import type { HistoryEntry } from "../types/history";
import {
  historyActor,
  historyDetailParts,
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
    [
      {
        action: "changed",
        fields: ["rules"],
        details: '{"counts":{"rules":{"added":2,"removed":1,"moved":1}}}',
      },
      "Changed in Proxmox: 2 rules added, 1 removed, 1 moved",
    ],
    [
      { action: "removed", details: '{"children":3}' },
      "Removed in Proxmox, with its 3 children",
    ],
    [
      { action: "added", details: '{"while_over_cap":true}' },
      "Created in Proxmox while its kind was past the cap",
    ],
    [
      {
        fields: ["network"],
        details:
          '{"notice":"changed_past_cap","while_over_cap":true,"added":3,"added_ids":["a","b","c"],"removed":1,"removed_ids":["d"],"changed":12,"node":"pve-01"}',
      },
      "Network on pve-01 past the cap: 3 added, 1 removed, 12 changed",
    ],
    [
      {
        fields: ["token_privileges"],
        details: '{"notice":"token_privileges","during_upgrade":true}',
      },
      "The cluster token's privileges on / changed during an upgrade: objects Proxmox lists by privilege may come and go",
    ],
    [
      {
        fields: ["firewall_ipset_entry"],
        details:
          '{"notice":"too_many_objects","objects":12000,"cap":10000,"parent":"blocklist"}',
      },
      "Firewall Ipset Entry of blocklist: 12,000 objects, more than the 10,000 compared field by field; until it is back under, what changes is counted",
    ],
    [
      {
        fields: ["network"],
        details: '{"notice":"back_under_cap","node":"pve-01"}',
      },
      "Network on pve-01: back under the cap and compared field by field again",
    ],
    [
      {
        fields: ["nodes"],
        details: '{"notice":"node_never_read","node":"pve-03"}',
      },
      "Node pve-03 has never been read: what it holds is not compared",
    ],
    [
      {
        fields: ["guests"],
        details:
          '{"notice":"guests_read_in_turns","guests":3200,"per_pass":3000}',
      },
      "3,200 guests, more than one pass reads: read in turns, 3,000 a pass",
    ],
  ])("%o reads %s", (over, want) => {
    expect(historyEventLabel(kind, observed(over), t)).toBe(want);
  });

  // A notice is a point event, when it was filed; a change, between two
  // readings, with what its detail marks beside its fields.
  it.each<[Partial<HistoryEntry>, string]>([
    [
      {
        last_seen_at: "2026-03-02T10:10:00Z",
        details: '{"notice":"token_privileges"}',
      },
      "",
    ],
    [
      {
        last_seen_at: "2026-03-02T10:05:00Z",
        details: '{"fields_appeared":["cores"],"bulk":"b1","bulk_size":41}',
      },
      `Between ${formatTimestampShort(Date.parse("2026-03-02T10:05:00Z"))} and ${formatTimestampShort(Date.parse("2026-03-02T10:10:00Z"))} · Filled in by Proxmox: cores · Changed together with 40 others`,
    ],
  ])("%o sums up as %s", (over, want) => {
    expect(
      historySummary(
        kind,
        observed({ occurred_at: "2026-03-02T10:10:00Z", ...over }),
        t,
      ),
    ).toBe(want);
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

// An update sums up as the fields it names, a list or keyed by name, and
// says when Nexara could not check them.
describe("an update's summary", () => {
  const kind = historyKind(
    "probe",
    { categories: { other: "Other" } },
    { icons: {} },
  );
  const t = i18n.getFixedT("en", "history-probe");
  it.each<[Partial<HistoryEntry>, string]>([
    [{ details: '{"fields":["comment","nodes"]}' }, "comment, nodes"],
    [{ details: '{"fields":{"memory":"4096"}}' }, "memory"],
    [
      { details: '{"fields":["comment"],"fields_unverified":true}' },
      "comment · Fields not checked against Proxmox",
    ],
  ])("%o sums up as %s", (over, want) => {
    expect(historySummary(kind, entry(over), t)).toBe(want);
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

// A list too long to read inline — the ids a kind read past its cap names —
// is set apart from the details' pairs, to be shown collapsed.
describe("historyDetailParts", () => {
  it("sets apart only the long lists of names", () => {
    const ids = Array.from({ length: 12 }, (_, i) => `vm-${String(i)}`);
    const parts = historyDetailParts(
      JSON.stringify({ while_over_cap: true, added_ids: ids, fields: ["a"] }),
    );
    expect(parts.lists).toEqual([{ key: "added_ids", items: ids }]);
    expect(parts.pairs?.map(([k]) => k)).toEqual(["While Over Cap", "Fields"]);
  });
});
