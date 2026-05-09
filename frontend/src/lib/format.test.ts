import { describe, it, expect } from "vitest";

import {
  formatBytes,
  formatBytesPerSecond,
  formatPercent,
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
