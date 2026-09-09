import type { UserSession } from "@/types/api";

/**
 * Browser brands, most specific first — the order is load bearing. Edge and
 * Opera both carry "Chrome" in their UA and Chrome carries "Safari", so a
 * less specific match placed earlier would label nearly every session
 * "Safari" and the list would stop distinguishing devices at all. Chromium
 * Edge is matched on "Edg" because that is the token it actually ships.
 */
const BROWSERS: readonly (readonly [RegExp, string])[] = [
  [/Edg\//, "Edge"],
  [/OPR\//, "Opera"],
  [/Firefox\//, "Firefox"],
  [/Chrome\//, "Chrome"],
  [/Safari\//, "Safari"],
];

/**
 * Platforms, most specific first. Android must precede Linux: an Android UA
 * reads "Linux; Android 14", so testing Linux first labels every phone a
 * Linux desktop.
 */
const PLATFORMS: readonly (readonly [RegExp, string])[] = [
  [/Windows/, "Windows"],
  [/Android/, "Android"],
  [/iPhone|iPad|iPod/, "iOS"],
  [/Mac OS X/, "macOS"],
  [/Linux/, "Linux"],
];

/** Leading product token of a UA — the "curl" in "curl/8.5.0". */
const PRODUCT = /^([A-Za-z][A-Za-z0-9._-]{0,31})\//;

/**
 * A readable name for a session, for the active-sessions list.
 *
 * Prefers the device name a non-browser client supplied via the
 * X-Nexara-Device-* headers. Browsers send none of those, so the fallback
 * reads the browser and platform out of the User-Agent — enough to tell two
 * sessions apart, which is the only job here.
 *
 * Deliberately not a UA-parsing dependency: getting this subtly wrong costs a
 * slightly vague label, while every UA database needs perpetual updating to
 * stay accurate, and that is not maintenance worth taking on for a label.
 */
export function sessionLabel(session: UserSession): string {
  if (session.device_name) return session.device_name;

  const ua = session.user_agent;
  if (!ua) return "Unknown device";

  const browser = BROWSERS.find(([re]) => re.test(ua))?.[1];
  if (!browser) {
    // Not a browser we recognise, so name the client after its own product
    // token — "curl/8.5.0" reads as "curl". Calling it "Browser" was worse
    // than vague: three curl sessions rendered as three identical "Browser"
    // rows, in a list that exists so you can tell apart what you do not
    // recognise. "Mozilla" is excluded because every browser UA opens with
    // it, so it names nothing.
    const product = PRODUCT.exec(ua)?.[1];
    return product && product !== "Mozilla" ? product : "Unknown client";
  }

  const platform = PLATFORMS.find(([re]) => re.test(ua))?.[1];
  return platform ? `${browser} on ${platform}` : browser;
}
