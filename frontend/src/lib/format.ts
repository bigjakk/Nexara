const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB", "PB"] as const;

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const index = Math.min(i, BYTE_UNITS.length - 1);
  const unit = BYTE_UNITS[index];
  if (unit === undefined) return `${String(bytes)} B`;
  const value = bytes / Math.pow(1024, index);
  return `${value.toFixed(index === 0 ? 0 : 1)} ${unit}`;
}

export function formatUptime(seconds: number): string {
  if (seconds <= 0) return "0s";
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
  const i = Math.floor(Math.log(bytesPerSec) / Math.log(1024));
  const index = Math.min(i, BYTE_UNITS.length - 1);
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
