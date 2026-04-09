/**
 * Tests for the small formatting helpers in `lib/format.ts`. Pure
 * functions, no mocks needed.
 */

import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  jest,
} from "@jest/globals";

import {
  formatBytes,
  formatPercent,
  formatRelative,
  formatUptime,
} from "./format";

describe("formatBytes", () => {
  it("returns em-dash for negative or non-finite", () => {
    expect(formatBytes(-1)).toBe("—");
    expect(formatBytes(NaN)).toBe("—");
    expect(formatBytes(Infinity)).toBe("—");
  });

  it("renders raw bytes under 1024 with B suffix", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1023)).toBe("1023 B");
  });

  it("scales to KiB / MiB / GiB / TiB / PiB", () => {
    expect(formatBytes(1024)).toBe("1.0 KiB");
    expect(formatBytes(1024 * 1024)).toBe("1.0 MiB");
    expect(formatBytes(1024 * 1024 * 1024)).toBe("1.0 GiB");
    expect(formatBytes(1024 * 1024 * 1024 * 1024)).toBe("1.0 TiB");
    expect(formatBytes(1024 ** 5)).toBe("1.0 PiB");
  });

  it("uses 1 decimal under 10, 0 decimals at/above 10", () => {
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(1024 * 11)).toBe("11 KiB");
    expect(formatBytes(1024 * 100)).toBe("100 KiB");
  });

  it("caps at PiB and doesn't overflow into a missing unit", () => {
    // 1 EiB worth of bytes — clamps to PiB, no crash
    const oneEiB = 1024 ** 6;
    const result = formatBytes(oneEiB);
    expect(result).toContain("PiB");
  });
});

describe("formatUptime", () => {
  it("returns em-dash for zero or negative or non-finite", () => {
    expect(formatUptime(0)).toBe("—");
    expect(formatUptime(-5)).toBe("—");
    expect(formatUptime(NaN)).toBe("—");
  });

  it("formats sub-minute as Ns", () => {
    expect(formatUptime(1)).toBe("1s");
    expect(formatUptime(59)).toBe("59s");
  });

  it("formats sub-hour as Nm", () => {
    expect(formatUptime(60)).toBe("1m");
    expect(formatUptime(60 * 30)).toBe("30m");
    expect(formatUptime(60 * 59)).toBe("59m");
  });

  it("formats sub-day as Nh Mm", () => {
    expect(formatUptime(3600)).toBe("1h 0m");
    expect(formatUptime(3600 + 60 * 30)).toBe("1h 30m");
    expect(formatUptime(3600 * 23 + 60 * 59)).toBe("23h 59m");
  });

  it("formats day-or-more as Nd Mh", () => {
    expect(formatUptime(86400)).toBe("1d 0h");
    expect(formatUptime(86400 + 3600 * 5)).toBe("1d 5h");
    expect(formatUptime(86400 * 7 + 3600 * 12)).toBe("7d 12h");
  });
});

describe("formatPercent", () => {
  it("returns em-dash for non-finite", () => {
    expect(formatPercent(NaN)).toBe("—");
    expect(formatPercent(Infinity)).toBe("—");
  });

  it("uses 1 decimal place by default", () => {
    expect(formatPercent(0)).toBe("0.0%");
    expect(formatPercent(99.999)).toBe("100.0%");
    expect(formatPercent(50.5)).toBe("50.5%");
  });

  it("respects an explicit decimals argument", () => {
    expect(formatPercent(50.4, 0)).toBe("50%");
    expect(formatPercent(50.6, 0)).toBe("51%");
    // Note: JS Number.prototype.toFixed uses banker's-rounding-ish
    // behavior on floats that can't be represented exactly. Use values
    // that don't sit on the rounding boundary.
    expect(formatPercent(50.12, 2)).toBe("50.12%");
    expect(formatPercent(50.999, 2)).toBe("51.00%");
  });
});

describe("formatRelative", () => {
  // Freeze "now" so the relative outputs are deterministic.
  const NOW = new Date("2026-04-08T12:00:00Z").getTime();
  beforeEach(() => {
    jest.useFakeTimers();
    jest.setSystemTime(NOW);
  });
  afterEach(() => {
    jest.useRealTimers();
  });

  it("returns em-dash for null / undefined / unparseable", () => {
    expect(formatRelative(null)).toBe("—");
    expect(formatRelative(undefined)).toBe("—");
    expect(formatRelative("not-a-date")).toBe("—");
  });

  it("formats sub-minute deltas in seconds", () => {
    const t = new Date(NOW - 30_000).toISOString();
    expect(formatRelative(t)).toBe("30s ago");
  });

  it("formats sub-hour deltas in minutes", () => {
    const t = new Date(NOW - 5 * 60 * 1000).toISOString();
    expect(formatRelative(t)).toBe("5m ago");
  });

  it("formats sub-day deltas in hours", () => {
    const t = new Date(NOW - 3 * 60 * 60 * 1000).toISOString();
    expect(formatRelative(t)).toBe("3h ago");
  });

  it("formats sub-week deltas in days", () => {
    const t = new Date(NOW - 4 * 86400 * 1000).toISOString();
    expect(formatRelative(t)).toBe("4d ago");
  });

  it("falls back to a locale date string for older entries", () => {
    const t = new Date(NOW - 30 * 86400 * 1000).toISOString();
    const result = formatRelative(t);
    // Don't pin the exact format — locale-dependent. Just verify it's
    // not the relative form anymore.
    expect(result).not.toMatch(/ago$/);
    expect(result).not.toBe("—");
  });
});
