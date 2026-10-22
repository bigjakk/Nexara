import { MemoryStick, RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatBytes, formatRelativeTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  useNodeMemoryModules,
  useRefreshNodeMemoryModules,
  type NodeMemoryModule,
  type NodeMemoryModulesResponse,
} from "../../api/cluster-queries";
import { NodeCardShell } from "./NodeCardShell";

/**
 * A node's memory DIMM layout: every slot, its module, and the headroom to the
 * firmware's maximum, as `dmidecode` reports them over the cluster's SSH
 * credentials. Like the temperatures card it depends on SSH and degrades to a
 * note; unlike it, it shows a stored read, so opening the page never dials the
 * node. The server reads each node daily, and Read now reads it on demand.
 */
export function NodeMemoryModules({
  clusterId,
  nodeName,
  online,
  canRead,
  className,
}: {
  clusterId: string;
  nodeName: string;
  /** Read now needs the node reachable over SSH. */
  online: boolean;
  /** Read now opens a root SSH session, so it needs manage:node. */
  canRead: boolean;
  className?: string;
}) {
  const { data, isError } = useNodeMemoryModules(clusterId, nodeName);
  const refresh = useRefreshNodeMemoryModules(clusterId, nodeName);

  return (
    <NodeCardShell
      icon={<MemoryStick className="h-4 w-4" />}
      title="Memory modules"
      className={className}
      action={
        canRead ? (
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            disabled={!online || refresh.isPending}
            title={
              online
                ? "Read the memory layout now with dmidecode over the cluster's SSH credentials"
                : "The node is offline"
            }
            onClick={() => {
              refresh.mutate();
            }}
          >
            <RefreshCw
              className={cn("h-3.5 w-3.5", refresh.isPending && "animate-spin")}
            />
            {refresh.isPending ? "Reading…" : "Read now"}
          </Button>
        ) : undefined
      }
    >
      {/* Data first, even beside a failed refetch; then the error; and a
          query with neither, loading or paused in a background tab, shows the
          skeleton rather than an empty card. */}
      {data !== undefined ? (
        <Body data={data} />
      ) : isError ? (
        <Note>Could not load the memory layout for this node.</Note>
      ) : (
        <div className="space-y-2">
          <Skeleton className="h-5 w-full" />
          <Skeleton className="h-5 w-4/5" />
        </div>
      )}
    </NodeCardShell>
  );
}

function Body({ data }: { data: NodeMemoryModulesResponse }) {
  if (data.status === "never") {
    return (
      <Note>
        {data.auto_read
          ? "Not read yet. Nexara reads each node's memory layout once a day with dmidecode over the cluster's SSH credentials."
          : "Not read yet. Read now reads it with dmidecode over the cluster's SSH credentials."}
      </Note>
    );
  }
  if (data.read_at === undefined) {
    return <Note>{data.reason ?? "The memory layout could not be read."}</Note>;
  }
  return (
    <div className="space-y-3">
      {data.status !== "ok" && (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          The last read failed: {data.reason} Showing the read from{" "}
          {formatRelativeTime(data.read_at)}.
        </p>
      )}
      <p className="text-sm">
        <span className="font-medium">
          {formatBytes(data.installed_bytes)}
        </span>{" "}
        installed in {data.slots_used} of {data.slots_total} slots
        {data.max_capacity_bytes > 0 && (
          <>
            {" "}
            · firmware maximum {formatBytes(data.max_capacity_bytes)}
          </>
        )}
        {data.ecc !== undefined && <> · {data.ecc}</>}
        <span className="text-muted-foreground">
          {" "}
          · read {formatRelativeTime(data.read_at)}
        </span>
      </p>
      {data.node_mem_total > data.installed_bytes && (
        <Note>
          The firmware lists {formatBytes(data.installed_bytes)}, but the node
          has {formatBytes(data.node_mem_total)}: memory hot-plugged into a
          virtual node, modules the firmware leaves out, or memory added since
          this read are missing from this table.
        </Note>
      )}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Slot</TableHead>
            <TableHead>Size</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Speed</TableHead>
            <TableHead>Manufacturer</TableHead>
            <TableHead>Part number</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.items.map((m, i) => (
            <ModuleRow key={`${m.locator}-${String(i)}`} module={m} />
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function ModuleRow({ module: m }: { module: NodeMemoryModule }) {
  const slot = m.bank_locator ? `${m.locator} (${m.bank_locator})` : m.locator;
  if (m.size_bytes === 0) {
    return (
      <TableRow className="text-muted-foreground">
        <TableCell>{slot}</TableCell>
        <TableCell colSpan={5}>Empty</TableCell>
      </TableRow>
    );
  }
  return (
    <TableRow>
      <TableCell>{slot}</TableCell>
      <TableCell className="tabular-nums">{formatBytes(m.size_bytes)}</TableCell>
      <TableCell>
        {[m.type, m.form_factor].filter(Boolean).join(" ") || "—"}
      </TableCell>
      <TableCell className="tabular-nums">{speedLabel(m)}</TableCell>
      <TableCell>{m.manufacturer ?? "—"}</TableCell>
      <TableCell className="font-mono text-xs">{m.part_number ?? "—"}</TableCell>
    </TableRow>
  );
}

/** "2666 of 2933 MT/s" when the board runs a module below its rating. */
function speedLabel(m: NodeMemoryModule): string {
  const rated = m.speed_mts ?? 0;
  const running = m.configured_speed_mts ?? 0;
  if (running > 0 && rated > 0 && running !== rated) {
    return `${String(running)} of ${String(rated)} MT/s`;
  }
  const either = running || rated;
  return either > 0 ? `${String(either)} MT/s` : "—";
}

function Note({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>;
}
