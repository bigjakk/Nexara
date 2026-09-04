// Helpers for the systemd calendar-event strings PVE uses as vzdump job
// schedules: "02:00" (daily), "mon,fri 22:30" (weekly), "*<slash>6:00" (every
// six hours), "*-*-01 04:00" (monthly). The dialog drives a small structured
// spec instead of raw text; these functions convert between the two so an
// existing job can be reopened in the builder it was created with.

import { pad2 } from "@/lib/format";

export type ScheduleFrequency =
  | "hourly"
  | "daily"
  | "weekly"
  | "monthly"
  | "custom";

export interface ScheduleSpec {
  frequency: ScheduleFrequency;
  /** Hourly: run every N hours. */
  everyHours: number;
  /** Daily/weekly/monthly hour of day (0-23). */
  hour: number;
  /** Minute past the hour (0-59). */
  minute: number;
  /** Weekly: systemd day abbreviations, e.g. ["mon", "fri"]. */
  weekdays: string[];
  /** Monthly: day of month (1-31). */
  dayOfMonth: number;
  /** Custom: the raw calendar string, passed through untouched. */
  custom: string;
}

export const WEEKDAYS = [
  { value: "mon", short: "Mon", long: "Monday" },
  { value: "tue", short: "Tue", long: "Tuesday" },
  { value: "wed", short: "Wed", long: "Wednesday" },
  { value: "thu", short: "Thu", long: "Thursday" },
  { value: "fri", short: "Fri", long: "Friday" },
  { value: "sat", short: "Sat", long: "Saturday" },
  { value: "sun", short: "Sun", long: "Sunday" },
] as const;

const WEEKDAY_VALUES = WEEKDAYS.map((d) => d.value as string);

export const DEFAULT_SCHEDULE: ScheduleSpec = {
  frequency: "daily",
  everyHours: 6,
  hour: 2,
  minute: 0,
  weekdays: ["sun"],
  dayOfMonth: 1,
  custom: "",
};

/** Sorts weekdays into Mon..Sun order and drops anything unrecognised. */
export function sortWeekdays(days: string[]): string[] {
  return WEEKDAY_VALUES.filter((d) => days.includes(d));
}

/** Builds the PVE calendar string for a spec. Returns "" when incomplete. */
export function formatSchedule(spec: ScheduleSpec): string {
  const time = `${pad2(spec.hour)}:${pad2(spec.minute)}`;
  switch (spec.frequency) {
    case "hourly":
      return spec.everyHours > 1
        ? `*/${String(spec.everyHours)}:${pad2(spec.minute)}`
        : `*:${pad2(spec.minute)}`;
    case "daily":
      return time;
    case "weekly": {
      const days = sortWeekdays(spec.weekdays);
      if (days.length === 0) return "";
      return `${days.join(",")} ${time}`;
    }
    case "monthly":
      return `*-*-${pad2(spec.dayOfMonth)} ${time}`;
    case "custom":
      return spec.custom.trim();
  }
}

function parseTime(value: string): { hour: number; minute: number } | null {
  const m = /^(\d{1,2}):(\d{2})$/.exec(value);
  if (!m) return null;
  const [, rawHour = "", rawMinute = ""] = m;
  const hour = Number(rawHour);
  const minute = Number(rawMinute);
  if (hour > 23 || minute > 59) return null;
  return { hour, minute };
}

/** Expands "mon..fri" style ranges; returns null if the token isn't a day list. */
function parseWeekdayList(value: string): string[] | null {
  const days = new Set<string>();
  for (const part of value.split(",")) {
    const range = /^([a-z]{3})\.\.([a-z]{3})$/.exec(part);
    if (range) {
      const from = WEEKDAY_VALUES.indexOf(range[1] ?? "");
      const to = WEEKDAY_VALUES.indexOf(range[2] ?? "");
      if (from === -1 || to === -1 || from > to) return null;
      for (const day of WEEKDAY_VALUES.slice(from, to + 1)) days.add(day);
      continue;
    }
    if (!WEEKDAY_VALUES.includes(part)) return null;
    days.add(part);
  }
  return days.size > 0 ? sortWeekdays([...days]) : null;
}

/**
 * Best-effort inverse of formatSchedule. Anything the builder can't express
 * comes back as a "custom" spec holding the original string, so reopening a
 * hand-written schedule never silently rewrites it.
 */
export function parseSchedule(raw: string | undefined | null): ScheduleSpec {
  const value = (raw ?? "").trim().toLowerCase();
  if (value === "") return { ...DEFAULT_SCHEDULE };

  const custom = (): ScheduleSpec => ({
    ...DEFAULT_SCHEDULE,
    frequency: "custom",
    custom: (raw ?? "").trim(),
  });

  // systemd shorthands PVE accepts verbatim.
  switch (value) {
    case "hourly":
      return {
        ...DEFAULT_SCHEDULE,
        frequency: "hourly",
        everyHours: 1,
        minute: 0,
      };
    case "daily":
      return { ...DEFAULT_SCHEDULE, frequency: "daily", hour: 0, minute: 0 };
    case "weekly":
      return {
        ...DEFAULT_SCHEDULE,
        frequency: "weekly",
        weekdays: ["mon"],
        hour: 0,
        minute: 0,
      };
    case "monthly":
      return {
        ...DEFAULT_SCHEDULE,
        frequency: "monthly",
        dayOfMonth: 1,
        hour: 0,
        minute: 0,
      };
  }

  // Every N hours: "*/6:00", or every hour: "*:30".
  const hourly = /^\*(?:\/(\d{1,2}))?:(\d{2})$/.exec(value);
  if (hourly) {
    const everyHours = hourly[1] ? Number(hourly[1]) : 1;
    const minute = Number(hourly[2] ?? "");
    if (everyHours >= 1 && everyHours <= 23 && minute <= 59) {
      return { ...DEFAULT_SCHEDULE, frequency: "hourly", everyHours, minute };
    }
    return custom();
  }

  const parts = value.split(/\s+/);
  const [head = "", tail = ""] = parts;

  // Daily: bare "HH:MM".
  if (parts.length === 1) {
    const time = parseTime(head);
    return time
      ? { ...DEFAULT_SCHEDULE, frequency: "daily", ...time }
      : custom();
  }

  if (parts.length !== 2) return custom();
  const time = parseTime(tail);
  if (!time) return custom();

  // Monthly: "*-*-01 02:00".
  const monthly = /^\*-\*-(\d{1,2})$/.exec(head);
  if (monthly) {
    const dayOfMonth = Number(monthly[1] ?? "");
    if (dayOfMonth >= 1 && dayOfMonth <= 31) {
      return { ...DEFAULT_SCHEDULE, frequency: "monthly", dayOfMonth, ...time };
    }
    return custom();
  }

  // Weekly: "mon,fri 22:30" or "mon..fri 22:30".
  const weekdays = parseWeekdayList(head);
  if (weekdays) {
    return { ...DEFAULT_SCHEDULE, frequency: "weekly", weekdays, ...time };
  }

  return custom();
}

/** 1 -> "1st", 2 -> "2nd", 11 -> "11th", 21 -> "21st". */
export function ordinal(n: number): string {
  const suffix =
    n % 100 >= 11 && n % 100 <= 13
      ? "th"
      : (["th", "st", "nd", "rd"][n % 10] ?? "th");
  return `${String(n)}${suffix}`;
}

function joinLabels(labels: string[]): string {
  if (labels.length <= 1) return labels.join("");
  const last = labels[labels.length - 1] ?? "";
  return `${labels.slice(0, -1).join(", ")} and ${last}`;
}

/**
 * Plain-English rendering of a calendar string, or null when the string is
 * beyond the builder's vocabulary (callers show the raw string instead).
 */
export function describeSchedule(
  raw: string | undefined | null,
): string | null {
  if (!raw || raw.trim() === "") return null;
  const spec = parseSchedule(raw);
  const time = `${pad2(spec.hour)}:${pad2(spec.minute)}`;
  switch (spec.frequency) {
    case "hourly":
      if (spec.everyHours === 1) {
        return spec.minute === 0
          ? "Every hour, on the hour"
          : `Every hour at :${pad2(spec.minute)}`;
      }
      return `Every ${String(spec.everyHours)} hours at :${pad2(spec.minute)}`;
    case "daily":
      return `Every day at ${time}`;
    case "weekly": {
      const days = sortWeekdays(spec.weekdays);
      if (days.length === 7) return `Every day at ${time}`;
      const labels = days.map(
        (d) =>
          WEEKDAYS.find((w) => w.value === d)?.[
            days.length === 1 ? "long" : "short"
          ] ?? d,
      );
      return `Every ${joinLabels(labels)} at ${time}`;
    }
    case "monthly":
      return `On the ${ordinal(spec.dayOfMonth)} of each month at ${time}`;
    case "custom":
      return null;
  }
}
