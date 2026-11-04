import { describe, expect, it } from "vitest";
import i18n from "@/lib/i18n";
import { makeHistoryEntry } from "@/test/history.fixtures";
import type { HistoryEntry } from "@/features/history/types/history";
import { upsHistory, upsHistoryEventLabel, upsHistoryNote, upsHistorySummary } from "./ups-history";

const at = "2026-11-03T12:00:00Z";
const power = (action: string, details: object): HistoryEntry =>
  makeHistoryEntry({ kind: "power", source: "detected", action, occurred_at: at, details: JSON.stringify(details) });
const audit = (action: string, details: object): HistoryEntry =>
  makeHistoryEntry({ action, resource_type: "ups_device", details: JSON.stringify(details) });

// A power entry reads as the Power events tab reads the event, from its own end
// and readings; a command by what was run or set; a feed by what it names.
describe("a UPS's History", () => {
  const t = i18n.getFixedT("en", "history-ups-device");
  it.each([
    [
      "an outage",
      power("on_battery", { ended_at: "2026-11-03T12:05:00Z", end_observed: true, lowest_charge: 95.4, peak_load: 50 }),
      "Power outage",
      "5m 00s · lowest charge 95% · peak load 50%",
    ],
    ["an end Nexara did not see", power("unreachable", { ended_at: "2026-11-03T12:02:00Z", end_observed: false }), "Lost contact", "≤ 2m 00s"],
    ["a self-test", power("self_test", { ended_at: at, end_observed: true, result: "Done and passed" }), "Self-test: Done and passed", ""],
    ["a command", audit("ups_command_sent", { kind: "command", name: "beeper.mute", tier: "everyday" }), "Ran beeper.mute", ""],
    [
      "a refused cut",
      audit("ups_command_failed", { kind: "command", name: "load.off", tier: "cuts_power", error: "ACCESS-DENIED" }),
      "load.off failed",
      "Cuts power · Error: ACCESS-DENIED",
    ],
    ["a setting", audit("ups_command_sent", { kind: "set", name: "battery.charge.low" }), "Set battery.charge.low", ""],
    ["a feed", audit("ups_feed_updated", { node: "pve-01", protected_elsewhere: true }), undefined, "pve-01 · protected another way"],
  ])("%s", (_, entry, label, summary) => {
    expect(upsHistoryEventLabel(entry, t)).toBe(label);
    expect(upsHistorySummary(entry, t)).toBe(summary);
  });

  // Its details hold the end in UTC; the note says it in local time, in the
  // Power events tab's words.
  it("notes when a power event ended", () => {
    expect(upsHistoryNote(power("on_battery", { ended_at: null }), t)).toBe("Ended: Ongoing");
    expect(upsHistoryNote(power("unreachable", { ended_at: "2026-11-03T12:02:00Z", end_observed: false }), t)).toMatch(
      /^Ended: By .+ \(not seen: Nexara had lost contact\)$/,
    );
    expect(upsHistoryNote(power("self_test", { ended_at: at }), t)).toBeUndefined();
  });

  it("summarizes a maintenance window by its span", () => {
    const opened = audit("ups_maintenance_window_created", { starts_at: "2026-11-04T10:00:00Z", ends_at: "2026-11-04T11:00:00Z" });
    expect(upsHistorySummary(opened, t)).toMatch(/^.+ to .+$/);
  });

  it("is read from the UPS's own route, as a global History", () => {
    const history = upsHistory("u1");
    expect(history.target.clusterId).toBeNull();
    expect(history.permission).toBe("ups");
    expect(String(history.target.path(new URLSearchParams()))).toBe("/api/v1/ups-devices/u1/history");
  });
});
