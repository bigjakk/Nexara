import { describe, it, expect } from "vitest";
import {
  anyRunInFlight,
  isRunInFlight,
  scheduleRunState,
} from "./schedule-status";

// The scheduler used to write "success" the moment Proxmox accepted a run's
// call, so a schedule read Success while every run's task failed. It now
// records "dispatched" until the task ends, and the tab has to read that — and
// the claim's "running" — as a run under way, not as the failure it showed for
// any value other than "success".
describe("scheduleRunState", () => {
  it.each([
    [null, "pending"],
    ["", "pending"],
    ["running", "running"],
    ["dispatched", "running"],
    ["success", "success"],
    ["failed", "failed"],
  ] as const)("reads %j as %s", (lastStatus, want) => {
    expect(scheduleRunState(lastStatus)).toBe(want);
  });

  // A value this build does not know keeps the tab's old reading: anything
  // but a success is shown as failed, never as a success nobody reported.
  // "toString" is there because the lookup is a plain object, and a bare `in`
  // would find it on the prototype.
  it.each(["completed", "SUCCESS", "toString", "constructor"])(
    "reads an unknown value %j as failed",
    (lastStatus) => {
      expect(scheduleRunState(lastStatus)).toBe("failed");
    },
  );
});

// Which rows a view polls for: an enabled schedule's run that is under way.
// Anything else would keep every open tab re-reading forever — nothing
// re-claims a disabled schedule, so one stuck at "running" never moves.
describe("isRunInFlight", () => {
  const row = (last_status: string | null, enabled = true) => ({
    id: "sch-1",
    enabled,
    last_status,
    last_run_at: "2026-09-26T02:00:00Z",
  });

  it.each([
    ["an enabled schedule's task under way", row("dispatched"), true],
    ["an enabled schedule's run being started", row("running"), true],
    ["a disabled schedule stuck at running", row("running", false), false],
    ["a disabled schedule's task under way", row("dispatched", false), false],
    ["a settled run", row("success"), false],
    ["a failed run", row("failed"), false],
    ["a schedule that never ran", row(null), false],
  ] as const)("%s → %s", (_name, schedule, want) => {
    expect(isRunInFlight(schedule)).toBe(want);
  });
});

// How long a view polls: thirty minutes from when IT first saw the run, on the
// browser's own clock. The run's start as the server recorded it (last_run_at)
// is only the run's identity, so a server clock hours away from the browser's
// can neither stop polling before it starts nor keep it going for hours.
describe("anyRunInFlight", () => {
  const MINUTE = 60_000;
  const t0 = Date.parse("2026-09-26T03:00:00Z");
  const run = (lastRunAt: string, last_status = "dispatched") => ({
    id: "sch-1",
    enabled: true,
    last_status,
    last_run_at: lastRunAt,
  });
  const at = (ms: number) => new Date(ms).toISOString();

  it("polls for thirty minutes from the moment the view first sees a run", () => {
    const sightings = new Map<string, number>();
    const rows = [run(at(t0 - MINUTE))];
    expect(anyRunInFlight(rows, t0, sightings)).toBe(true);
    expect(anyRunInFlight(rows, t0 + 29 * MINUTE, sightings)).toBe(true);
    expect(anyRunInFlight(rows, t0 + 31 * MINUTE, sightings)).toBe(false);
  });

  it("is not stopped by a server clock two hours behind the browser", () => {
    const sightings = new Map<string, number>();
    // By the server's timestamp this run is two hours old already.
    const rows = [run(at(t0 - 120 * MINUTE))];
    expect(anyRunInFlight(rows, t0, sightings)).toBe(true);
    expect(anyRunInFlight(rows, t0 + 29 * MINUTE, sightings)).toBe(true);
  });

  it("is not stretched by a server clock two hours ahead of the browser", () => {
    const sightings = new Map<string, number>();
    // By the server's timestamp this run has not even started.
    const rows = [run(at(t0 + 120 * MINUTE))];
    expect(anyRunInFlight(rows, t0, sightings)).toBe(true);
    expect(anyRunInFlight(rows, t0 + 31 * MINUTE, sightings)).toBe(false);
  });

  it("times a schedule's next run from when that run is seen", () => {
    const sightings = new Map<string, number>();
    const first = [run(at(t0))];
    expect(anyRunInFlight(first, t0, sightings)).toBe(true);
    expect(anyRunInFlight(first, t0 + 31 * MINUTE, sightings)).toBe(false);
    // The next run of the same schedule: a new start, so a new sighting.
    const next = [run(at(t0 + 60 * MINUTE))];
    expect(anyRunInFlight(next, t0 + 61 * MINUTE, sightings)).toBe(true);
  });

  it("forgets a run once it is no longer in flight, and never records a disabled one", () => {
    const sightings = new Map<string, number>();
    anyRunInFlight([run(at(t0))], t0, sightings);
    expect(sightings.size).toBe(1);
    anyRunInFlight([run(at(t0), "success")], t0 + MINUTE, sightings);
    expect(sightings.size).toBe(0);
    expect(
      anyRunInFlight(
        [{ ...run(at(t0), "running"), enabled: false }],
        t0,
        sightings,
      ),
    ).toBe(false);
    expect(sightings.size).toBe(0);
  });

  it("is false with nothing loaded", () => {
    expect(anyRunInFlight(undefined, t0, new Map())).toBe(false);
    expect(anyRunInFlight([], t0, new Map())).toBe(false);
  });
});
