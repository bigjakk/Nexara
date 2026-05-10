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

export function formatTimestamp(ts: number): string {
  const date = new Date(ts);
  const h = String(date.getHours()).padStart(2, "0");
  const m = String(date.getMinutes()).padStart(2, "0");
  const s = String(date.getSeconds()).padStart(2, "0");
  return `${h}:${m}:${s}`;
}

export function formatTimestampShort(ts: number): string {
  const date = new Date(ts);
  const h = String(date.getHours()).padStart(2, "0");
  const m = String(date.getMinutes()).padStart(2, "0");
  return `${h}:${m}`;
}

export function formatTimestampLong(ts: number): string {
  const date = new Date(ts);
  const mon = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  const h = String(date.getHours()).padStart(2, "0");
  const m = String(date.getMinutes()).padStart(2, "0");
  return `${mon}/${day} ${h}:${m}`;
}
