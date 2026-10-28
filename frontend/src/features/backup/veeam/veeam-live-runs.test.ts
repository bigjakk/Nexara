import { expect, it } from "vitest";
import { latestRuns } from "./veeam-live-runs";
import { session } from "./veeam.fixtures";

const run = (id: string, job: string | null, at: string) =>
  session({ id, job_veeam_id: job, creation_time: at });

it("lists a job's latest runs, newest first, and no other job's", () => {
  const runs = [
    run("old", "j1", "2026-08-26T10:00:00Z"),
    run("other", "j2", "2026-08-26T12:00:00Z"),
    run("new", "j1", "2026-08-26T11:00:00Z"),
    run("none", null, "2026-08-26T13:00:00Z"),
    run("oldest", "j1", "2026-08-26T09:00:00Z"),
  ];
  expect(latestRuns(runs, "j1", null, 2).map((r) => r.id)).toEqual([
    "new",
    "old",
  ]);
});

// The time filter reaches the live rows as it does the entries below them.
it("leaves out the runs from before the time filter", () => {
  const runs = [
    run("old", "j1", "2026-08-26T10:00:00Z"),
    run("new", "j1", "2026-08-26T11:00:00Z"),
  ];
  const since = new Date("2026-08-26T10:30:00Z").getTime();
  expect(latestRuns(runs, "j1", since).map((r) => r.id)).toEqual(["new"]);
});
