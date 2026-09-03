import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { OSDActionDialog } from "./OSDActionDialog";
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

  return (
    <>
      <div className="overflow-x-auto rounded-md border">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="px-4 py-2 text-left font-medium">ID</th>
              <th className="px-4 py-2 text-left font-medium">Name</th>
              <th className="px-4 py-2 text-left font-medium">Host</th>
              <th className="px-4 py-2 text-left font-medium">Status</th>
              <th className="px-4 py-2 text-right font-medium">CRUSH Weight</th>
              {canManageCeph && (
                <th className="px-4 py-2 text-center font-medium">Actions</th>
              )}
            </tr>
          </thead>
          <tbody>
            {sorted.map((osd) => (
              <tr key={osd.id} className="border-b last:border-0">
                <td className="px-4 py-2 font-mono">{osd.id}</td>
                <td className="px-4 py-2">{osd.name}</td>
                <td className="px-4 py-2">{osd.host}</td>
                <td className="px-4 py-2">
                  <div className="flex gap-1">
                    <Badge variant={osd.up === 1 ? "default" : "destructive"}>
                      {osd.up === 1 ? "Up" : "Down"}
                    </Badge>
                    <Badge variant={osd.in === 1 ? "default" : "secondary"}>
                      {osd.in === 1 ? "In" : "Out"}
                    </Badge>
                  </div>
                </td>
                <td className="px-4 py-2 text-right font-mono">
                  {osd.crush_weight.toFixed(4)}
                </td>
                {canManageCeph && (
                  <td className="px-4 py-2 text-center">
                    <OSDActionsMenu
                      osd={osd}
                      onSelect={(action) => {
                        setPending({ osd, action });
                      }}
                    />
                  </td>
                )}
              </tr>
            ))}
            {sorted.length === 0 && (
              <tr>
                <td
                  colSpan={columnCount}
                  className="px-4 py-8 text-center text-muted-foreground"
                >
                  No OSDs found.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

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
            Mark Out
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            onSelect={() => {
              onSelect("in");
            }}
          >
            <LogIn className="mr-2 h-4 w-4" />
            Mark In
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
            Stop Daemon
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            onSelect={() => {
              onSelect("start");
            }}
          >
            <Play className="mr-2 h-4 w-4" />
            Start Daemon
          </DropdownMenuItem>
        )}
        <DropdownMenuItem
          onSelect={() => {
            onSelect("restart");
          }}
        >
          <RotateCw className="mr-2 h-4 w-4" />
          Restart Daemon
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
