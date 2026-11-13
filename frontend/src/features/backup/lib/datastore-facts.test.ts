import { describe, expect, it } from "vitest";
import type {
  PBSDatastoreMetric,
  PBSSnapshot,
  PBSTask,
  PBSVerifyJob,
} from "../types/backup";
import {
  capacityForecast,
  lastGarbageCollection,
  runStateTone,
  snapshotFacts,
  verifyFacts,
} from "./datastore-facts";

const STORE = "datastore01";

describe("runStateTone", () => {
  it.each([
    ["", "idle"],
    ["OK", "ok"],
    ["ok", "ok"],
    ["WARNINGS: 2", "warn"],
    ["connection refused", "bad"],
  ])("%j reads as %s", (state, tone) => {
    expect(runStateTone(state)).toBe(tone);
  });
});

it("counts a datastore's own snapshots, their guests and the verified ones", () => {
  const snap = (over: Partial<PBSSnapshot>) =>
    ({
      datastore: STORE,
      backup_type: "vm",
      backup_id: "100",
      verified: true,
      ...over,
    }) as PBSSnapshot;
  expect(
    snapshotFacts(
      [
        snap({}),
        snap({ verified: false }),
        snap({ backup_type: "ct" }),
        snap({ datastore: "datastore02" }),
      ],
      STORE,
    ),
  ).toEqual({ count: 3, guests: 2, verified: 2 });
});

describe("lastGarbageCollection", () => {
  // A task has no end, and no status, while it runs.
  const running = (over: Partial<PBSTask> = {}): PBSTask => ({
    upid: `UPID:pbs01:1:2:3:4:garbage_collection:${STORE}:root@pam:`,
    node: "pbs01",
    pid: 1,
    starttime: 100,
    worker_type: "garbage_collection",
    user: "root@pam",
    ...over,
  });
  const ended = (over: Partial<PBSTask> = {}): PBSTask =>
    running({ endtime: 160, status: "OK", ...over });

  it.each([
    ["no tasks", [], null],
    [
      "another datastore's, or another kind of task",
      [
        ended({ upid: "UPID:pbs01:1:2:3:4:garbage_collection:datastore02:r:" }),
        ended({ worker_type: "prunejob" }),
      ],
      null,
    ],
    [
      "the newest of several, by when it began",
      [ended(), ended({ starttime: 500, endtime: 560, status: "disk full" })],
      { outcome: "failed", at: 560 },
    ],
    [
      "one still running has only a start",
      [ended(), running({ starttime: 900 })],
      { outcome: "running", at: 900 },
    ],
  ])("%s", (_, tasks, expected) => {
    expect(lastGarbageCollection(tasks, STORE)).toEqual(expected);
  });
});

it("reports a datastore's verify jobs by their schedules and worst last run", () => {
  const job = (over: Partial<PBSVerifyJob>) =>
    ({
      store: STORE,
      schedule: "daily",
      last_run_state: "OK",
      ...over,
    }) as PBSVerifyJob;
  expect(verifyFacts([job({ store: "datastore02" })], STORE)).toEqual({
    jobs: 0,
    schedule: "",
    tone: "idle",
  });
  expect(
    verifyFacts(
      [
        job({}),
        job({ schedule: "" }),
        job({ schedule: "sat 02:00", last_run_state: "verification failed" }),
      ],
      STORE,
    ),
  ).toEqual({ jobs: 3, schedule: "daily, sat 02:00", tone: "bad" });
});

describe("capacityForecast", () => {
  const DAY = 86_400_000;
  const T0 = Date.parse("2026-09-01T00:00:00Z");
  const samples = (used: number[], total = 1_000): PBSDatastoreMetric[] =>
    used.map((u, i) => ({
      time: new Date(T0 + i * DAY).toISOString(),
      pbs_server_id: "pbs-1",
      datastore: STORE,
      total,
      used: u,
      avail: total - u,
    }));

  it("needs three samples before it draws anything", () => {
    expect(capacityForecast(samples([10, 20]), STORE).points).toEqual([]);
  });

  it("projects a growing datastore to the step at which it fills", () => {
    // 100 a day from 100 reaches 1010 a little after nine days, which the
    // six-hour steps first see at nine and a quarter.
    const forecast = capacityForecast(samples([100, 200, 300], 1_010), STORE);
    expect(forecast.totalCapacity).toBe(1_010);
    expect(forecast.fullDate?.getTime()).toBe(T0 + 9.25 * DAY);
    expect(forecast.points.at(-1)?.forecast).toBe(1_010);
  });

  it("projects nothing for a datastore that is not growing", () => {
    const forecast = capacityForecast(samples([300, 300, 290]), STORE);
    expect(forecast.fullDate).toBeNull();
    expect(forecast.points).toHaveLength(3);
  });
});
