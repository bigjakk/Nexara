import { describe, it, expect } from "vitest";
import { isPVEAtLeast, isPVEVersionKnown, PVE_FEATURES } from "./pve-version";

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
