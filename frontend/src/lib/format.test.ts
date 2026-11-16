import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";

import {
  usePreferencesStore,
  type DateDisplay,
} from "@/stores/preferences-store";
import {
  formatAxisDay,
  formatBytes,
  formatBytesPerSecond,
  formatDate,
  formatDateTime,
  formatDateValue,
  formatExactDate,
  formatExactDateTime,
  formatPercent,
  formatRelativeTime,
  formatTime,
  formatTimestampLong,
  formatUptime,
  pad2,
  type DateInput,
} from "./format";

describe("formatBytes", () => {
  afterEach(() => {
    usePreferencesStore.getState().setPreferences({ byteUnit: "binary" });
  });

  it("returns 0 B for zero", () => {
    expect(formatBytes(0)).toBe("0 B");
  });

  it("formats integer bytes without decimals", () => {
    expect(formatBytes(512)).toBe("512 B");
  });

  // Binary is the default: divided by 1024 and labelled so, as the reports are.
  it.each([
    [1024, "1.0 KiB"],
    [2048, "2.0 KiB"],
    [1024 ** 3, "1.0 GiB"],
    [1024 ** 3 * 1.5, "1.5 GiB"],
    [1024 ** 6, "1024.0 PiB"],
    [-1024, "-1.0 KiB"],
    [-1, "-1 B"],
  ])("formats %d bytes as %s", (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want);
  });

  // The Appearance preference's decimal choice divides by 1000.
  it.each([
    [1000, "1.0 kB"],
    [1024 ** 3, "1.1 GB"],
    [1e15, "1.0 PB"],
  ])("formats %d bytes as %s under the decimal preference", (bytes, want) => {
    usePreferencesStore.getState().setPreferences({ byteUnit: "decimal" });
    expect(formatBytes(bytes)).toBe(want);
  });
});

describe("formatBytesPerSecond", () => {
  afterEach(() => {
    usePreferencesStore.getState().setPreferences({ byteUnit: "binary" });
  });

  it("returns 0 B/s for zero", () => {
    expect(formatBytesPerSecond(0)).toBe("0 B/s");
  });

  it.each([
    ["binary", 1024, "1.0 KiB/s"],
    ["binary", -2048, "-2.0 KiB/s"],
    ["decimal", 2_500_000, "2.5 MB/s"],
  ] as const)("formats %s %d as %s", (byteUnit, rate, want) => {
    usePreferencesStore.getState().setPreferences({ byteUnit });
    expect(formatBytesPerSecond(rate)).toBe(want);
  });
});

describe("formatUptime", () => {
  it("returns -- by default for non-positive input", () => {
    expect(formatUptime(0)).toBe("--");
    expect(formatUptime(-1)).toBe("--");
  });

  it("honors custom fallback for non-positive input", () => {
    expect(formatUptime(0, "0s")).toBe("0s");
    expect(formatUptime(-100, "n/a")).toBe("n/a");
  });

  it("formats minutes-only", () => {
    expect(formatUptime(60)).toBe("1m");
    expect(formatUptime(59)).toBe("0m");
  });

  it("formats hours+minutes", () => {
    expect(formatUptime(3661)).toBe("1h 1m");
  });

  it("formats days+hours", () => {
    expect(formatUptime(86400 * 2 + 3600 * 5)).toBe("2d 5h");
  });
});

describe("formatPercent", () => {
  it("renders one decimal", () => {
    expect(formatPercent(75)).toBe("75.0%");
    expect(formatPercent(33.333)).toBe("33.3%");
  });
});

describe("dates under the Date format preference", () => {
  // A fixed "now"; iso expectations are built from this machine's own zone.
  const NOW = new Date("2026-09-03T12:00:00Z");
  const at = (msFromNow: number) => new Date(NOW.getTime() + msFromNow);
  const offset = (d: Date) => {
    const m = -d.getTimezoneOffset();
    const a = Math.abs(m);
    return `${m < 0 ? "-" : "+"}${pad2(Math.floor(a / 60))}:${pad2(a % 60)}`;
  };
  const isoDate = (d: Date) =>
    `${String(d.getFullYear())}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
  const isoTime = (d: Date) =>
    `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
  const HOUR = 3_600_000;
  const DAY = 24 * HOUR;
  const past = at(-5 * 60_000);
  const farPast = new Date("2001-01-02T03:04:05Z");

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
    usePreferencesStore.getState().setPreferences({ dateDisplay: "auto" });
  });

  it.each<[DateDisplay, DateInput, string]>([
    ["relative", past.toISOString(), "5m ago"],
    ["relative", past.getTime(), "5m ago"],
    ["relative", at(-30_000), "now"],
    ["relative", at(3 * HOUR + 60_000).toISOString(), "in 3h"],
    ["relative", at(-2 * DAY).toISOString(), "2d ago"],
    ["relative", farPast.toISOString(), "25y ago"],
    ["iso", past.toISOString(), `${isoDate(past)}T${isoTime(past)}${offset(past)}`],
    ["iso", farPast, `${isoDate(farPast)}T${isoTime(farPast)}${offset(farPast)}`],
    ["locale", past.toISOString(), past.toLocaleString()],
    ["locale", farPast.getTime(), farPast.toLocaleString()],
    // Automatic, the default: the locale, as a place showed it before.
    ["auto", past.toISOString(), past.toLocaleString()],
  ])("formatDateTime under %s: %s → %s", (dateDisplay, value, want) => {
    usePreferencesStore.getState().setPreferences({ dateDisplay });
    expect(formatDateTime(value)).toBe(want);
  });

  const today = NOW;
  const earlier = at(-2 * DAY);
  it.each<[DateDisplay, (v: DateInput) => string, DateInput, string]>([
    // Automatic: a place that was relative keeps the old wording (its "auto"
    // argument), a fixed clock stays one, the server's text stays as sent.
    ["auto", (v) => formatDateTime(v, "—", "relative"), at(-30_000), "30s ago"],
    ["auto", (v) => formatDateTime(v, "—", "relative"), at(-2 * DAY), "2d ago"],
    // A bare date under the old relative wording counts calendar days.
    ["auto", (v) => formatDateTime(v, "—", "relative"), isoDate(NOW), "today"],
    ["auto", (v) => formatExactDateTime(v, "—", "relative"), past, "5m ago"],
    ["auto", (v) => formatExactDateTime(v, "—", "clock"), past, isoTime(past)],
    ["auto", (v) => formatDateValue(v, { exact: true, auto: "as-sent" }), "2026-09-03T11:55:00Z", "2026-09-03T11:55:00Z"],
    ["auto", formatDate, farPast, farPast.toLocaleDateString()],
    ["relative", (v) => formatDateTime(v, "—", "relative"), at(-30_000), "now"],
    ["iso", (v) => formatDateValue(v, { auto: "as-sent" }), past.toISOString(), `${isoDate(past)}T${isoTime(past)}${offset(past)}`],
    // A narrow cell: the clock today, the date otherwise; Automatic keeps the place's form.
    ["iso", (v) => formatDateValue(v, { compact: true }), today, isoTime(today).slice(0, 5)],
    ["iso", (v) => formatDateValue(v, { compact: true }), earlier, isoDate(earlier)],
    ["locale", (v) => formatDateValue(v, { compact: true }), earlier, earlier.toLocaleDateString()],
    ["auto", (v) => formatDateValue(v, { compact: true }), earlier, earlier.toLocaleString()],
    // A date alone stays a date alone, counted in calendar days.
    ["relative", formatDate, NOW, "today"],
    ["relative", formatDate, at(-DAY), "yesterday"],
    ["relative", formatDate, at(40 * DAY), "in 1mo"],
    ["iso", formatDate, farPast, isoDate(farPast)],
    ["locale", formatDate, farPast, farPast.toLocaleDateString()],
    // A clock reading and an exact time are never relative.
    ["relative", formatTime, past, past.toLocaleTimeString()],
    ["iso", formatTime, past, isoTime(past)],
    ["relative", formatExactDateTime, past, past.toLocaleString()],
    ["iso", formatExactDateTime, past, `${isoDate(past)}T${isoTime(past)}${offset(past)}`],
    ["relative", formatExactDate, at(-DAY), at(-DAY).toLocaleDateString()],
    // A chart tick is never relative; iso writes the day month-day.
    ["relative", (v) => formatTimestampLong(Number(v)), past.getTime(), `${pad2(past.getMonth() + 1)}/${pad2(past.getDate())} ${pad2(past.getHours())}:${pad2(past.getMinutes())}`],
    ["iso", (v) => formatTimestampLong(Number(v)), past.getTime(), `${pad2(past.getMonth() + 1)}-${pad2(past.getDate())} ${pad2(past.getHours())}:${pad2(past.getMinutes())}`],
    ["iso", (v) => formatAxisDay(Number(v)), past.getTime(), `${pad2(past.getMonth() + 1)}-${pad2(past.getDate())}`],
  ])("under %s, %o of %s is %s", (dateDisplay, fn, value, want) => {
    usePreferencesStore.getState().setPreferences({ dateDisplay });
    expect(fn(value)).toBe(want);
  });

  it.each<DateDisplay>(["auto", "relative", "iso", "locale"])(
    "under %s, a missing, zero or unreadable value reads as the fallback or as sent",
    (dateDisplay) => {
      usePreferencesStore.getState().setPreferences({ dateDisplay });
      expect(formatDateTime(null)).toBe("—");
      expect(formatDateTime(undefined, "Never")).toBe("Never");
      expect(formatDateTime("", "Never")).toBe("Never");
      expect(formatDateTime(0, "-")).toBe("-");
      expect(formatDate(Number.NaN)).toBe("—");
      expect(formatDateTime("not a date")).toBe("not a date");
    },
  );
});

describe("formatRelativeTime", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function agoBy(ms: number): string {
    const now = new Date("2026-09-03T12:00:00Z");
    vi.useFakeTimers();
    vi.setSystemTime(now);
    return formatRelativeTime(new Date(now.getTime() - ms).toISOString());
  }

  it("reports seconds under a minute", () => {
    expect(agoBy(0)).toBe("0s ago");
    expect(agoBy(59_000)).toBe("59s ago");
  });

  it("switches unit at each boundary", () => {
    expect(agoBy(60_000)).toBe("1m ago");
    expect(agoBy(3_599_000)).toBe("59m ago");
    expect(agoBy(3_600_000)).toBe("1h ago");
    expect(agoBy(86_399_000)).toBe("23h ago");
    expect(agoBy(86_400_000)).toBe("1d ago");
  });

  it("keeps the coarsest unit for old entries", () => {
    expect(agoBy(30 * 86_400_000)).toBe("30d ago");
  });
});
