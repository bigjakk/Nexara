import { describe, expect, it } from "vitest";
import type { ReportRun, ReportSchedule } from "@/types/api";
import { reportsSummary } from "./reports-summary";

const schedule = (enabled: boolean) => ({ enabled }) as ReportSchedule;
const run = (status: ReportRun["status"], created_at: string) =>
  ({ status, created_at }) as ReportRun;
const ago = (iso: string) => `since ${iso.slice(0, 10)}`;

describe("reportsSummary", () => {
  it.each<{
    what: string;
    schedules: ReportSchedule[] | undefined;
    runs: ReportRun[] | undefined;
    want: string;
  }>([
    {
      what: "says what the page is for until both lists have loaded",
      schedules: undefined,
      runs: [],
      want: "Generate a report now, or schedule one and have it emailed.",
    },
    {
      what: "says so when there is nothing at all",
      schedules: [],
      runs: [],
      want: "No schedules and no reports yet. Generate a report now, or schedule one and have it emailed.",
    },
    {
      what: "counts the schedules that are off, and dates the newest finished run",
      schedules: [schedule(true), schedule(false), schedule(true)],
      runs: [
        run("completed", "2026-01-02T00:00:00Z"),
        run("completed", "2026-01-05T00:00:00Z"),
        // Newer, but nothing a reader can open.
        run("failed", "2026-01-09T00:00:00Z"),
      ],
      want: "3 schedules (1 off), last report generated since 2026-01-05",
    },
    {
      what: "keeps a lone schedule singular when no run has finished",
      schedules: [schedule(true)],
      runs: [run("running", "2026-01-09T00:00:00Z")],
      want: "1 schedule, no report generated yet",
    },
    {
      what: "still dates the last report with no schedules",
      schedules: [],
      runs: [run("completed", "2026-01-05T00:00:00Z")],
      want: "No schedules, last report generated since 2026-01-05",
    },
  ])("$what", ({ schedules, runs, want }) => {
    expect(reportsSummary(schedules, runs, ago)).toBe(want);
  });
});
