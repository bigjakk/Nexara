import { describe, it, expect } from "vitest";
import {
  isNodePVEAtLeast,
  isPVEAtLeast,
  isPVEVersionKnown,
  nodePVEVersion,
  PVE_FEATURES,
  PVE_NODE_FEATURES,
} from "./pve-version";

describe("isPVEAtLeast", () => {
  it("compares major.minor.patch leniently", () => {
    expect(isPVEAtLeast("9.2.0", "9.2")).toBe(true);
    expect(isPVEAtLeast("9.1.8", "9.2")).toBe(false);
    expect(isPVEAtLeast("9.0", "8.4")).toBe(true);
    expect(isPVEAtLeast("8.4.1", "9.0")).toBe(false);
    expect(isPVEAtLeast("9.1.2", "9.1")).toBe(true);
    expect(isPVEAtLeast("9.1.2", "9.1.3")).toBe(false);
  });

  it("returns false for empty or unparseable versions", () => {
    expect(isPVEAtLeast("", "9.0")).toBe(false);
    expect(isPVEAtLeast("not-a-version", "9.0")).toBe(false);
  });

  it("gates capabilities via PVE_FEATURES", () => {
    expect(isPVEAtLeast("9.2.1", PVE_FEATURES.CRS_DYNAMIC)).toBe(true);
    expect(isPVEAtLeast("9.1.8", PVE_FEATURES.CRS_DYNAMIC)).toBe(false);
    expect(isPVEAtLeast("9.0.0", PVE_FEATURES.HA_RULES)).toBe(true);
    expect(isPVEAtLeast("9.1.0", PVE_FEATURES.OCI_IMAGES)).toBe(true);
  });
});

describe("isPVEVersionKnown", () => {
  it("is false for a version isPVEAtLeast cannot compare", () => {
    expect(isPVEVersionKnown("")).toBe(false);
    expect(isPVEVersionKnown("not-a-version")).toBe(false);
  });

  it("is true for any version it can, the old ones included", () => {
    expect(isPVEVersionKnown("8.4.1")).toBe(true);
    expect(isPVEVersionKnown("9.0")).toBe(true);
  });
});

describe("nodePVEVersion", () => {
  it.each([
    // What the node list carries: the package string with its build hash.
    ["pve-manager/9.2.20/0123abcd", "9.2.20"],
    ["pve-manager/8.1.3", "8.1.3"],
    ["  pve-manager/9.0.5/0123abcd  ", "9.0.5"],
    // Not that form: returned as it is, trimmed.
    ["9.2.1", "9.2.1"],
    [" 9.2 ", "9.2"],
    ["", ""],
    ["   ", ""],
  ])("reads %j as %j", (given, version) => {
    expect(nodePVEVersion(given)).toBe(version);
  });

  it("keeps an unknown string unknown to isPVEAtLeast", () => {
    expect(isPVEAtLeast(nodePVEVersion("pve-manager/"), "8.0")).toBe(false);
    expect(isPVEAtLeast(nodePVEVersion("not-a-version"), "8.0")).toBe(false);
  });
});

describe("isNodePVEAtLeast", () => {
  it("compares the version inside the package string", () => {
    expect(isNodePVEAtLeast("pve-manager/9.2.20/0123abcd", "9.2.3")).toBe(true);
    expect(isNodePVEAtLeast("pve-manager/9.2.2/0123abcd", "9.2.3")).toBe(false);
    expect(isNodePVEAtLeast("9.2.3", "9.2.3")).toBe(true);
  });

  it("is false for an empty or unparseable version", () => {
    expect(isNodePVEAtLeast("", "8.0.0")).toBe(false);
    expect(isNodePVEAtLeast("pve-manager/", "8.0.0")).toBe(false);
  });

  // The floors are patch releases: each pair straddles one, so a comparison
  // that read only major.minor would call both sides alike.
  it.each([
    ["BALLOONING_TARGET", "8.3.5", "8.3.6"],
    ["WOL_BIND_BROADCAST", "8.1.8", "8.1.9"],
    ["LOCATION", "9.1.12", "9.1.13"],
  ] as const)("%s starts at its own patch release", (feature, below, from) => {
    const floor = PVE_NODE_FEATURES[feature];
    expect(floor).toBe(from);
    expect(isNodePVEAtLeast(`pve-manager/${below}/0123abcd`, floor)).toBe(
      false,
    );
    expect(isNodePVEAtLeast(`pve-manager/${from}/0123abcd`, floor)).toBe(true);
  });
});
