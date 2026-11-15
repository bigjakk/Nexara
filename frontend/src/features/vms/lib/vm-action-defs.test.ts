import { describe, expect, it } from "vitest";
import { lifecycleActions } from "./vm-action-defs";
import type { ResourceKind } from "../types/vm";

const offered = (state: string, kind: ResourceKind = "vm") =>
  lifecycleActions.filter((a) => a.showWhen(state, kind)).map((a) => a.action);

// What qemu-server does for each QEMU state: resume runs `cont` (or
// `system_wakeup` for S3), which QEMU refuses after an internal error;
// shutdown and reboot are refused for a paused or sleeping VM.
describe("lifecycleActions per guest state", () => {
  it.each([
    ["running", ["shutdown", "reboot", "stop", "reset", "suspend"]],
    ["stopped", ["start"]],
    ["suspended", ["stop", "reset", "resume"]],
    ["asleep", ["stop", "reset", "resume"]],
    ["io-error", ["stop", "reset", "resume"]],
    ["watchdog", ["stop", "reset", "resume"]],
    ["debug", ["stop", "reset", "resume"]],
    ["guest-panicked", ["stop", "reset"]],
    ["internal-error", ["stop", "reset"]],
    ["shutdown", ["stop", "reset"]],
  ])("a VM %s is offered %j", (state, actions) => {
    expect(offered(state)).toEqual(actions);
  });

  it("leaves a suspended container its Start and Resume", () => {
    expect(offered("suspended", "ct")).toEqual(["start", "resume"]);
  });
});
