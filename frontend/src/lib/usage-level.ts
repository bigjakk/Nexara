export type UsageLevel = "ok" | "warn" | "err";

/** Amber from 75%, red from 90%: the thresholds every capacity bar shares. */
export function usageLevel(percent: number | null): UsageLevel {
  if (percent === null || percent < 75) return "ok";
  return percent < 90 ? "warn" : "err";
}

/** The text colour that goes with a level; "ok" is left to the caller. */
export const USAGE_TEXT_CLASS: Record<UsageLevel, string> = {
  ok: "",
  warn: "font-semibold text-amber-700 dark:text-amber-400",
  err: "font-semibold text-red-700 dark:text-red-400",
};
