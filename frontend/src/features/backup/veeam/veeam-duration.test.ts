import { describe, it, expect } from "vitest";
import { veeamDurationSeconds } from "./veeam-duration";

describe("veeamDurationSeconds", () => {
  it("parses the ordinary HH:MM:SS shape", () => {
    expect(veeamDurationSeconds("00:18:27")).toBe(1107);
    expect(veeamDurationSeconds("12:20:05")).toBe(44405);
  });

  it("reads a day prefix as days, not as an hour", () => {
    // The bug this exists to prevent: "1.02:15:00" is 26h15m. Parsed as three
    // plain numbers it comes out as 1h16m and sorts below a two-hour run —
    // the longest backup in the table lands in the middle of the list.
    expect(veeamDurationSeconds("1.02:15:00")).toBe(94500);
    expect(veeamDurationSeconds("1.02:15:00")).toBeGreaterThan(
      veeamDurationSeconds("02:00:00") ?? 0,
    );
  });

  it("keeps hours past 24 ordered above shorter runs", () => {
    expect(veeamDurationSeconds("26:15:00")).toBe(94500);
  });

  it("returns null rather than a wrong number for anything unrecognised", () => {
    // Number("") is 0, so these would otherwise sort as the SHORTEST runs.
    for (const bad of [
      "",
      "::",
      "0:0:",
      "1:2",
      "abc",
      "00:75:00",
      "1,02:15:00",
    ]) {
      expect(veeamDurationSeconds(bad)).toBeNull();
    }
  });
});
