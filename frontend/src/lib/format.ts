import i18n from "@/lib/i18n";
import {
  usePreferencesStore,
  type DateDisplay,
} from "@/stores/preferences-store";

const BINARY_UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;
const DECIMAL_UNITS = ["B", "kB", "MB", "GB", "TB", "PB"] as const;

/**
 * Scales a byte count in the unit system chosen under Settings → Appearance:
 * binary (the default) divides by 1024 and says so — "GiB", the figures the
 * server-rendered reports print — and decimal divides by 1000 ("GB").
 */
function scaleBytes(
  bytes: number,
): { value: number; unit: string; index: number } | undefined {
  const decimal =
    usePreferencesStore.getState().preferences.byteUnit === "decimal";
  const base = decimal ? 1000 : 1024;
  const units = decimal ? DECIMAL_UNITS : BINARY_UNITS;
  // Use Math.abs in the log to support negative values (network deltas,
  // diff displays). Original sign is preserved through the division below.
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(base));
  const index = Math.min(Math.max(i, 0), units.length - 1);
  const unit = units[index];
  if (unit === undefined) return undefined;
  return { value: bytes / Math.pow(base, index), unit, index };
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const scaled = scaleBytes(bytes);
  if (scaled === undefined) return `${String(bytes)} B`;
  return `${scaled.value.toFixed(scaled.index === 0 ? 0 : 1)} ${scaled.unit}`;
}

// formatUptime renders a positive seconds value as a compact "Xd Yh" /
// "Xh Ym" / "Xm" string. Pass `fallback` (default "--") for the
// no-data / zero / negative case — most call sites want "--", a few
// want a literal "0s" or similar.
export function formatUptime(seconds: number, fallback = "--"): string {
  if (seconds <= 0) return fallback;
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);

  if (days > 0) return `${String(days)}d ${String(hours)}h`;
  if (hours > 0) return `${String(hours)}h ${String(minutes)}m`;
  return `${String(minutes)}m`;
}

export function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`;
}

export function formatBytesPerSecond(bytesPerSec: number): string {
  if (bytesPerSec === 0) return "0 B/s";
  const scaled = scaleBytes(bytesPerSec);
  if (scaled === undefined) return `${String(bytesPerSec)} B/s`;
  return `${scaled.value.toFixed(scaled.index === 0 ? 0 : 1)} ${scaled.unit}/s`;
}

/** Zero-pads to two digits, for the clock and calendar fields below. */
export function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

// ---------------------------------------------------------------------------
// Dates and times. Every date the app shows goes through here, so the Date
// format chosen under Settings → Appearance decides how it reads:
//   auto     — the default: what each place showed before the choice was
//              honoured. A place that was relative says so (`auto:
//              "relative"`, old "3m ago" wording); the rest use the locale;
//   relative — "5m ago" / "in 3h", the exact time in formatExactDateTime;
//   iso      — ISO 8601 in the browser's zone with its offset, sortable;
//   locale   — the browser's locale.
// A number is milliseconds since the epoch (pass a PVE unix time × 1000); 0
// means "never set" and reads as the fallback. lib/date-format.guard.test.ts
// fails on a date formatted anywhere else.
// ---------------------------------------------------------------------------

/** A timestamp as the API sends it: ISO-8601 text, epoch milliseconds or a Date. */
export type DateInput = string | number | Date | null | undefined;

function dateFormat(): DateDisplay {
  return usePreferencesStore.getState().preferences.dateDisplay;
}

const DATE_ONLY = /^(\d{4})-(\d{2})-(\d{2})$/;

/** The instant, null when there is none, or the raw text when it will not parse. */
function toDate(value: DateInput): Date | null | string {
  if (value == null || value === "" || value === 0) return null;
  // A bare date is a calendar day here, not UTC midnight (which reads as the
  // day before west of UTC).
  const day = typeof value === "string" ? DATE_ONLY.exec(value) : null;
  const parsed = day
    ? new Date(Number(day[1]), Number(day[2]) - 1, Number(day[3]))
    : value instanceof Date
      ? value
      : new Date(value);
  if (!Number.isNaN(parsed.getTime())) return parsed;
  // Text we cannot read is shown as sent: hiding it behind "Invalid Date"
  // helps nobody. A bad number or Date has nothing worth showing.
  return typeof value === "string" ? value : null;
}

function isoDate(d: Date): string {
  return `${String(d.getFullYear())}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}

function isoTime(d: Date): string {
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
}

function isoOffset(d: Date): string {
  const minutes = -d.getTimezoneOffset();
  const abs = Math.abs(minutes);
  return `${minutes < 0 ? "-" : "+"}${pad2(Math.floor(abs / 60))}:${pad2(abs % 60)}`;
}

let rtfCache: {
  lang: string;
  auto: Intl.RelativeTimeFormat;
  always: Intl.RelativeTimeFormat;
} | null = null;

/**
 * "now", "yesterday", "5m ago". Months and years are always numbered: the
 * "auto" wording for them ("last mo.") reads as a calendar month, not 30 days.
 */
function formatRelative(n: number, unit: Intl.RelativeTimeFormatUnit): string {
  const lang = i18n.resolvedLanguage ?? "en";
  if (rtfCache?.lang !== lang) {
    rtfCache = {
      lang,
      auto: new Intl.RelativeTimeFormat(lang, { style: "narrow", numeric: "auto" }),
      always: new Intl.RelativeTimeFormat(lang, { style: "narrow", numeric: "always" }),
    };
  }
  const rtf = unit === "month" || unit === "year" ? rtfCache.always : rtfCache.auto;
  return rtf.format(n, unit);
}

const RELATIVE_STEPS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 86_400],
  ["month", 30 * 86_400],
  ["day", 86_400],
  ["hour", 3_600],
  ["minute", 60],
];

/** "5m ago", "in 3h", "now" under a minute; to one unit, rounded toward now. */
function relativeDateTime(d: Date, now: number): string {
  const seconds = (d.getTime() - now) / 1000;
  const abs = Math.abs(seconds);
  for (const [unit, size] of RELATIVE_STEPS) {
    if (abs >= size) {
      return formatRelative(Math.trunc(seconds / size), unit);
    }
  }
  return formatRelative(0, "second");
}

/** "today", "yesterday", "in 3d", "2mo ago": counted in calendar days. */
function relativeDate(d: Date, now: number): string {
  const today = new Date(now);
  const days = Math.round(
    (Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) -
      Date.UTC(today.getFullYear(), today.getMonth(), today.getDate())) /
      86_400_000,
  );
  const abs = Math.abs(days);
  if (abs >= 365) return formatRelative(Math.trunc(days / 365), "year");
  if (abs >= 30) return formatRelative(Math.trunc(days / 30), "month");
  return formatRelative(days, "day");
}

/** What a date shows: the date and time, the date alone, or the time alone. */
export type DateKind = "datetime" | "date" | "time";

/** What a place showed before the Date format was honoured, used under auto. */
export type AutoStyle = "relative" | "clock" | "as-sent";

export interface DateOptions {
  kind?: DateKind | undefined;
  fallback?: string | undefined;
  /** Never relative: for a tooltip, or a date inside a sentence ("as of …"). */
  exact?: boolean | undefined;
  /** The place's own locale options (a long month, no seconds). */
  locale?: Intl.DateTimeFormatOptions | undefined;
  /** Under auto: "relative" where the place was relative, "clock" where it
   *  showed a fixed 24-hour clock, "as-sent" where it showed the server's
   *  text. Omitted: the locale. */
  auto?: AutoStyle | undefined;
  /** A narrow cell: an absolute value under iso or locale reads as the clock
   *  for today and the date otherwise (DateTime puts the exact time beside). */
  compact?: boolean | undefined;
}

function isToday(d: Date, now: number): boolean {
  return isoDate(d) === isoDate(new Date(now));
}

function render(d: Date, o: DateOptions): string {
  const kind = o.kind ?? "datetime";
  const format = dateFormat();
  const now = Date.now();
  if (format === "auto") {
    if (o.auto === "relative" && kind !== "time") {
      return formatRelativeTime(d.toISOString());
    }
    if (o.auto === "clock") return isoTime(d);
  }
  const compact = o.compact === true && kind === "datetime" && format !== "auto";
  if (format === "iso") {
    if (kind === "date" || (compact && !isToday(d, now))) return isoDate(d);
    if (compact) return isoTime(d).slice(0, 5);
    if (kind === "time") return isoTime(d);
    return `${isoDate(d)}T${isoTime(d)}${isoOffset(d)}`;
  }
  // A clock reading is never relative: "since 5m ago" says less than the time.
  if (format === "relative" && kind !== "time" && o.exact !== true) {
    return kind === "date" ? relativeDate(d, now) : relativeDateTime(d, now);
  }
  if (compact) {
    return isToday(d, now)
      ? d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
      : d.toLocaleDateString();
  }
  if (kind === "date") return d.toLocaleDateString(undefined, o.locale);
  if (kind === "time") return d.toLocaleTimeString(undefined, o.locale);
  return d.toLocaleString(undefined, o.locale);
}

/** A date under the Appearance preference, or `fallback` when there is none. */
export function formatDateValue(value: DateInput, options: DateOptions = {}): string {
  const d = toDate(value);
  if (d === null) return options.fallback ?? "—";
  if (typeof d === "string") return d;
  if (
    options.auto === "as-sent" &&
    typeof value === "string" &&
    dateFormat() === "auto"
  ) {
    return value;
  }
  return render(d, options);
}

/**
 * What DateTime renders: the text, the exact time when the text is not it
 * (relative, compact), and the instant for <time dateTime>.
 */
export function describeDate(
  value: DateInput,
  options: DateOptions = {},
): { text: string; exact: string; iso: string } {
  const text = formatDateValue(value, options);
  const d = toDate(value);
  if (!(d instanceof Date)) return { text, exact: "", iso: "" };
  const exact = formatDateValue(d, {
    kind: options.kind,
    locale: options.locale,
    exact: true,
  });
  return { text, exact: exact === text ? "" : exact, iso: d.toISOString() };
}

/**
 * A timestamp under the Date format preference.
 *
 * `fallback` covers the missing case — call sites disagree on what "no value
 * yet" should read as ("Never" for a sync that has not run, an em dash for a
 * run still in flight), so it is a parameter rather than a constant.
 *
 * A text value that will not parse is returned verbatim: if the server sent
 * something we cannot read, showing it beats hiding it behind "Invalid Date".
 */
export function formatDateTime(
  value: DateInput,
  fallback = "—",
  auto?: AutoStyle,
): string {
  return formatDateValue(value, { fallback, auto });
}

/**
 * The exact date and time, never relative: a hover title, a chart tooltip, a
 * date inside a sentence. Under auto, `auto` keeps what the place showed before.
 */
export function formatExactDateTime(
  value: DateInput,
  fallback = "—",
  auto?: AutoStyle,
): string {
  return formatDateValue(value, { fallback, exact: true, auto });
}

/** The date alone under the preference ("today", "2026-03-08", "3/8/2026"). */
export function formatDate(value: DateInput, fallback = "—"): string {
  return formatDateValue(value, { kind: "date", fallback });
}

/** The date alone, never relative: for "full around …", "the newest on …". */
export function formatExactDate(value: DateInput, fallback = "—"): string {
  return formatDateValue(value, { kind: "date", fallback, exact: true });
}

/** The time of day alone; never relative ("on battery since 14:03:22"). */
export function formatTime(value: DateInput, fallback = "—"): string {
  return formatDateValue(value, { kind: "time", fallback });
}

/**
 * Compact chart axis labels. A tick is never relative, and keeps the fixed
 * 24-hour reading it has always had; under iso a day is written month-day.
 */
export function formatTimestamp(ts: number): string {
  return isoTime(new Date(ts));
}

export function formatTimestampShort(ts: number): string {
  const date = new Date(ts);
  return `${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
}

export function formatTimestampLong(ts: number): string {
  const date = new Date(ts);
  const mon = pad2(date.getMonth() + 1);
  const day = pad2(date.getDate());
  const sep = dateFormat() === "iso" ? "-" : "/";
  return `${mon}${sep}${day} ${formatTimestampShort(ts)}`;
}

// Built once each: constructing an Intl formatter dwarfs the cost of using
// one, and an axis formatter runs per tick.
const AXIS_CLOCK = new Intl.DateTimeFormat(undefined, {
  hour: "2-digit",
  minute: "2-digit",
});
const AXIS_DAY = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
});
const AXIS_DAY_CLOCK = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

/** An axis tick's clock reading in the locale ("14:30", "02:30 PM"); iso "14:30". */
export function formatAxisClock(ts: number): string {
  if (!Number.isFinite(ts)) return "";
  return dateFormat() === "iso" ? formatTimestampShort(ts) : AXIS_CLOCK.format(ts);
}

/** An axis tick's day in the locale ("Mar 8"); iso "03-08". */
export function formatAxisDay(ts: number): string {
  if (!Number.isFinite(ts)) return "";
  if (dateFormat() !== "iso") return AXIS_DAY.format(ts);
  const d = new Date(ts);
  return `${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}

/** An axis tick's day and clock in the locale ("Mar 8, 14:30"); iso "03-08 14:30". */
export function formatAxisDayClock(ts: number): string {
  if (!Number.isFinite(ts)) return "";
  if (dateFormat() !== "iso") return AXIS_DAY_CLOCK.format(ts);
  return `${formatAxisDay(ts)} ${formatTimestampShort(ts)}`;
}

/**
 * How long ago an ISO-8601 timestamp was, to one unit ("3m ago", "2d ago"),
 * whatever the preference. Kept for its callers' tests; display goes through
 * formatDateTime, which is relative only when the person chose it.
 */
export function formatRelativeTime(iso: string): string {
  const ago = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (ago < 60) return `${String(ago)}s ago`;
  if (ago < 3600) return `${String(Math.floor(ago / 60))}m ago`;
  if (ago < 86400) return `${String(Math.floor(ago / 3600))}h ago`;
  return `${String(Math.floor(ago / 86400))}d ago`;
}
