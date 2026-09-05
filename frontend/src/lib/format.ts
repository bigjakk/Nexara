const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB", "PB"] as const;

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  // Use Math.abs in the log to support negative values (network deltas,
  // diff displays). Original sign is preserved through the division below.
  const i = Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024));
  const index = Math.min(Math.max(i, 0), BYTE_UNITS.length - 1);
  const unit = BYTE_UNITS[index];
  if (unit === undefined) return `${String(bytes)} B`;
  const value = bytes / Math.pow(1024, index);
  return `${value.toFixed(index === 0 ? 0 : 1)} ${unit}`;
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
  const i = Math.floor(Math.log(Math.abs(bytesPerSec)) / Math.log(1024));
  const index = Math.min(Math.max(i, 0), BYTE_UNITS.length - 1);
  const unit = BYTE_UNITS[index];
  if (unit === undefined) return `${String(bytesPerSec)} B/s`;
  const value = bytesPerSec / Math.pow(1024, index);
  return `${value.toFixed(index === 0 ? 0 : 1)} ${unit}/s`;
}

/** Zero-pads to two digits, for the clock and calendar fields below. */
export function pad2(n: number): string {
  return String(n).padStart(2, "0");
}

export function formatTimestamp(ts: number): string {
  const date = new Date(ts);
  const h = pad2(date.getHours());
  const m = pad2(date.getMinutes());
  const s = pad2(date.getSeconds());
  return `${h}:${m}:${s}`;
}

export function formatTimestampShort(ts: number): string {
  const date = new Date(ts);
  const h = pad2(date.getHours());
  const m = pad2(date.getMinutes());
  return `${h}:${m}`;
}

export function formatTimestampLong(ts: number): string {
  const date = new Date(ts);
  const mon = pad2(date.getMonth() + 1);
  const day = pad2(date.getDate());
  const h = pad2(date.getHours());
  const m = pad2(date.getMinutes());
  return `${mon}/${day} ${h}:${m}`;
}

/**
 * An ISO-8601 timestamp in the viewer's locale.
 *
 * `fallback` covers the missing case — call sites disagree on what "no value
 * yet" should read as ("Never" for a sync that has not run, an em dash for a
 * run still in flight), so it is a parameter rather than a constant.
 *
 * A value that will not parse is returned verbatim: if the server sent
 * something we cannot read, showing it beats hiding it behind "Invalid Date".
 */
export function formatDateTime(
  value: string | null | undefined,
  fallback = "—",
): string {
  if (value == null || value === "") return fallback;
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
}

/** How long ago an ISO-8601 timestamp was, to one unit ("3m ago", "2d ago"). */
export function formatRelativeTime(iso: string): string {
  const ago = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (ago < 60) return `${String(ago)}s ago`;
  if (ago < 3600) return `${String(Math.floor(ago / 60))}m ago`;
  if (ago < 86400) return `${String(Math.floor(ago / 3600))}h ago`;
  return `${String(Math.floor(ago / 86400))}d ago`;
}
