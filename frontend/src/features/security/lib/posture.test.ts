import { describe, expect, it } from "vitest";
import i18n from "@/lib/i18n";
import type { SecurityPosture } from "@/types/api";
import { posture } from "../security.fixtures";
import { countTone, postureSummary, scoreTone } from "./posture";

const t = i18n.getFixedT("en", "security");

describe("postureSummary", () => {
  it.each<{ what: string; given: SecurityPosture | undefined; want: string }>([
    {
      what: "says what the page is for before the posture has loaded",
      given: undefined,
      want: "Known vulnerabilities on each node, and rolling package updates.",
    },
    {
      // A cluster never scanned reports a score of 100 and no findings: that
      // must not read as a clean bill of health.
      what: "does not call an unscanned cluster clean",
      given: posture({ status: "no_scans", total_nodes: 0, scanned_nodes: 0 }),
      want: "No vulnerability scan has run for this cluster yet",
    },
    {
      what: "says a scan found nothing",
      given: posture({}),
      want: "3 of 3 nodes scanned, no known vulnerabilities",
    },
    {
      what: "counts the findings and names the worst of them",
      given: posture({
        total_nodes: 1,
        scanned_nodes: 1,
        total_vulns: 12,
        critical_count: 2,
        kev_count: 1,
      }),
      want: "1 of 1 node scanned, 12 known vulnerabilities (2 critical, 1 actively exploited)",
    },
    {
      what: "leaves the brackets out when nothing is critical or exploited",
      given: posture({ scanned_nodes: 2, total_vulns: 1, low_count: 1 }),
      want: "2 of 3 nodes scanned, 1 known vulnerability",
    },
  ])("$what", ({ given, want }) => {
    expect(postureSummary(given, t)).toBe(want);
  });
});

// The strip is quiet when healthy: a zero is never coloured, and a good score
// is plain rather than green.
describe("posture tones", () => {
  it.each([
    [0, "quiet"],
    [1, "bad"],
  ] as const)("a count of %i reads %s", (count, tone) => {
    expect(countTone(count, "bad")).toBe(tone);
  });

  it.each([
    [100, "default"],
    [90, "default"],
    [89, "warn"],
    [70, "warn"],
    [69, "bad"],
  ] as const)("a score of %i reads %s", (score, tone) => {
    expect(scoreTone(score)).toBe(tone);
  });
});
