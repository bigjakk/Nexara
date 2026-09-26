import { describe, it, expect } from "vitest";
import {
  AUTO_SNAPSHOT_PREFIX,
  scheduledSnapshotNamePattern,
  snapshotNameError,
  snapshotPrefixError,
  SNAPSHOT_NAME_PREFIX_MAX_LENGTH,
} from "./snapshot-name";
import type { ResourceKind } from "../types/vm";

const KINDS: ResourceKind[] = ["vm", "ct"];

describe("snapshotNameError", () => {
  // The shape rules are identical for both guest kinds — only the reserved
  // set differs — so run them over every kind to keep them that way.
  describe.each(KINDS)("shape rules (%s)", (kind) => {
    it("accepts valid Proxmox snapshot names", () => {
      const valid = [
        "ab",
        "before-upgrade",
        "Snap_2026-07-30",
        "a1",
        "a".repeat(40),
        // Plausible names Proxmox does NOT reserve, for both kinds: a
        // reserved set widened past the server's turns these red instead of
        // silently 400-ing names the server would have taken.
        "backup",
        "snapshot",
        "vzdump1",
        "template",
      ];
      for (const name of valid) {
        expect(snapshotNameError(name, kind)).toBeNull();
      }
    });

    it("returns null for empty input (required handles that case)", () => {
      expect(snapshotNameError("", kind)).toBeNull();
    });

    it("flags spaces", () => {
      expect(snapshotNameError("my snap", kind)).toMatch(/spaces/i);
      expect(snapshotNameError(" abc", kind)).toMatch(/spaces/i);
    });

    it("flags names not starting with a letter", () => {
      expect(snapshotNameError("1abc", kind)).toMatch(/start with a letter/i);
      expect(snapshotNameError("-abc", kind)).toMatch(/start with a letter/i);
      expect(snapshotNameError("_abc", kind)).toMatch(/start with a letter/i);
    });

    it("flags invalid characters", () => {
      expect(snapshotNameError("ab.c", kind)).toMatch(/only letters/i);
      expect(snapshotNameError("ab/c", kind)).toMatch(/only letters/i);
    });

    it("flags too-short and too-long names", () => {
      expect(snapshotNameError("a", kind)).toMatch(/at least 2/i);
      expect(snapshotNameError("a".repeat(41), kind)).toMatch(/40/);
    });

    it("flags the reserved name current", () => {
      expect(snapshotNameError("current", kind)).toMatch(/reserved/i);
    });
  });

  // Mirrors proxmox.ReservedSnapshotName in internal/proxmox/client_guests.go: "current"
  // for both kinds (exact), "pending" for VMs only (case-insensitive),
  // "vzdump" for containers only (exact).
  describe("reserved names", () => {
    it("refuses pending on a VM, whatever its case", () => {
      expect(snapshotNameError("pending", "vm")).toMatch(/reserved/i);
      expect(snapshotNameError("Pending", "vm")).toMatch(/reserved/i);
      expect(snapshotNameError("PENDING", "vm")).toMatch(/reserved/i);
      expect(snapshotNameError("pEnDiNg", "vm")).toMatch(/reserved/i);
    });

    it("accepts pending on a container — an LXC config spells it [pve:pending]", () => {
      expect(snapshotNameError("pending", "ct")).toBeNull();
      expect(snapshotNameError("Pending", "ct")).toBeNull();
    });

    it("refuses vzdump on a container", () => {
      expect(snapshotNameError("vzdump", "ct")).toMatch(/reserved/i);
    });

    it("accepts vzdump on a VM — only containers reserve it", () => {
      expect(snapshotNameError("vzdump", "vm")).toBeNull();
    });

    it("accepts VZDump on a container — upstream compares it with eq, not lc", () => {
      expect(snapshotNameError("VZDump", "ct")).toBeNull();
    });

    it("accepts Current on both kinds — only pending folds case", () => {
      expect(snapshotNameError("Current", "vm")).toBeNull();
      expect(snapshotNameError("Current", "ct")).toBeNull();
    });
  });
});

// A snapshot schedule stores a PREFIX: each run's snapshot is
// <prefix>-YYYYMMDD-HHMMSS. Mirrors the API's check — the handler's
// validateSnapshotScheduleParams, where an empty snap_name means "auto", over
// proxmox.ValidateSnapshotNamePrefix (internal/proxmox/client_guests.go).
describe("scheduled snapshot prefix", () => {
  // A real run's suffix, for checking the name a run would actually send.
  const aRun = "-20260926-020000";

  it("leaves 24 characters for the prefix", () => {
    expect(SNAPSHOT_NAME_PREFIX_MAX_LENGTH).toBe(24);
    // The budget fills the 40 exactly: a longer one would let a prefix
    // through whose every run the whole-name rule refuses.
    expect(snapshotNameError("a".repeat(24) + aRun, "vm")).toBeNull();
    expect(snapshotNameError("a".repeat(25) + aRun, "vm")).not.toBeNull();
  });

  it("describes the name each run takes, auto standing in for an empty prefix", () => {
    expect(AUTO_SNAPSHOT_PREFIX).toBe("auto");
    expect(scheduledSnapshotNamePattern("")).toBe("auto-YYYYMMDD-HHMMSS");
    expect(scheduledSnapshotNamePattern("nightly")).toBe(
      "nightly-YYYYMMDD-HHMMSS",
    );
  });

  describe.each(KINDS)("on a %s", (kind) => {
    it("returns null for an empty prefix — the scheduler uses auto", () => {
      expect(snapshotPrefixError("", kind)).toBeNull();
    });

    it("accepts prefixes whose run names Proxmox takes", () => {
      const valid = [
        "nightly",
        "a", // one letter: the two-character minimum is on the whole name
        "a".repeat(24),
        // Reserved only as whole names; a run's name carries a date.
        "current",
        "pending",
        "PENDING",
        "vzdump",
      ];
      for (const prefix of valid) {
        expect(snapshotPrefixError(prefix, kind)).toBeNull();
      }
    });

    it("flags a prefix over the budget in the prefix's own terms", () => {
      expect(snapshotPrefixError("a".repeat(25), kind)).toMatch(
        /prefix is limited to 24 characters/i,
      );
      // The old whole-name maximum no longer fits once a date is added.
      expect(snapshotPrefixError("a".repeat(40), kind)).toMatch(/24/);
    });

    it("flags a prefix no run name could carry", () => {
      expect(snapshotPrefixError("my snap", kind)).toMatch(/spaces/i);
      expect(snapshotPrefixError("1abc", kind)).toMatch(/start with a letter/i);
      expect(snapshotPrefixError("-abc", kind)).toMatch(/start with a letter/i);
      expect(snapshotPrefixError("ab.c", kind)).toMatch(/only letters/i);
    });

    // What the check is FOR: its verdict is the whole-name rule's verdict on
    // the name a run sends. Disagreeing either way is a form that refuses a
    // schedule Proxmox would run, or accepts one that fails on every fire.
    it("agrees with the whole-name rule on the name a run sends", () => {
      const prefixes = [
        "a",
        "nightly",
        "current",
        "Pending",
        "vzdump",
        "x_y-z",
        "a".repeat(24),
        "a".repeat(25),
        " ",
        "my snap",
        "a.b",
        "1a",
        "-a",
        "_a",
        "é",
      ];
      for (const prefix of prefixes) {
        expect(
          snapshotPrefixError(prefix, kind) === null,
          `prefix ${JSON.stringify(prefix)}`,
        ).toBe(snapshotNameError(prefix + aRun, kind) === null);
      }
    });
  });
});
