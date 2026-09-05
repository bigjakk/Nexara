import { describe, it, expect, afterEach, vi } from "vitest";

import {
  formatBytes,
  formatBytesPerSecond,
  formatDateTime,
  formatPercent,
  formatRelativeTime,
  formatUptime,
} from "./format";

describe("formatBytes", () => {
  it("returns 0 B for zero", () => {
    expect(formatBytes(0)).toBe("0 B");
  });

  it("formats integer bytes without decimals", () => {
    expect(formatBytes(512)).toBe("512 B");
  });

  it("formats KB with 1 decimal", () => {
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(2048)).toBe("2.0 KB");
  });

  it("formats GB scale", () => {
    expect(formatBytes(1024 * 1024 * 1024)).toBe("1.0 GB");
    expect(formatBytes(1024 * 1024 * 1024 * 1.5)).toBe("1.5 GB");
  });

  it("clamps to PB at the high end", () => {
    const oneEB = 1024 ** 6;
    expect(formatBytes(oneEB)).toBe("1024.0 PB");
  });

  it("handles negative values for chart deltas", () => {
    expect(formatBytes(-1024)).toBe("-1.0 KB");
    expect(formatBytes(-1)).toBe("-1 B");
  });
});

describe("formatBytesPerSecond", () => {
  it("returns 0 B/s for zero", () => {
    expect(formatBytesPerSecond(0)).toBe("0 B/s");
  });

  it("formats KB/s scale", () => {
    expect(formatBytesPerSecond(1024)).toBe("1.0 KB/s");
  });

  it("handles negatives", () => {
    expect(formatBytesPerSecond(-2048)).toBe("-2.0 KB/s");
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

describe("formatDateTime", () => {
  it("renders a parseable ISO timestamp in the locale", () => {
    // Not asserting the exact text: toLocaleString follows the runtime's
    // locale, so pin only that it produced a real rendering of that instant.
    expect(formatDateTime("2026-09-03T14:03:22Z")).toBe(
      new Date("2026-09-03T14:03:22Z").toLocaleString(),
    );
  });

  it("falls back to an em dash when there is no value", () => {
    expect(formatDateTime(null)).toBe("—");
    expect(formatDateTime(undefined)).toBe("—");
    expect(formatDateTime("")).toBe("—");
  });

  it("honors a custom fallback for the missing case", () => {
    // "Never" is what the Veeam tables pass for a sync that has not run.
    expect(formatDateTime(null, "Never")).toBe("Never");
    expect(formatDateTime("", "Never")).toBe("Never");
  });

  it("returns an unparseable value verbatim rather than Invalid Date", () => {
    expect(formatDateTime("not a date")).toBe("not a date");
    expect(formatDateTime("not a date", "Never")).toBe("not a date");
  });
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
