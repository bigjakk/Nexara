import { useState } from "react";
import { MoreHorizontal, X } from "lucide-react";

import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { Panel } from "@/components/page/Panel";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatBytes } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  useDiskSMART,
  useInitializeGPT,
  useNodeDisks,
  useWipeDisk,
} from "../../api/cluster-queries";
import {
  DirectorySection,
  LVMSection,
  LVMThinSection,
  ZFSPoolsSection,
} from "./NodeStorageSections";
import { NoValue, StateText, type StateTone } from "./StateText";

/**
 * How a S.M.A.R.T. verdict reads: a pass is quiet, no verdict is not a fault,
 * and anything else is one.
 */
function healthTone(health: string): StateTone {
  const verdict = health.toUpperCase();
  if (verdict === "PASSED" || verdict === "OK") return "ok";
  if (verdict === "" || verdict === "UNKNOWN") return "off";
  return "bad";
}

/** "PASSED" as "Passed"; an abbreviation such as a SAS disk's "OK" as it is. */
function healthLabel(health: string): string {
  if (health === "") return "Unknown";
  if (health.length <= 2) return health;
  return health.charAt(0).toUpperCase() + health.slice(1).toLowerCase();
}

export function DisksTab({
  clusterId,
  nodeId,
  nodeName,
  isOnline,
}: {
  clusterId: string;
  nodeId: string;
  nodeName: string;
  isOnline: boolean;
}) {
  const disksQuery = useNodeDisks(clusterId, nodeId);
  const disks = disksQuery.data;
  const [smartDisk, setSmartDisk] = useState<string | null>(null);
  const initGPT = useInitializeGPT(clusterId, nodeName);
  const wipeDisk = useWipeDisk(clusterId, nodeName);
  // Both row actions only set this; the disk is touched from its confirmation.
  const [pending, setPending] = useState<{
    disk: string;
    action: "gpt" | "wipe";
  } | null>(null);
  const count = disks?.length ?? 0;

  return (
    <div className="space-y-4">
      <Panel
        flush
        title="Physical disks"
        aside={
          count > 0
            ? `${String(count)} ${count === 1 ? "disk" : "disks"}. Select a device for its S.M.A.R.T. data.`
            : undefined
        }
      >
        {!disks || disks.length === 0 ? (
          <div className="px-[18px] pb-1">
            <QueryStateNotice
              query={disksQuery}
              subject="this node's physical disks"
              empty="No physical disks found."
              skeletonClassName="h-48 w-full"
            />
          </div>
        ) : (
          <Table className="whitespace-nowrap">
            <TableHeader>
              <TableRow>
                <TableHead>Device</TableHead>
                <TableHead>Model</TableHead>
                <TableHead>Serial</TableHead>
                <TableHead>Size</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Health</TableHead>
                <TableHead>Wearout</TableHead>
                {isOnline && <TableHead className="w-12" />}
              </TableRow>
            </TableHeader>
            <TableBody>
              {disks.map((d) => (
                <TableRow
                  key={d.id}
                  data-state={
                    smartDisk === d.dev_path && isOnline
                      ? "selected"
                      : undefined
                  }
                >
                  <TableCell className="font-mono text-[13px]">
                    <button
                      type="button"
                      aria-expanded={smartDisk === d.dev_path}
                      className="text-primary underline-offset-4 hover:underline"
                      onClick={() => {
                        setSmartDisk(
                          smartDisk === d.dev_path ? null : d.dev_path,
                        );
                      }}
                    >
                      {d.dev_path}
                    </button>
                  </TableCell>
                  <TableCell>{d.model || <NoValue />}</TableCell>
                  <TableCell className="font-mono text-[12.5px] text-muted-foreground">
                    {d.serial || <NoValue />}
                  </TableCell>
                  <TableCell className="whitespace-nowrap tabular-nums">
                    {formatBytes(d.size)}
                  </TableCell>
                  <TableCell>
                    {d.disk_type ? d.disk_type.toUpperCase() : "Unknown"}
                  </TableCell>
                  <TableCell>
                    <StateText tone={healthTone(d.health)}>
                      {healthLabel(d.health)}
                    </StateText>
                  </TableCell>
                  <TableCell className="tabular-nums">
                    {d.wearout && d.wearout !== "N/A" ? d.wearout : <NoValue />}
                  </TableCell>
                  {isOnline && (
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            aria-label={`Actions for ${d.dev_path}`}
                            variant="ghost"
                            size="icon"
                            className="h-7 w-7 text-muted-foreground"
                          >
                            <MoreHorizontal />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem
                            onClick={() => {
                              setPending({ disk: d.dev_path, action: "gpt" });
                            }}
                          >
                            Initialize with GPT
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            className="text-red-700 focus:text-red-700 dark:text-red-400 dark:focus:text-red-400"
                            onClick={() => {
                              setPending({ disk: d.dev_path, action: "wipe" });
                            }}
                          >
                            Wipe disk
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

      {smartDisk && isOnline && (
        <DiskSMARTPanel
          clusterId={clusterId}
          nodeName={nodeName}
          disk={smartDisk}
          onClose={() => {
            setSmartDisk(null);
          }}
        />
      )}

      {/* ZFS / LVM / LVM-Thin / Directory sections (only when online) */}
      {isOnline && (
        <>
          <ZFSPoolsSection clusterId={clusterId} nodeName={nodeName} />
          <LVMSection clusterId={clusterId} nodeName={nodeName} />
          <LVMThinSection clusterId={clusterId} nodeName={nodeName} />
          <DirectorySection clusterId={clusterId} nodeName={nodeName} />
        </>
      )}

      <ConfirmDeleteDialog
        target={pending}
        onClose={() => {
          setPending(null);
        }}
        onConfirm={(p) => {
          if (p.action === "gpt") initGPT.mutate(p.disk);
          else wipeDisk.mutate(p.disk);
        }}
        title={(p) =>
          p.action === "gpt"
            ? `Initialize ${p.disk} with GPT?`
            : `Wipe ${p.disk}?`
        }
        description={(p) =>
          p.action === "gpt"
            ? "This will create a new GPT partition table on the disk. All existing data will be lost."
            : "This will wipe all data from the disk. This action cannot be undone."
        }
        confirmLabel={(p) => (p.action === "gpt" ? "Initialize" : "Wipe")}
      />
    </div>
  );
}

function DiskSMARTPanel({
  clusterId,
  nodeName,
  disk,
  onClose,
}: {
  clusterId: string;
  nodeName: string;
  disk: string;
  onClose: () => void;
}) {
  const smartQuery = useDiskSMART(clusterId, nodeName, disk);
  const smart = smartQuery.data;
  const close = (
    <Button
      aria-label="Close S.M.A.R.T. data"
      variant="ghost"
      size="icon"
      className="h-7 w-7 text-muted-foreground"
      onClick={onClose}
    >
      <X />
    </Button>
  );

  // The panel is opened by clicking a disk, so it has to appear whatever the
  // read does. Returning null on failure looked exactly like the click not
  // registering, and left the operator with no reason for it.
  if (!smart) {
    return (
      <Panel title={`S.M.A.R.T. data for ${disk}`} actions={close}>
        <QueryStateNotice
          query={smartQuery}
          subject={`S.M.A.R.T. data for ${disk}`}
          empty="No S.M.A.R.T. data available for this disk."
          identity={disk}
          skeletonClassName="h-24 w-full"
        />
      </Panel>
    );
  }

  const attributes = smart.attributes ?? [];
  return (
    <Panel
      flush={attributes.length > 0}
      title={`S.M.A.R.T. data for ${disk}`}
      aside={
        <StateText tone={healthTone(smart.health)}>
          {healthLabel(smart.health)}
        </StateText>
      }
      actions={close}
    >
      {attributes.length > 0 ? (
        <Table className="whitespace-nowrap">
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Attribute</TableHead>
              <TableHead className="text-right">Value</TableHead>
              <TableHead className="text-right">Worst</TableHead>
              <TableHead className="text-right">Threshold</TableHead>
              <TableHead>Raw</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {attributes.map((attr) => {
              const failing = attr.value <= attr.threshold;
              return (
                <TableRow
                  key={attr.id}
                  className={cn(failing && "bg-destructive/10")}
                >
                  <TableCell className="py-1.5 font-mono text-[12.5px] text-muted-foreground">
                    {attr.id}
                  </TableCell>
                  <TableCell className="py-1.5">{attr.name}</TableCell>
                  <TableCell className="py-1.5 text-right font-mono text-[12.5px]">
                    {attr.value}
                  </TableCell>
                  <TableCell className="py-1.5 text-right font-mono text-[12.5px]">
                    {attr.worst}
                  </TableCell>
                  <TableCell className="py-1.5 text-right font-mono text-[12.5px]">
                    {attr.threshold}
                  </TableCell>
                  <TableCell className="py-1.5 font-mono text-[12.5px]">
                    {attr.raw}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      ) : smart.text ? (
        <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded-md bg-foreground/[0.05] p-3 text-xs">
          {smart.text}
        </pre>
      ) : (
        <p className="text-sm text-muted-foreground">
          No S.M.A.R.T. attributes available.
        </p>
      )}
    </Panel>
  );
}
