/**
 * The accent a user picks in Settings › Appearance. "default" picks none and
 * leaves the theme alone: ink buttons and marks, blue links and charts.
 *
 * A picked accent colours the one primary button, the "you are here" marks
 * (the rail's active item, the active tab, the selected figure of a stat
 * strip), links, toggles and focus rings, and measured data: charts, bars,
 * sparklines. It never colours --info, the "under way" state: a running task
 * wearing a green or red accent would read as done or failed.
 */
export const ACCENT_COLORS = [
  { name: "blue", labelKey: "colorBlue", hsl: "221 83% 53%" },
  { name: "green", labelKey: "colorGreen", hsl: "142 71% 45%" },
  { name: "purple", labelKey: "colorPurple", hsl: "262 83% 58%" },
  { name: "orange", labelKey: "colorOrange", hsl: "25 95% 53%" },
  { name: "red", labelKey: "colorRed", hsl: "0 72% 51%" },
  { name: "pink", labelKey: "colorPink", hsl: "330 81% 60%" },
  { name: "teal", labelKey: "colorTeal", hsl: "173 80% 40%" },
  { name: "cyan", labelKey: "colorCyan", hsl: "189 94% 43%" },
  { name: "amber", labelKey: "colorAmber", hsl: "38 92% 50%" },
] as const;

/**
 * Accents that are also state colours. Under one of these, measured data
 * keeps the stock blue: a usage bar turns amber and red at its thresholds,
 * and beside a green "OK" series a green chart line would mean nothing.
 */
const STATE_HUES: ReadonlySet<string> = new Set([
  "green",
  "orange",
  "red",
  "amber",
]);

const ACCENT_TOKENS = [
  "--primary",
  "--ring",
  "--marker",
  "--data",
  "--action",
  "--action-foreground",
  "--rail-marker",
] as const;

type AccentToken = (typeof ACCENT_TOKENS)[number];
/** Every token but --data, which a state-coloured accent leaves alone. */
export type AccentTokens = Record<Exclude<AccentToken, "--data">, string> & {
  "--data"?: string;
};

/**
 * Surfaces from index.css. Accent text has to stay readable on each theme's
 * hardest one: the light theme's canvas (--background), the dark theme's
 * panel (--card). The rail (--rail) is dark in both.
 */
export const ACCENT_SURFACES = {
  light: "220 19% 94%",
  dark: "216 14% 14%",
  rail: "216 14% 7%",
} as const;
const WHITE = "0 0% 100%";
const INK = "214 15% 9%";

/**
 * What accent text is tuned to on those surfaces. Above WCAG AA (4.5) on
 * purpose: it is what the theme's own link blue reaches, and it leaves AA on
 * a hovered row and on a badge tinted with the accent itself.
 */
const TEXT_CONTRAST = 5.5;
/** A mark on the rail is an icon and a rule, not text to read. */
const RAIL_CONTRAST = 4.5;

type Hsl = readonly [hue: number, saturation: number, lightness: number];

function parse(hsl: string): Hsl {
  const [h = 0, s = 0, l = 0] = hsl.split(" ").map((part) => parseFloat(part));
  return [h, s, l];
}

function luminance([h, s, l]: Hsl): number {
  const light = l / 100;
  const chroma = (s / 100) * Math.min(light, 1 - light);
  const channel = (n: number) => {
    const k = (n + h / 30) % 12;
    const c = light - chroma * Math.max(-1, Math.min(k - 3, 9 - k, 1));
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(0) + 0.7152 * channel(8) + 0.0722 * channel(4);
}

/** WCAG contrast ratio between two "H S% L%" colours. */
export function contrast(a: string, b: string): number {
  const la = luminance(parse(a));
  const lb = luminance(parse(b));
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

/** The colour, darkened or lightened just far enough to read on a surface. */
function readableOn(
  color: string,
  surface: string,
  step: 1 | -1,
  target = TEXT_CONTRAST,
): string {
  const [h, s, start] = parse(color);
  const shade = (l: number) => `${String(h)} ${String(s)}% ${String(l)}%`;
  let l = start;
  while (l > 0 && l < 100 && contrast(shade(l), surface) < target) l += step;
  return shade(l);
}

/** The theme tokens a picked accent overrides; null for "default". */
export function accentTokens(
  name: string,
  isDark: boolean,
): AccentTokens | null {
  const accent = ACCENT_COLORS.find((color) => color.name === name);
  if (!accent) return null;
  // Text and marks take a shade that reads on the page. The button keeps the
  // colour as picked and takes whichever label reads on it.
  const text = isDark
    ? readableOn(accent.hsl, ACCENT_SURFACES.dark, 1)
    : readableOn(accent.hsl, ACCENT_SURFACES.light, -1);
  return {
    "--primary": text,
    "--ring": text,
    "--marker": text,
    ...(STATE_HUES.has(accent.name) ? {} : { "--data": text }),
    "--action": accent.hsl,
    "--action-foreground":
      contrast(accent.hsl, WHITE) >= contrast(accent.hsl, INK) ? WHITE : INK,
    "--rail-marker": readableOn(
      accent.hsl,
      ACCENT_SURFACES.rail,
      1,
      RAIL_CONTRAST,
    ),
  };
}

/** Applies a picked accent to the document, or clears it for "default". */
export function applyAccentColor(name: string): void {
  const root = document.documentElement;
  const tokens = accentTokens(name, root.classList.contains("dark"));
  for (const token of ACCENT_TOKENS) {
    const value = tokens?.[token];
    if (value === undefined) root.style.removeProperty(token);
    else root.style.setProperty(token, value);
  }
}
