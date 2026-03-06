import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Activity, HardDrive, Database, Gauge } from "lucide-react";
import { CephHealthBadge } from "./CephHealthBadge";
import type { CephStatus } from "../types/ceph";

interface CephStatusCardsProps {
  status: CephStatus;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const k = 1024;
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const idx = Math.min(i, units.length - 1);
  return `${(bytes / Math.pow(k, idx)).toFixed(1)} ${units[idx] ?? "?"}`;
}

function formatOps(ops: number): string {
  if (ops >= 1000000) return `${(ops / 1000000).toFixed(1)}M`;
  if (ops >= 1000) return `${(ops / 1000).toFixed(1)}K`;
  return String(ops);
}

export function CephStatusCards({ status }: CephStatusCardsProps) {
  const usedPct =
    status.pgmap.bytes_total > 0
      ? ((status.pgmap.bytes_used / status.pgmap.bytes_total) * 100).toFixed(1)
      : "0";

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">Health</CardTitle>
          <Activity className="h-4 w-4 text-foreground/50" />
        </CardHeader>
        <CardContent>
          <CephHealthBadge status={status.health.status} />
          <p className="mt-2 text-xs text-foreground/60">
            {status.osdmap.num_osds} OSDs, {status.pgmap.num_pgs} PGs
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">Capacity</CardTitle>
          <Database className="h-4 w-4 text-foreground/50" />
        </CardHeader>
        <CardContent>
          <div className="text-2xl font-bold">
            {formatBytes(status.pgmap.bytes_used)}
          </div>
          <p className="text-xs text-foreground/60">
            {usedPct}% of{" "}
            {formatBytes(status.pgmap.bytes_total)}
          </p>
          <div className="mt-2 h-2 rounded-full bg-muted">
            <div
              className="h-2 rounded-full bg-primary"
              style={{ width: `${String(Math.min(Number(usedPct), 100))}%` }}
            />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">IOPS</CardTitle>
          <Gauge className="h-4 w-4 text-foreground/50" />
        </CardHeader>
        <CardContent>
          <div className="text-2xl font-bold">
            {formatOps(
              status.pgmap.read_op_per_sec + status.pgmap.write_op_per_sec,
            )}
          </div>
          <p className="text-xs text-foreground/60">
            R: {formatOps(status.pgmap.read_op_per_sec)} / W:{" "}
            {formatOps(status.pgmap.write_op_per_sec)}
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">Throughput</CardTitle>
          <HardDrive className="h-4 w-4 text-foreground/50" />
        </CardHeader>
        <CardContent>
          <div className="text-2xl font-bold">
            {formatBytes(
              status.pgmap.read_bytes_sec + status.pgmap.write_bytes_sec,
            )}
            /s
          </div>
          <p className="text-xs text-foreground/60">
            R: {formatBytes(status.pgmap.read_bytes_sec)}/s / W:{" "}
            {formatBytes(status.pgmap.write_bytes_sec)}/s
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
