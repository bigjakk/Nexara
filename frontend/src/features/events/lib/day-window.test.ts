import { afterEach, describe, expect, it, vi } from "vitest";
import { dayWindow } from "./day-window";

afterEach(() => {
  vi.unstubAllEnvs();
});

// From was read as midnight UTC and To as 23:59:59 local, so away from UTC the
// window was off by the offset at one end. Etc/GMT-3 is UTC+3, a fixed offset.
describe("dayWindow", () => {
  it("reads both ends in the browser's time zone", () => {
    vi.stubEnv("TZ", "Etc/GMT-3");
    expect(dayWindow("2026-11-18", "2026-11-18")).toEqual({
      startTime: "2026-11-17T21:00:00.000Z",
      endTime: "2026-11-18T20:59:59.000Z",
    });
  });
});
