/**
 * Translation between the cron expression the API stores and the three shapes
 * the config card offers.
 *
 * The API accepts any valid five-field cron, so the card has to cope with an
 * expression it did not write — one set through the API, or one it offered in
 * an older build. Anything it cannot render as "daily" or "weekly" round-trips
 * verbatim as a custom expression rather than being silently rewritten.
 */

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

/** Default time-of-day offered when switching to a timed mode. */
const DEFAULT_TIME = "03:00";

const DAILY = /^(\d{1,2}) (\d{1,2}) \* \* \*$/;
const WEEKLY = /^(\d{1,2}) (\d{1,2}) \* \* ([0-6])$/;

function pad(n: number): string {
  return n.toString().padStart(2, "0");
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

  const daily = DAILY.exec(cron);
  if (daily?.[1] !== undefined && daily[2] !== undefined) {
    const minute = Number(daily[1]);
    const hour = Number(daily[2]);
    if (hour <= 23 && minute <= 59) {
      return { ...base, mode: "daily", time: `${pad(hour)}:${pad(minute)}` };
    }
  }

  const weekly = WEEKLY.exec(cron);
  if (
    weekly?.[1] !== undefined &&
    weekly[2] !== undefined &&
    weekly[3] !== undefined
  ) {
    const minute = Number(weekly[1]);
    const hour = Number(weekly[2]);
    if (hour <= 23 && minute <= 59) {
      return {
        ...base,
        mode: "weekly",
        time: `${pad(hour)}:${pad(minute)}`,
        weekday: Number(weekly[3]),
      };
    }
  }

  return { ...base, mode: "custom" };
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

  const [rawHour, rawMinute] = (parsed.time || DEFAULT_TIME).split(":");
  const hour = Number(rawHour);
  const minute = Number(rawMinute);
  const safeHour = Number.isInteger(hour) && hour >= 0 && hour <= 23 ? hour : 3;
  const safeMinute =
    Number.isInteger(minute) && minute >= 0 && minute <= 59 ? minute : 0;

  return parsed.mode === "daily"
    ? `${String(safeMinute)} ${String(safeHour)} * * *`
    : `${String(safeMinute)} ${String(safeHour)} * * ${String(parsed.weekday)}`;
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
  const local = localTimezone();
  try {
    const all = Intl.supportedValuesOf("timeZone");
    return all.length > 0 ? all : [local, "UTC"];
  } catch {
    return Array.from(new Set([local, "UTC"]));
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
