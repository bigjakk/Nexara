interface StorageCapacityBarProps {
  used: number;
  total: number;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const k = 1024;
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const value = bytes / Math.pow(k, i);
  return `${value.toFixed(1)} ${units[i] ?? "?"}`;
}

export function StorageCapacityBar({ used, total }: StorageCapacityBarProps) {
  const pct = total > 0 ? (used / total) * 100 : 0;
  const color =
    pct > 90
      ? "bg-destructive"
      : pct > 70
        ? "bg-yellow-500"
        : "bg-primary";

  return (
    <div className="space-y-1">
      <div className="flex justify-between text-xs text-muted-foreground">
        <span>
          {formatBytes(used)} / {formatBytes(total)}
        </span>
        <span>{pct.toFixed(1)}%</span>
      </div>
      <div className="h-2 w-full rounded-full bg-muted">
        <div
          className={`h-full rounded-full transition-all ${color}`}
          style={{ width: `${String(Math.min(pct, 100))}%` }}
        />
      </div>
    </div>
  );
}
