import { describe, expect, it } from "vitest";
import { guestState } from "./guest-state";

// A halted VM's state replaces "running" on screen; QEMU's "suspended" (S3)
// must not read as the status "suspended", which means paused.
describe("guestState", () => {
  it.each([
    ["running", undefined, "running"],
    ["running", "", "running"],
    ["running", "io-error", "io-error"],
    ["running", "suspended", "asleep"],
    ["running", "guest-panicked", "guest-panicked"],
    ["running", "prelaunch", "running"],
    ["Stopped", "io-error", "stopped"], // a stale run state never outlives the run
    ["suspended", "", "suspended"],
  ])("%s with run state %j is %s", (status, runState, want) => {
    expect(guestState(status, runState)).toBe(want);
  });
});
