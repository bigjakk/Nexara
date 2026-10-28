import { describe, expect, it } from "vitest";
import { makeHistoryEntry as entry } from "@/test/history.fixtures";
import type { PBSTask } from "../types/backup";
import {
  entryPbsUpid,
  latestTasks,
  pbsTaskOutcome,
  pbsWorkerId,
  tasksSince,
} from "./pbs-live-tasks";

const task = (over: Partial<PBSTask>): PBSTask => ({
  upid: "UPID:pbs:0000A1B2:00C0FFEE:00000001:66F1A2B3:garbage_collection:datastore01:root@pam!tok:",
  node: "pbs",
  pid: 1,
  starttime: 100,
  worker_type: "garbage_collection",
  user: "root@pam!tok",
  ...over,
});

describe("pbsWorkerId", () => {
  it.each([
    [
      "UPID:pbs:1:2:3:4:garbage_collection:datastore01:root@pam:",
      "datastore01",
    ],
    // PBS escapes a colon or a slash in the worker id as \xNN.
    [
      "UPID:pbs:1:2:3:4:backup:datastore01\\x3avm\\x2f100:root@pam:",
      "datastore01:vm/100",
    ],
    ["UPID:pbs:1:2:3:4:logrotate::root@pam:", ""],
  ])("reads %s as %j", (upid, want) => {
    expect(pbsWorkerId(upid)).toBe(want);
  });
});

describe("pbsTaskOutcome", () => {
  it.each([
    [{}, "running"],
    [{ endtime: 200 }, "ok"],
    [{ endtime: 200, status: "OK" }, "ok"],
    [{ endtime: 200, status: "WARNINGS: 2" }, "warning"],
    [{ endtime: 200, status: "unable to open chunk store" }, "failed"],
  ] as const)("%o is %s", (over, want) => {
    expect(pbsTaskOutcome(task(over))).toBe(want);
  });
});

it("lists the latest tasks, newest first", () => {
  const tasks = [1, 5, 3, 4].map((starttime) =>
    task({ starttime, upid: `u${String(starttime)}` }),
  );
  expect(latestTasks(tasks, 3).map((x) => x.upid)).toEqual(["u5", "u4", "u3"]);
});

it("reads the task an entry records Nexara starting", () => {
  expect(
    entryPbsUpid(entry({ details: JSON.stringify({ pbs_upid: "UPID:x" }) })),
  ).toBe("UPID:x");
  expect(
    entryPbsUpid(entry({ details: JSON.stringify({ upid: "UPID:y" }) })),
  ).toBe("");
});

// The time filter reaches the live rows as it does the entries below them.
it("leaves out the tasks from before the time filter", () => {
  const tasks = [100, 200].map((starttime) =>
    task({ starttime, upid: `u${String(starttime)}` }),
  );
  expect(tasksSince(tasks, 150_000).map((x) => x.upid)).toEqual(["u200"]);
  expect(tasksSince(tasks, null)).toHaveLength(2);
});
