import { describe, expect, it } from "vitest";
import {
  ACTIVE_MIGRATION_STATUSES,
  checkLabel,
  jobSummary,
} from "./job-summary";

describe("jobSummary", () => {
  it.each([
    ["no jobs is null, for the page to say what it is for", [], null],
    ["one finished job", ["completed"], "1 migration"],
    [
      "jobs in flight and failed each get a clause",
      ["pending", "migrating", "failed", "completed", "cancelled"],
      "5 migrations, 2 in progress, 1 failed",
    ],
    [
      "a clause with nothing to say is left out",
      ["failed"],
      "1 migration, 1 failed",
    ],
  ] as const)("%s", (_name, statuses, want) => {
    expect(jobSummary("migration", statuses, ACTIVE_MIGRATION_STATUSES)).toBe(
      want,
    );
  });
});

describe("checkLabel", () => {
  it.each([
    ["target_node_online", "Target node"],
    ["cpu_compatibility", "CPU compatibility"],
    ["disk_space_free", "Disk space free"],
  ])("labels %j as %j", (name, want) => {
    expect(checkLabel(name)).toBe(want);
  });
});
