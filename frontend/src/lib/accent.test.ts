import { afterEach, describe, expect, it } from "vitest";
import {
  ACCENT_COLORS,
  ACCENT_SURFACES,
  accentTokens,
  applyAccentColor,
  contrast,
} from "./accent";

// --muted in the dark theme: a hovered row, lighter than the panel under it.
const DARK_MUTED = "217 14% 18%";
const themes = [
  { theme: "light", isDark: false, surfaces: [ACCENT_SURFACES.light] },
  { theme: "dark", isDark: true, surfaces: [ACCENT_SURFACES.dark, DARK_MUTED] },
];
const cases = themes.flatMap((t) =>
  ACCENT_COLORS.map((accent) => ({ ...t, name: accent.name })),
);

describe("contrast", () => {
  // Anchors from the WCAG definition, so the tables below are not measured
  // with a ruler that is itself wrong.
  it.each([
    ["0 0% 0%", 21],
    ["0 100% 50%", 4.0],
    ["120 100% 50%", 1.37],
    ["240 100% 50%", 8.59],
  ])("puts white on %s at %s:1", (color, ratio) => {
    expect(contrast("0 0% 100%", color)).toBeCloseTo(ratio, 1);
  });
});

describe("accentTokens", () => {
  it.each(cases)(
    "$name reads as text, on its button and on the rail in the $theme theme",
    ({ name, isDark, surfaces }) => {
      const tokens = accentTokens(name, isDark);
      if (!tokens) throw new Error("no tokens");
      for (const surface of surfaces) {
        for (const token of ["--primary", "--marker"] as const) {
          expect(contrast(tokens[token], surface)).toBeGreaterThanOrEqual(4.5);
        }
      }
      expect(
        contrast(tokens["--action-foreground"], tokens["--action"]),
      ).toBeGreaterThanOrEqual(4.5);
      expect(
        contrast(tokens["--rail-marker"], ACCENT_SURFACES.rail),
      ).toBeGreaterThanOrEqual(4.5);
    },
  );

  it("overrides nothing for the default accent", () => {
    expect(accentTokens("default", false)).toBeNull();
  });
});

describe("applyAccentColor", () => {
  const style = document.documentElement.style;
  afterEach(() => {
    applyAccentColor("default");
  });

  it("sets the accent's tokens on the document and clears them for default", () => {
    applyAccentColor("purple");
    expect(style.getPropertyValue("--action")).toBe("262 83% 58%");
    expect(style.getPropertyValue("--rail-marker")).not.toBe("");

    applyAccentColor("default");
    expect(style.getPropertyValue("--action")).toBe("");
    expect(style.getPropertyValue("--primary")).toBe("");
  });
});
