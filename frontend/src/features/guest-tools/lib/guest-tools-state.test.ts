import { describe, it, expect } from "vitest";
import { guestToolsStagedMismatch } from "./guest-tools-state";
import type { GuestToolsGuest, GuestToolsStage } from "../types/guest-tools";

/** A guest with only the fields the mismatch rule reads set to anything real. */
function guest(
  stage: GuestToolsStage,
  staged_version: string,
  target_version: string,
): GuestToolsGuest {
  return {
    vmid: 101,
    name: "win-test",
    node: "pve1",
    status: "running",
    template: false,
    installed_version: "0.1.285",
    agent_version: "110.0.2",
    agent_running: true,
    detected_at: null,
    stage,
    reboot_required: false,
    staged_version,
    staged_at: null,
    last_error: "",
    last_result_at: null,
    excluded: false,
    policy_target_version: "",
    note: "",
    target_version,
    up_to_date: false,
    needs_update: false,
  };
}

describe("guestToolsStagedMismatch", () => {
  // The case it exists for: 0.1.302 went out, broke something, the operator
  // pinned back to 0.1.285 — and every already-staged guest is still armed
  // with 0.1.302 until the reconcile loop withdraws it. Until then the Target
  // column reads 0.1.285, which is not what would install.
  it("names the version that will actually install when the target moved", () => {
    expect(
      guestToolsStagedMismatch(guest("staged", "0.1.302-1", "0.1.285-1")),
    ).toBe("Will install 0.1.302-1, not 0.1.285-1");
  });

  it("says nothing when the staged version is the target", () => {
    expect(
      guestToolsStagedMismatch(guest("staged", "0.1.302-1", "0.1.302-1")),
    ).toBeNull();
  });

  // The gate matches exactly what the backend withdraws, so the UI never
  // promises a withdrawal that is not coming: 'staging' is a row mid-write that
  // is about to carry the new version anyway, 'running' is an install already
  // underway that nothing withdraws, and a terminal staged_version records what
  // ran rather than what will run.
  it("says nothing for any stage the backend will not withdraw", () => {
    const stages: GuestToolsStage[] = [
      "staging",
      "running",
      "idle",
      "succeeded",
      "failed",
    ];
    for (const stage of stages) {
      expect(
        guestToolsStagedMismatch(guest(stage, "0.1.302-1", "0.1.285-1")),
      ).toBeNull();
    }
  });

  it("says nothing when either version is unknown", () => {
    expect(
      guestToolsStagedMismatch(guest("staged", "", "0.1.285-1")),
    ).toBeNull();
    expect(
      guestToolsStagedMismatch(guest("staged", "0.1.302-1", "")),
    ).toBeNull();
  });
});
