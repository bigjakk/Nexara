import { describe, expect, it } from "vitest";
import { guestActionPermission, lifecycleActions } from "./vm-action-defs";
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

  // Its checkpoint comes back only by resume; a start would boot it afresh.
  it("offers a suspended container Resume, not Start", () => {
    expect(offered("suspended", "ct")).toEqual(["resume"]);
  });
});

// Each action is offered only to a role its route accepts (registry_vms.go,
// registry_containers.go, registry_migrations.go).
describe("guestActionPermission", () => {
  it.each([
    ["start", "vm", "execute:vm"],
    ["shutdown", "ct", "execute:container"],
    ["snapshot", "vm", "execute:vm"],
    ["clone", "ct", "manage:container"],
    ["clone-to-template", "vm", "manage:vm"],
    ["convert-to-template", "vm", "manage:vm"],
    ["deploy", "ct", "manage:container"],
    ["migrate", "vm", "manage:migration"],
    ["destroy", "ct", "delete:container"],
    ["rollback", "vm", "execute:vm"],
    ["delete-snapshot", "ct", "delete:container"],
    ["media", "vm", "execute:vm"],
  ] as const)("%s on a %s takes %s", (action, kind, want) => {
    expect(guestActionPermission(action, kind).join(":")).toBe(want);
  });
});
