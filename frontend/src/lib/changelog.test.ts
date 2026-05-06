import { describe, it, expect } from "vitest";
import {
  extractBaseVersion,
  getEntriesToShow,
  type ChangelogEntry,
} from "./changelog";

describe("extractBaseVersion", () => {
  it("strips a leading v", () => {
    expect(extractBaseVersion("v0.2.33")).toBe("0.2.33");
  });

  it("returns the base version from a git-describe suffix", () => {
    expect(extractBaseVersion("v0.2.33-1-gabc123")).toBe("0.2.33");
    expect(extractBaseVersion("v0.2.33-dirty")).toBe("0.2.33");
    expect(extractBaseVersion("v0.2.33-1-gabc-dirty")).toBe("0.2.33");
  });

  it("returns null for non-semver strings", () => {
    expect(extractBaseVersion("dev")).toBeNull();
    expect(extractBaseVersion("unknown")).toBeNull();
    expect(extractBaseVersion("")).toBeNull();
  });

  it("returns null for nullish input", () => {
    expect(extractBaseVersion(null)).toBeNull();
    expect(extractBaseVersion(undefined)).toBeNull();
  });
});

describe("getEntriesToShow", () => {
  const changelog: ChangelogEntry[] = [
    { version: "0.2.33", date: "2026-05-05", highlights: [{ title: "A" }] },
    { version: "0.2.32", date: "2026-05-04", highlights: [{ title: "B" }] },
    { version: "0.2.31", date: "2026-05-03", highlights: [{ title: "C" }] },
  ];

  it("returns just the current entry on first visit", () => {
    expect(getEntriesToShow("0.2.33", null, changelog)).toEqual([changelog[0]]);
  });

  it("returns nothing when last seen matches current", () => {
    expect(getEntriesToShow("0.2.33", "0.2.33", changelog)).toEqual([]);
  });

  it("returns entries between current (inclusive) and last seen (exclusive)", () => {
    expect(getEntriesToShow("0.2.33", "0.2.32", changelog)).toEqual([
      changelog[0],
    ]);
    expect(getEntriesToShow("0.2.33", "0.2.31", changelog)).toEqual([
      changelog[0],
      changelog[1],
    ]);
  });

  it("falls back to current entry when last seen is unknown", () => {
    expect(getEntriesToShow("0.2.33", "9.9.9", changelog)).toEqual([
      changelog[0],
    ]);
  });

  it("returns empty when current version is not in changelog", () => {
    expect(getEntriesToShow("99.99.99", null, changelog)).toEqual([]);
  });

  it("returns empty when changelog is empty", () => {
    expect(getEntriesToShow("0.2.33", null, [])).toEqual([]);
  });
});
