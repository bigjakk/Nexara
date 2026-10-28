import { expect, it } from "vitest";
import { latestRuns } from "./veeam-live-runs";
import { session } from "./veeam.fixtures";

const run = (id: string, at: string) => session({ id, creation_time: at });

it("lists the latest runs, newest first", () => {
  const runs = [
    run("old", "2026-08-26T10:00:00Z"),
    run("new", "2026-08-26T11:00:00Z"),
    run("oldest", "2026-08-26T09:00:00Z"),
  ];
  expect(latestRuns(runs, null, 2).map((r) => r.id)).toEqual(["new", "old"]);
});

// The time filter reaches the live rows as it does the entries below them.
it("leaves out the runs from before the time filter", () => {
  const runs = [
    run("old", "2026-08-26T10:00:00Z"),
    run("new", "2026-08-26T11:00:00Z"),
  ];
  const since = new Date("2026-08-26T10:30:00Z").getTime();
  expect(latestRuns(runs, since).map((r) => r.id)).toEqual(["new"]);
});
