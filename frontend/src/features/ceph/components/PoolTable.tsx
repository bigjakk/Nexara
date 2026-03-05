import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Trash2 } from "lucide-react";
import { PoolDeleteDialog } from "./PoolDeleteDialog";
import type { CephPool } from "../types/ceph";

interface PoolTableProps {
  pools: CephPool[];
  clusterId: string;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const k = 1024;
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const idx = Math.min(i, units.length - 1);
  return `${(bytes / Math.pow(k, idx)).toFixed(1)} ${units[idx] ?? "?"}`;
}

export function PoolTable({ pools, clusterId }: PoolTableProps) {
  const [deletePool, setDeletePool] = useState<string | null>(null);

  return (
    <>
      <div className="overflow-x-auto rounded-md border">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="px-4 py-2 text-left font-medium">Name</th>
              <th className="px-4 py-2 text-right font-medium">Size</th>
              <th className="px-4 py-2 text-right font-medium">PGs</th>
              <th className="px-4 py-2 text-right font-medium">Used</th>
              <th className="px-4 py-2 text-right font-medium">% Used</th>
              <th className="px-4 py-2 text-right font-medium">IOPS (R/W)</th>
              <th className="px-4 py-2 text-right font-medium">
                Throughput (R/W)
              </th>
              <th className="px-4 py-2 text-center font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            {pools.map((pool) => (
              <tr key={pool.pool} className="border-b last:border-0">
                <td className="px-4 py-2">
                  <div className="flex items-center gap-2">
                    <span className="font-medium">{pool.pool_name}</span>
                    {pool.pg_autoscale_mode === "on" && (
                      <Badge variant="outline" className="text-[10px]">
                        autoscale
                      </Badge>
                    )}
                  </div>
                </td>
                <td className="px-4 py-2 text-right font-mono">{pool.size}</td>
                <td className="px-4 py-2 text-right font-mono">
                  {pool.pg_num}
                </td>
                <td className="px-4 py-2 text-right font-mono">
                  {formatBytes(pool.bytes_used)}
                </td>
                <td className="px-4 py-2 text-right font-mono">
                  {(pool.percent_used * 100).toFixed(1)}%
                </td>
                <td className="px-4 py-2 text-right font-mono">
                  {pool.read_op_per_sec} / {pool.write_op_per_sec}
                </td>
                <td className="px-4 py-2 text-right font-mono">
                  {formatBytes(pool.read_bytes_sec)}/s /{" "}
                  {formatBytes(pool.write_bytes_sec)}/s
                </td>
                <td className="px-4 py-2 text-center">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setDeletePool(pool.pool_name);
                    }}
                  >
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </td>
              </tr>
            ))}
            {pools.length === 0 && (
              <tr>
                <td
                  colSpan={8}
                  className="px-4 py-8 text-center text-muted-foreground"
                >
                  No pools found.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {deletePool !== null && (
        <PoolDeleteDialog
          clusterId={clusterId}
          poolName={deletePool}
          open
          onClose={() => {
            setDeletePool(null);
          }}
        />
      )}
    </>
  );
}
