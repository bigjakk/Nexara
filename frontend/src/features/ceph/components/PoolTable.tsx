import { useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { UsageBar } from "@/components/UsageBar";
import { Trash2 } from "lucide-react";
import { PoolDeleteDialog } from "./PoolDeleteDialog";
import type { CephPool } from "../types/ceph";
import { formatBytes, formatBytesPerSecond, formatPercent } from "@/lib/format";
import { unaddressableHint } from "@/lib/api-path";
import { USAGE_TEXT_CLASS, usageLevel } from "@/lib/usage-level";
import { cn } from "@/lib/utils";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import { usePermissions } from "@/hooks/usePermissions";

interface PoolTableProps {
  pools: CephPool[];
  clusterId: string;
  /** Controls for the panel's title row: the Create pool button. */
  actions?: ReactNode;
}

export function PoolTable({ pools, clusterId, actions }: PoolTableProps) {
  const [deletePool, setDeletePool] = useState<string | null>(null);
  // Deleting a pool takes manage:ceph on the cluster, as the OSD actions do;
  // without it the table has no actions column.
  const canManageCeph = usePermissions().canManage("ceph");

  return (
    <>
      <Panel
        flush
        title="Pools"
        aside={
          pools.length === 0
            ? undefined
            : pools.length === 1
              ? "1 pool"
              : `${String(pools.length)} pools`
        }
        actions={actions}
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead className="text-right">Replicas</TableHead>
              <TableHead className="text-right">PGs</TableHead>
              <TableHead className="text-right">Used</TableHead>
              <TableHead>% used</TableHead>
              <TableHead className="text-right">IOPS, read / write</TableHead>
              <TableHead className="text-right">
                Throughput, read / write
              </TableHead>
              {canManageCeph && (
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {pools.map((pool) => {
              const percent = pool.percent_used * 100;
              return (
                <TableRow key={pool.pool}>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{pool.pool_name}</span>
                      <ClusterHistoryButton
                        history={clusterHistories.cephPool(
                          clusterId,
                          pool.pool_name,
                        )}
                        title={pool.pool_name}
                      />
                      {pool.pg_autoscale_mode === "on" && (
                        <span className="text-[12.5px] text-muted-foreground">
                          autoscale
                        </span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">
                    {pool.size}
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">
                    {pool.pg_num}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-right font-mono tabular-nums">
                    {formatBytes(pool.bytes_used)}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <UsageBar percent={percent} className="w-16 shrink-0" />
                      <span
                        className={cn(
                          "text-[12.5px] tabular-nums text-muted-foreground",
                          USAGE_TEXT_CLASS[usageLevel(percent)],
                        )}
                      >
                        {formatPercent(percent)}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-right font-mono tabular-nums">
                    {pool.read_op_per_sec} / {pool.write_op_per_sec}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-right font-mono tabular-nums">
                    {formatBytesPerSecond(pool.read_bytes_sec)} /{" "}
                    {formatBytesPerSecond(pool.write_bytes_sec)}
                  </TableCell>
                  {canManageCeph && (
                    <TableCell className="py-1 text-right">
                      {/* Ceph and Proxmox both admit a pool named "." or "..";
                          Nexara cannot address one (lib/api-path.ts), so the
                          row shows why in place of the delete — as text, since a
                          disabled button's title never shows. */}
                      {unaddressableHint(pool.pool_name) !== null ? (
                        <span className="text-xs text-muted-foreground">
                          {unaddressableHint(pool.pool_name)}
                        </span>
                      ) : (
                        <Button
                          aria-label={`Delete pool ${pool.pool_name}`}
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8 text-muted-foreground hover:text-destructive"
                          onClick={() => {
                            setDeletePool(pool.pool_name);
                          }}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      )}
                    </TableCell>
                  )}
                </TableRow>
              );
            })}
            {pools.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell
                  colSpan={canManageCeph ? 8 : 7}
                  className="py-8 text-center text-muted-foreground"
                >
                  No pools found.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Panel>

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
