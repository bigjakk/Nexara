import { describe, expect, it } from "vitest";
import {
  eventDuration,
  eventLabel,
  formatRuntime,
  onBatteryRuns,
  upsFlagChips,
  upsOnBattery,
  upsStatus,
} from "./ups-state";

const base = {
  state: "online",
  reachability: "ok",
  reading_stale: false,
  server_enabled: true,
  flags: ["OL"],
} as const;

describe("upsStatus", () => {
  it.each([
    ["online", { ...base }, "Online", "ok"],
    ["on battery", { ...base, state: "on_battery", flags: ["OB"] }, "On battery", "warn"],
    ["low", { ...base, state: "low_battery", flags: ["OB", "LB"] }, "Low battery", "err"],
    ["forced shutdown", { ...base, state: "low_battery", flags: ["FSD", "OL"] }, "Shutting down", "err"],
    // Could not look wins over the last known state, whatever it was.
    ["unreachable mid-outage", { ...base, state: "on_battery", reachability: "unreachable" }, "Unreachable", "idle"],
    [
      "unreachable through a rolling update",
      { ...base, reachability: "unreachable", server_rolling_update: true },
      "Rolling update",
      "idle",
    ],
    ["old reading", { ...base, reading_stale: true }, "No recent reading", "idle"],
    ["driver down", { ...base, reachability: "driver_down" }, "Driver down", "idle"],
    // The driver's last reading goes stale while it is down; it is still the driver.
    ["driver down, its reading stale", { ...base, reachability: "driver_down", reading_stale: true }, "Driver down", "idle"],
    ["never read", { ...base, reachability: "unknown" }, "Waiting", "idle"],
    ["server disabled", { ...base, server_enabled: false }, "Polling off", "idle"],
  ] as const)("%s", (_, d, label, tone) => {
    expect(upsStatus({ ...d, flags: [...d.flags] })).toEqual({ label, tone });
  });
});

describe("upsOnBattery", () => {
  it.each([
    ["on battery", { state: "on_battery", flags: ["OB", "DISCHRG"] }, true],
    ["low and on battery", { state: "low_battery", flags: ["OB", "LB"] }, true],
    // A shutdown test on mains, or power back mid-shutdown.
    ["forced shutdown on mains", { state: "low_battery", flags: ["FSD", "OL"] }, false],
    // The last reading said OB; Nexara cannot read it now.
    ["unreadable", { state: "unknown", flags: ["OB"] }, false],
  ] as const)("%s", (_, d, want) => {
    expect(upsOnBattery({ ...d, flags: [...d.flags] })).toBe(want);
  });
});

describe("onBatteryRuns", () => {
  const p = (t: number, on: boolean) => ({ timestamp: t, on_battery: on }) as never;
  it.each([
    // One bucket on battery still shades a bucket's width.
    ["a single bucket", [p(0, false), p(300, true), p(600, false)], [[300, 600]]],
    ["a run to the end of its last bucket", [p(0, true), p(300, true), p(600, false)], [[0, 600]]],
    ["a run still going", [p(0, false), p(300, true)], [[300, 600]]],
  ] as const)("%s", (_, points, want) => {
    expect(onBatteryRuns([...points], 300)).toEqual(want);
  });
});

describe("upsFlagChips", () => {
  it("skips what the status says and keeps unknown flags", () => {
    expect(
      upsFlagChips({ state: "low_battery", flags: ["OB", "LB", "RB", "XYZ"] }).map((c) => c.label),
    ).toEqual(["Replace battery", "XYZ"]);
    expect(upsFlagChips({ state: "online", flags: ["OL", "LB"] }).map((c) => c.label)).toEqual([
      "Low battery",
    ]);
  });
});

describe("eventLabel", () => {
  it.each([
    ["low battery", { kind: "low_battery", details: { status: "OB DISCHRG LB" } }, "Low battery"],
    ["forced shutdown on mains", { kind: "low_battery", details: { status: "FSD OL" } }, "Forced shutdown"],
    ["self-test result", { kind: "self_test", details: { result: "Done and passed" } }, "Self-test: Done and passed"],
  ] as const)("%s", (_, e, want) => {
    expect(eventLabel(e)).toBe(want);
  });
});

describe("formatRuntime and eventDuration", () => {
  it.each([
    [null, "—"],
    [45, "45s"],
    [580, "9m 40s"],
    [1440, "24 min"],
    [3900, "1h 05m"],
  ])("formatRuntime(%s)", (s, want) => {
    expect(formatRuntime(s)).toBe(want);
  });

  const start = "2026-11-02T03:12:40Z";
  const t = Date.parse(start);
  it.each([
    ["open", { started_at: start, ended_at: null, end_observed: true, kind: "on_battery" }, "ongoing · 6m 40s"],
    ["closed", { started_at: start, ended_at: "2026-11-02T03:16:52Z", end_observed: true, kind: "on_battery" }, "4m 12s"],
    ["end not seen", { started_at: start, ended_at: "2026-11-02T03:16:52Z", end_observed: false, kind: "on_battery" }, "≤ 4m 12s"],
    ["polling turned off", { started_at: start, ended_at: "2026-11-02T03:16:52Z", end_observed: false, kind: "on_battery", details: { ended_by: "polling_off" } }, "≥ 4m 12s"],
    ["point event", { started_at: start, ended_at: start, end_observed: true, kind: "self_test" }, "—"],
  ] as const)("%s", (_, e, want) => {
    expect(eventDuration(e, t + 400_000)).toBe(want);
  });
});
