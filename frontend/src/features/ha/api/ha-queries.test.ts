import { describe, it, expect } from "vitest";
import { haResourceHasFailback, parseRetryCount } from "./ha-queries";

// A resource has a failback of its own from PVE 9.0 (pve-ha-manager 5.0.2);
// on PVE 8 sending one is a 400. An unknown version must hide it: hiding the
// switch costs a PVE 9 operator nothing they cannot set on a later visit,
// while offering it on PVE 8 fails every save that carries it.
describe("haResourceHasFailback", () => {
  it.each([
    {
      version: "",
      want: false,
      why: "an unknown version hides it, as PVE 8 does",
    },
    { version: "8.4", want: false, why: "PVE 8 has no resource failback" },
    { version: "9.0", want: true, why: "PVE 9.0 is where it arrived" },
    { version: "9.1.2", want: true, why: "later PVE 9 releases keep it" },
  ])("answers $want for $version — $why", ({ version, want }) => {
    expect(haResourceHasFailback(version)).toBe(want);
  });
});

// What a retry-count input may send. A number input hands over any valid
// floating-point spelling, exponent form included, so the value has to be
// read as a number rather than as its leading digits.
describe("parseRetryCount", () => {
  it.each([
    { text: "1e1", ceiling: 10, want: 10, why: "exponent form is 10, not 1" },
    { text: "10e-1", ceiling: 10, want: 1, why: "and 1, not 10" },
    { text: "0", ceiling: 10, want: 0, why: "0 is a real count" },
    { text: "15", ceiling: 15, want: 15, why: "a stored count above 10" },
    { text: "", ceiling: 10, want: undefined, why: "blank is not 0" },
    { text: " ", ceiling: 10, want: undefined, why: "neither is whitespace" },
    { text: "1.5", ceiling: 10, want: undefined, why: "not a whole count" },
    { text: "-1", ceiling: 10, want: undefined, why: "below the input's min" },
    { text: "11", ceiling: 10, want: undefined, why: "above its max" },
  ])(
    "reads $text (max $ceiling) as $want — $why",
    ({ text, ceiling, want }) => {
      expect(parseRetryCount(text, ceiling)).toBe(want);
    },
  );
});
