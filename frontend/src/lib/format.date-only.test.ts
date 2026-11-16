import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { usePreferencesStore } from "@/stores/preferences-store";
import { formatDate } from "./format";

// A bare date is that calendar day, not UTC midnight, which west of UTC reads
// as the day before. Pinned to a generic zone five hours west so the case can
// fail on a machine in UTC; restored after, so it cannot leak to other files.
// The frontend's types leave Node out; vitest runs on Node, which re-reads TZ.
const env = (
  globalThis as unknown as { process: { env: Record<string, string | undefined> } }
).process.env;
const before = env["TZ"];
beforeAll(() => {
  env["TZ"] = "Etc/GMT+5";
});
afterAll(() => {
  if (before === undefined) delete env["TZ"];
  else env["TZ"] = before;
  usePreferencesStore.getState().setPreferences({ dateDisplay: "auto" });
});

describe("a date-only value", () => {
  it("reads as its own calendar day west of UTC", () => {
    expect(new Date("2026-11-16T00:00:00Z").getDate()).toBe(15);
    usePreferencesStore.getState().setPreferences({ dateDisplay: "iso" });
    expect(formatDate("2026-11-16")).toBe("2026-11-16");
  });
});
