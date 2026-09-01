import { describe, it, expect } from "vitest";
import { buildSchedule, parseSchedule } from "./virtio-win-schedule";

describe("parseSchedule", () => {
  it("reads an empty expression as the six-hourly interval", () => {
    expect(parseSchedule("").mode).toBe("interval");
  });

  it("reads a daily expression, zero-padding the time for the input", () => {
    const parsed = parseSchedule("5 3 * * *");
    expect(parsed.mode).toBe("daily");
    // <input type="time"> only accepts HH:MM, so "3:5" would render blank.
    expect(parsed.time).toBe("03:05");
  });

  it("reads a weekly expression with its day", () => {
    const parsed = parseSchedule("30 2 * * 0");
    expect(parsed.mode).toBe("weekly");
    expect(parsed.time).toBe("02:30");
    expect(parsed.weekday).toBe(0);
  });

  it("falls back to custom for an expression the card cannot render", () => {
    // A valid cron the API accepts but the three presets cannot express. It
    // must not be silently rewritten into one of them.
    const parsed = parseSchedule("0 */6 * * *");
    expect(parsed.mode).toBe("custom");
    expect(parsed.cron).toBe("0 */6 * * *");
  });

  it("treats an out-of-range field as custom rather than as a daily time", () => {
    const parsed = parseSchedule("0 99 * * *");
    expect(parsed.mode).toBe("custom");
  });
});

describe("buildSchedule", () => {
  it("emits an empty expression for the interval mode", () => {
    expect(buildSchedule(parseSchedule(""))).toBe("");
  });

  it("round-trips a daily expression", () => {
    expect(buildSchedule(parseSchedule("5 3 * * *"))).toBe("5 3 * * *");
  });

  it("round-trips a weekly expression", () => {
    expect(buildSchedule(parseSchedule("30 2 * * 6"))).toBe("30 2 * * 6");
  });

  it("passes a custom expression through untouched", () => {
    expect(buildSchedule(parseSchedule("0 */6 * * *"))).toBe("0 */6 * * *");
  });

  it("substitutes a default for a half-typed time instead of emitting a broken cron", () => {
    // <input type="time"> reports "" mid-edit, and the API rejects a malformed
    // expression outright — so the save must not carry one.
    const parsed = { ...parseSchedule("5 3 * * *"), time: "" };
    expect(buildSchedule(parsed)).toBe("0 3 * * *");
  });
});
