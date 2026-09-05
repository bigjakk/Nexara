/**
 * Translation between the cron expression the API stores and the three shapes
 * the config card offers.
 *
 * The API accepts any valid five-field cron, so the card has to cope with an
 * expression it did not write — one set through the API, or one it offered in
 * an older build. Anything it cannot render as "daily" or "weekly" round-trips
 * verbatim as a custom expression rather than being silently rewritten.
 */

import { pad2 } from "@/lib/format";

export type ScheduleMode = "interval" | "daily" | "weekly" | "custom";

export interface ParsedSchedule {
  mode: ScheduleMode;
  /** "HH:MM", for the daily and weekly modes. */
  time: string;
  /** 0 = Sunday, matching cron's day-of-week field. */
  weekday: number;
  /** The raw expression, kept so a custom one survives a save untouched. */
  cron: string;
}

/**
 * Default time-of-day offered when switching to a timed mode. Held as the
 * parts `buildSchedule` emits and rendered into the "HH:MM" the field shows,
 * so 03:00 is written down once.
 */
const DEFAULT_FIELDS = { hour: 3, minute: 0 };
const DEFAULT_TIME = `${pad2(DEFAULT_FIELDS.hour)}:${pad2(DEFAULT_FIELDS.minute)}`;

/**
 * A timed expression: minute, hour, and a day-of-week that is either a literal
 * day (weekly) or `*` (daily). One pattern rather than two, because those are
 * the same expression differing in one field.
 */
const TIMED = /^(\d{1,2}) (\d{1,2}) \* \* (\*|[0-6])$/;

/** Reads "HH:MM" into its parts, or null when either field is out of range. */
function readTime(time: string): { hour: number; minute: number } | null {
  const [rawHour, rawMinute] = time.split(":");
  const hour = Number(rawHour);
  const minute = Number(rawMinute);
  if (!Number.isInteger(hour) || hour < 0 || hour > 23) return null;
  if (!Number.isInteger(minute) || minute < 0 || minute > 59) return null;
  return { hour, minute };
}

/** Reads a stored expression into the fields the card renders. */
export function parseSchedule(cron: string): ParsedSchedule {
  const base: ParsedSchedule = {
    mode: "interval",
    time: DEFAULT_TIME,
    weekday: 0,
    cron,
  };
  if (cron.trim() === "") return base;

  const fields = TIMED.exec(cron);
  const day = fields?.[3];
  const time =
    fields === null ? null : readTime(`${fields[2] ?? ""}:${fields[1] ?? ""}`);
  if (time === null || day === undefined) return { ...base, mode: "custom" };

  const timed = { ...base, time: `${pad2(time.hour)}:${pad2(time.minute)}` };
  return day === "*"
    ? { ...timed, mode: "daily" }
    : { ...timed, mode: "weekly", weekday: Number(day) };
}

/**
 * Builds the expression to save. Returns "" for the interval mode, which is
 * what the API reads as "every six hours".
 *
 * An unparseable time falls back to the default rather than emitting a broken
 * expression: `<input type="time">` can read empty while the field is being
 * edited, and the API rejects a malformed cron outright.
 */
export function buildSchedule(parsed: ParsedSchedule): string {
  if (parsed.mode === "interval") return "";
  if (parsed.mode === "custom") return parsed.cron;

  const { hour, minute } = readTime(parsed.time) ?? DEFAULT_FIELDS;
  return parsed.mode === "daily"
    ? `${String(minute)} ${String(hour)} * * *`
    : `${String(minute)} ${String(hour)} * * ${String(parsed.weekday)}`;
}

export const WEEKDAY_LABELS = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
];

/**
 * The IANA zones this browser knows, for the zone picker.
 *
 * `Intl.supportedValuesOf` is ES2022 and absent in older engines, so the
 * fallback is the two zones that always matter: the viewer's own, and UTC —
 * which is what a container reports when the field is left empty.
 */
export function listTimezones(): string[] {
  // Deduped, because the viewer's own zone IS "UTC" inside a container and a
  // repeated entry would collide on its React key in the picker.
  const fallback = Array.from(new Set([localTimezone(), "UTC"]));
  try {
    const all = Intl.supportedValuesOf("timeZone");
    return all.length > 0 ? all : fallback;
  } catch {
    return fallback;
  }
}

/** The viewer's own zone, used to prefill rather than to assume. */
export function localTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}
