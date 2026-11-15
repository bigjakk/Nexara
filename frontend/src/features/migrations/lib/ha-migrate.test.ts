import { describe, expect, it } from "vitest";
import { movesStoppedHAGuest } from "./ha-migrate";

describe("movesStoppedHAGuest", () => {
  it.each([
    ["a stopped HA guest", "stopped", "stopped", true],
    ["a stopped guest HA would start", "stopped", "started", true],
    ["a running HA guest migrates live", "running", "started", false],
    ["a guest HA ignores is migrated directly", "stopped", "ignored", false],
    ["a guest HA does not manage", "stopped", "", false],
  ] as const)("%s", (_name, status, haState, want) => {
    expect(movesStoppedHAGuest(status, haState)).toBe(want);
  });
});
