import { describe, it, expect } from "vitest";
import { parsePropString, serializePropString, propStringsEqual } from "./prop-string";

describe("parsePropString", () => {
  it("returns empty for undefined / null / empty", () => {
    expect(parsePropString(undefined)).toEqual({});
    expect(parsePropString(null)).toEqual({});
    expect(parsePropString("")).toEqual({});
  });

  it("parses comma-separated key=value pairs", () => {
    expect(parsePropString("clone=10,migration=20,move=0,restore=0"))
      .toEqual({ clone: "10", migration: "20", move: "0", restore: "0" });
  });

  it("tolerates whitespace around keys, values, and separators", () => {
    expect(parsePropString(" clone = 10 , migration=20 "))
      .toEqual({ clone: "10", migration: "20" });
  });

  it("handles bare keys without an equals sign", () => {
    expect(parsePropString("flag1,key=v")).toEqual({ flag1: "", key: "v" });
  });

  it("preserves later duplicates over earlier", () => {
    expect(parsePropString("k=1,k=2")).toEqual({ k: "2" });
  });
});

describe("serializePropString", () => {
  it("skips empty / nullish values", () => {
    expect(serializePropString({ a: "1", b: "", c: "3" })).toBe("a=1,c=3");
  });

  it("keeps numeric-string zero values", () => {
    expect(serializePropString({ clone: "0", migration: "20" })).toBe("clone=0,migration=20");
  });

  it("returns empty string for empty map", () => {
    expect(serializePropString({})).toBe("");
  });
});

describe("propStringsEqual", () => {
  it("ignores key ordering", () => {
    expect(propStringsEqual("a=1,b=2", "b=2,a=1")).toBe(true);
  });

  it("treats undefined and empty as equal", () => {
    expect(propStringsEqual(undefined, "")).toBe(true);
    expect(propStringsEqual(null, undefined)).toBe(true);
  });

  it("notices a value change", () => {
    expect(propStringsEqual("a=1", "a=2")).toBe(false);
  });

  it("notices a key being added or removed", () => {
    expect(propStringsEqual("a=1", "a=1,b=2")).toBe(false);
  });
});
