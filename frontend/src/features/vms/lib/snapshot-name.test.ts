import { describe, it, expect } from "vitest";
import { snapshotNameError } from "./snapshot-name";
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
