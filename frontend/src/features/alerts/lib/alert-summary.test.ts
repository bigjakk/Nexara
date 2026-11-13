import { describe, expect, it } from "vitest";
import i18n from "@/lib/i18n";
import type { AlertSummary } from "@/types/api";
import { alertSummaryLine } from "./alert-summary";

function summary(
  firing: number,
  pending: number,
  acknowledged: number,
): AlertSummary {
  return {
    firing_count: firing,
    pending_count: pending,
    acknowledged_count: acknowledged,
    critical_firing: 0,
    warning_firing: firing,
    info_firing: 0,
  };
}

describe("alertSummaryLine", () => {
  const t = i18n.getFixedT("en", "alerts");

  it.each([
    [
      "not loaded",
      undefined,
      "Alerts your rules have raised, and where they are sent",
    ],
    ["quiet", summary(0, 0, 0), "Nothing is firing"],
    ["firing and pending", summary(2, 1, 0), "2 firing, 1 pending"],
    [
      "only waiting",
      summary(0, 1, 3),
      "Nothing is firing, 1 pending, 3 acknowledged",
    ],
  ])("%s", (_name, given, line) => {
    expect(alertSummaryLine(given, t)).toBe(line);
  });
});
