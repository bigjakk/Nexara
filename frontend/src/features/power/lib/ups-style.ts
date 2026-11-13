import type { UpsTone } from "./ups-state";

/**
 * The StatusIcon status that draws a tone's dot. It only picks the colour:
 * the dot is named by the UPS's own label.
 */
export const TONE_DOT: Record<UpsTone, string> = {
  ok: "online",
  warn: "degraded",
  err: "offline",
  idle: "unknown",
};

/** The text colour a reading wears in a tone; "ok" is left plain. */
export const TONE_TEXT: Record<UpsTone, string> = {
  ok: "",
  warn: "text-amber-700 dark:text-amber-400",
  err: "text-red-700 dark:text-red-400",
  idle: "text-muted-foreground",
};

/**
 * An outline button for an action that cuts power: marked as dangerous
 * without being the filled button on the screen.
 */
export const DANGER_OUTLINE =
  "border-red-500/40 text-red-700 hover:bg-red-500/10 hover:text-red-700 dark:text-red-400 dark:hover:text-red-400";
