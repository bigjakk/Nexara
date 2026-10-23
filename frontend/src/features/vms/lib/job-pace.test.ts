import { describe, it, expect } from "vitest";
import { jobPace } from "./job-pace";

describe("jobPace", () => {
  it("paces a job from the fetch before, once it has one", () => {
    const job = {
      type: "mirror",
      device: "mirror-scsi0",
      completed: 3000,
      total: 9000,
      speed_limit: 0,
    };
    expect(jobPace(null, [job], 10_000).size).toBe(0);
    const before = { at: 8_000, done: new Map([["mirror-scsi0", 1000]]) };
    expect(jobPace(before, [job], 10_000).get("mirror-scsi0")).toEqual({
      bps: 1000,
      etaSeconds: 6,
    });
    const restarted = { ...before, done: new Map([["mirror-scsi0", 5000]]) };
    expect(jobPace(restarted, [job], 10_000).size).toBe(0);
  });
});
