import { describe, expect, it } from "vitest";
import { isPVEAtLeast } from "./pve-version";

describe("isPVEAtLeast", () => {
  it("returns true when current > min on major", () => {
    expect(isPVEAtLeast("10.0", "9.1")).toBe(true);
  });
  it("returns true when current > min on minor", () => {
    expect(isPVEAtLeast("9.2", "9.1")).toBe(true);
    expect(isPVEAtLeast("9.1.2", "9.1")).toBe(true);
  });
  it("returns true when current == min", () => {
    expect(isPVEAtLeast("9.1", "9.1")).toBe(true);
    expect(isPVEAtLeast("9.1.0", "9.1")).toBe(true);
  });
  it("returns false when current < min", () => {
    expect(isPVEAtLeast("9.0.5", "9.1")).toBe(false);
    expect(isPVEAtLeast("8.4", "9.1")).toBe(false);
  });
  it("returns false for empty or unparseable input", () => {
    expect(isPVEAtLeast("", "9.1")).toBe(false);
    expect(isPVEAtLeast("unknown", "9.1")).toBe(false);
    expect(isPVEAtLeast("9.1", "")).toBe(false);
  });
  it("handles patch-suffix release strings like 9.1.2-1", () => {
    expect(isPVEAtLeast("9.1.2-1", "9.1")).toBe(true);
    expect(isPVEAtLeast("9.1.0", "9.1.1")).toBe(false);
  });
});
