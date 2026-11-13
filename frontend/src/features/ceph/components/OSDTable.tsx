import { useState } from "react";
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  MoreHorizontal,
  Play,
  Square,
  RotateCw,
  LogIn,
  LogOut,
} from "lucide-react";
import { usePermissions } from "@/hooks/usePermissions";
import { ClusterHistoryButton } from "@/features/history/components/ClusterHistoryButton";
import { clusterHistories } from "@/features/history/lib/cluster-histories";
import { cn } from "@/lib/utils";
import { OSDActionDialog } from "./OSDActionDialog";
import { OSDGrid } from "./OSDGrid";
import type { CephOSD, CephOSDAction } from "../types/ceph";

interface OSDTableProps {
  osds: CephOSD[];
  clusterId: string;
}

export function OSDTable({ osds, clusterId }: OSDTableProps) {
  const { canManage } = usePermissions();
  const [pending, setPending] = useState<{
    osd: CephOSD;
    action: CephOSDAction;
  } | null>(null);

  const canManageCeph = canManage("ceph");
  const sorted = [...osds].sort((a, b) => a.id - b.id);
  const columnCount = canManageCeph ? 6 : 5;
  const upCount = sorted.filter((o) => o.up === 1).length;
  const inCount = sorted.filter((o) => o.in === 1).length;
  const downCount = sorted.length - upCount;
  const outCount = sorted.length - inCount;

  return (
    <>
      <Panel
        flush
        title="OSDs"
        aside={
          sorted.length === 0 ? undefined : (
            <>
              {`${String(upCount)} of ${String(sorted.length)} up, ${String(inCount)} in`}
              {downCount > 0 && (
                <span className="ml-2 font-semibold text-red-700 dark:text-red-400">
                  {`${String(downCount)} down`}
                </span>
              )}
              {outCount > 0 && (
                <span className="ml-2 font-semibold text-amber-700 dark:text-amber-400">
                  {`${String(outCount)} out`}
                </span>
              )}
            </>
          )
        }
      >
        {sorted.length > 0 && (
          <div className="px-[18px] pb-4">
            <OSDGrid osds={sorted} />
          </div>
        )}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Host</TableHead>
              <TableHead>State</TableHead>
              <TableHead className="text-right">CRUSH weight</TableHead>
              {canManageCeph && (
                <TableHead className="w-0">
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {sorted.map((osd) => (
              <TableRow key={osd.id}>
                <TableCell className="font-mono tabular-nums">
                  {osd.id}
                  <ClusterHistoryButton
                    history={clusterHistories.cephOsd(clusterId, osd.id)}
                    title={osd.name || `osd.${String(osd.id)}`}
                  />
                </TableCell>
                <TableCell className="font-medium">{osd.name}</TableCell>
                <TableCell>{osd.host}</TableCell>
                <TableCell>
                  <OSDState osd={osd} />
                </TableCell>
                <TableCell className="text-right font-mono tabular-nums">
                  {osd.crush_weight.toFixed(4)}
                </TableCell>
                {canManageCeph && (
                  <TableCell className="py-1 text-right">
                    <OSDActionsMenu
                      osd={osd}
                      onSelect={(action) => {
                        setPending({ osd, action });
                      }}
                    />
                  </TableCell>
                )}
              </TableRow>
            ))}
            {sorted.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell
                  colSpan={columnCount}
                  className="py-8 text-center text-muted-foreground"
                >
                  No OSDs found.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Panel>

      <OSDActionDialog
        clusterId={clusterId}
        osd={pending?.osd ?? null}
        action={pending?.action ?? null}
        onClose={() => {
          setPending(null);
        }}
      />
    </>
  );
}

/** Up and in is the quiet state; down is red and out is amber. */
function OSDState({ osd }: { osd: CephOSD }) {
  const isUp = osd.up === 1;
  const isIn = osd.in === 1;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-2 whitespace-nowrap",
        !isUp
          ? "font-semibold text-red-700 dark:text-red-400"
          : !isIn && "font-semibold text-amber-700 dark:text-amber-400",
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "h-2 w-2 shrink-0 rounded-full",
          !isUp ? "bg-red-500" : !isIn ? "bg-amber-500" : "bg-emerald-500",
        )}
      />
      {`${isUp ? "Up" : "Down"}, ${isIn ? "in" : "out"}`}
    </span>
  );
}

function OSDActionsMenu({
  osd,
  onSelect,
}: {
  osd: CephOSD;
  onSelect: (action: CephOSDAction) => void;
}) {
  const isUp = osd.up === 1;
  const isIn = osd.in === 1;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" className="h-8 w-8">
          <MoreHorizontal className="h-4 w-4" />
          <span className="sr-only">
            Actions for {osd.name || `osd.${String(osd.id)}`}
          </span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {isIn ? (
          <DropdownMenuItem
            onSelect={() => {
              onSelect("out");
            }}
          >
            <LogOut className="mr-2 h-4 w-4" />
            Mark out
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            onSelect={() => {
              onSelect("in");
            }}
          >
            <LogIn className="mr-2 h-4 w-4" />
            Mark in
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        {isUp ? (
          <DropdownMenuItem
            onSelect={() => {
              onSelect("stop");
            }}
          >
            <Square className="mr-2 h-4 w-4" />
            Stop daemon
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            onSelect={() => {
              onSelect("start");
            }}
          >
            <Play className="mr-2 h-4 w-4" />
            Start daemon
          </DropdownMenuItem>
        )}
        <DropdownMenuItem
          onSelect={() => {
            onSelect("restart");
          }}
        >
          <RotateCw className="mr-2 h-4 w-4" />
          Restart daemon
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
