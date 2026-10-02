import { describe, expect, it } from "vitest";
import { sanitizeReturnTo } from "./return-to";

/**
 * The login page navigates to returnTo once someone is signed in, and it comes
 * from the URL, so anyone can put anything there. Only an in-app path is
 * followed; the rest is "/".
 */
describe("sanitizeReturnTo", () => {
  it.each([
    ["/", "/"],
    ["/clusters", "/clusters"],
    ["/clusters/c1/nodes/n1", "/clusters/c1/nodes/n1"],
    ["/inventory?kind=vm#top", "/inventory?kind=vm#top"],
    ["/a%2Fb", "/a%2Fb"],
    // A space is not a control character: the rule's lower edge is the first
    // one, not "up to and including a space".
    ["/a b", "/a b"],
  ])("follows the in-app path %j", (value, expected) => {
    expect(sanitizeReturnTo(value)).toBe(expected);
  });

  it.each([
    [null],
    [""],
    ["clusters"],
    ["https://evil.example.com/"],
    ["javascript:alert(1)"],
    // A protocol-relative URL: another origin.
    ["//evil.example.com"],
    ["///evil.example.com"],
    // A browser reads a backslash as a slash, so these are another origin too —
    // and navigating to one throws, leaving a signed-in user stuck on /login.
    ["/\\evil.example.com"],
    ["/\\/evil.example.com"],
    ["\\\\evil.example.com"],
    // The URL parser drops a tab or newline anywhere: "/<tab>/host" is "//host".
    ["/\t/evil.example.com"],
    ["/\n/evil.example.com"],
    ["/\r/evil.example.com"],
    // No control character is accepted, the parser's own list or not: NUL, the
    // last one below a space, and DEL (which the parser leaves alone, but which
    // has no place in a path someone is sent to either).
    ["/\u0000a"],
    ["/a\u001fb"],
    ["/a\u007fb"],
  ])("falls back to / for %j", (value) => {
    expect(sanitizeReturnTo(value)).toBe("/");
  });
});
