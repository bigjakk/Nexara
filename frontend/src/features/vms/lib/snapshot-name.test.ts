import { describe, it, expect } from "vitest";
import { snapshotNameError } from "./snapshot-name";

describe("snapshotNameError", () => {
  it("accepts valid Proxmox snapshot names", () => {
    const valid = [
      "ab",
      "before-upgrade",
      "Snap_2026-07-30",
      "a1",
      "a".repeat(40),
    ];
    for (const name of valid) {
      expect(snapshotNameError(name)).toBeNull();
    }
  });

  it("returns null for empty input (required handles that case)", () => {
    expect(snapshotNameError("")).toBeNull();
  });

  it("flags spaces", () => {
    expect(snapshotNameError("my snap")).toMatch(/spaces/i);
    expect(snapshotNameError(" abc")).toMatch(/spaces/i);
  });

  it("flags names not starting with a letter", () => {
    expect(snapshotNameError("1abc")).toMatch(/start with a letter/i);
    expect(snapshotNameError("-abc")).toMatch(/start with a letter/i);
    expect(snapshotNameError("_abc")).toMatch(/start with a letter/i);
  });

  it("flags invalid characters", () => {
    expect(snapshotNameError("ab.c")).toMatch(/only letters/i);
    expect(snapshotNameError("ab/c")).toMatch(/only letters/i);
  });

  it("flags too-short and too-long names", () => {
    expect(snapshotNameError("a")).toMatch(/at least 2/i);
    expect(snapshotNameError("a".repeat(41))).toMatch(/40/);
  });

  it("flags the reserved name current", () => {
    expect(snapshotNameError("current")).toMatch(/reserved/i);
  });
});
