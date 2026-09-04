import { describe, it, expect } from "vitest";
import {
  guestToolsInFlight,
  guestToolsPolicy,
  guestToolsStagedMismatch,
  guestToolsStageAction,
  guestToolsState,
} from "./guest-tools-state";
import type { GuestToolsGuest, GuestToolsStage } from "../types/guest-tools";

/**
 * A base guest with no flag set: not excluded, no reboot pending, and neither
 * up to date nor behind. Every describe below spreads over it and sets only the
 * fields its own rule reads.
 */
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

describe("guestToolsState", () => {
  /** Whatever the state chain reads, over an otherwise up-to-date guest. */
  const g = (over: Partial<GuestToolsGuest> = {}): GuestToolsGuest => ({
    ...guest("idle", "", ""),
    installed_version: "0.1.302",
    up_to_date: true,
    ...over,
  });

  // Precedence, top to bottom. Excluded outranks everything, including a
  // failure: an operator who said "never touch this guest" is not asking to be
  // told about the last attempt.
  it.each([
    [
      { excluded: true, stage: "failed" as GuestToolsStage },
      "Excluded",
      "outline",
    ],
    [{ reboot_required: true }, "Updated - reboot to finish", "secondary"],
    [{ stage: "staging" as GuestToolsStage }, "Staging", "secondary"],
    [
      { stage: "staged" as GuestToolsStage },
      "Staged for next boot",
      "secondary",
    ],
    [{ stage: "running" as GuestToolsStage }, "Installing", "secondary"],
    [{ stage: "failed" as GuestToolsStage }, "Failed", "destructive"],
  ])("resolves %o", (over, label, variant) => {
    expect(guestToolsState(g(over))).toMatchObject({ label, variant });
  });

  // reboot_required wins over the stage, which by then reads "succeeded".
  it("calls a rebooted-pending guest updated, not succeeded", () => {
    expect(
      guestToolsState(g({ stage: "succeeded", reboot_required: true })),
    ).toMatchObject({ label: "Updated - reboot to finish" });
  });

  // The one place label and variant disagree, and the reason guest-tools-state
  // keeps them as two expressions: the label leads with the version check, the
  // variant with the flags. The server never emits this combination — it clears
  // both flags when installed_version is empty — so nothing but this test stops
  // the next reader from "unifying" the two and changing a badge colour.
  it("keeps the label on the version and the variant on the flags", () => {
    expect(
      guestToolsState(
        g({ installed_version: "", up_to_date: false, needs_update: true }),
      ),
    ).toMatchObject({ label: "Unknown", variant: "secondary" });
    expect(
      guestToolsState(
        g({ installed_version: "", up_to_date: true, needs_update: false }),
      ),
    ).toMatchObject({ label: "Unknown", variant: "default" });
  });

  it("tells never-read apart from up-to-date", () => {
    expect(
      guestToolsState(g({ installed_version: "", up_to_date: false })),
    ).toMatchObject({ label: "Unknown", variant: "outline" });
    expect(guestToolsState(g())).toMatchObject({
      label: "Up to date",
      variant: "default",
    });
    expect(
      guestToolsState(g({ up_to_date: false, needs_update: true })),
    ).toMatchObject({ label: "Update available", variant: "secondary" });
  });

  // A pending reboot is not a failure and must not read as one; anything else
  // last_error carries is.
  it("mutes last_error only while a reboot is pending", () => {
    expect(guestToolsState(g({ reboot_required: true })).errorTone).toBe(
      "text-muted-foreground",
    );
    expect(guestToolsState(g({ stage: "failed" })).errorTone).toBe(
      "text-destructive",
    );
  });
});

describe("guestToolsInFlight", () => {
  it.each([
    ["staging", true],
    ["staged", true],
    ["running", true],
    ["idle", false],
    ["succeeded", false],
    ["failed", false],
  ] as [GuestToolsStage, boolean][])("%s -> %s", (stage, expected) => {
    expect(guestToolsInFlight(guest(stage, "", ""))).toBe(expected);
  });
});

describe("guestToolsStageAction", () => {
  // needs_update is checked first, so a guest that is behind stages an update
  // whether or not it already has tools installed.
  it.each([
    [{ needs_update: true, installed_version: "0.1.285" }, "stage"],
    [{ needs_update: true, installed_version: "" }, "stage"],
    [{ needs_update: false, installed_version: "0.1.302" }, "reinstall"],
    [{ needs_update: false, installed_version: "" }, "install"],
  ])("%o -> %s", (over, expected) => {
    expect(guestToolsStageAction({ ...guest("idle", "", ""), ...over })).toBe(
      expected,
    );
  });
});

describe("guestToolsPolicy", () => {
  // The endpoint overwrites target_version and note with whatever arrives, so
  // a caller changing one field has to resend the other two or clear them.
  it("resends the fields it is not changing", () => {
    const g: GuestToolsGuest = {
      ...guest("idle", "", ""),
      excluded: false,
      policy_target_version: "0.1.285-1",
      note: "pinned pending driver validation",
    };
    expect(guestToolsPolicy(g, { excluded: true })).toEqual({
      excluded: true,
      target_version: "0.1.285-1",
      note: "pinned pending driver validation",
    });
    expect(guestToolsPolicy(g, { target_version: "" })).toEqual({
      excluded: false,
      target_version: "",
      note: "pinned pending driver validation",
    });
  });
});
