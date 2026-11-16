import { RefreshCw } from "lucide-react";

import { Panel } from "@/components/page/Panel";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatBytes, formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import {
  useNodeMemoryModules,
  useRefreshNodeMemoryModules,
  type NodeMemoryModule,
  type NodeMemoryModulesResponse,
} from "../../api/cluster-queries";
import { DateTime } from "@/components/DateTime";

/**
 * A node's memory DIMM layout: every slot, its module, and the headroom to the
 * firmware's maximum, as `dmidecode` reports them over the cluster's SSH
 * credentials. Like the temperatures it depends on SSH; unlike them, it shows
 * a stored read, so opening the page never dials the node. The server reads
 * each node daily, and Read now reads it on demand.
 *
 * The panel is drawn while the stored read is being fetched and once there is
 * one to show. When there is none, NodeMemoryModulesNote says why, as one line
 * of the Hardware panel, with the same Read now. Both read the same query, so
 * the page asks once.
 */
export function NodeMemoryModules({
  clusterId,
  nodeName,
  online,
  canRead,
}: {
  clusterId: string;
  nodeName: string;
  /** Read now needs the node reachable over SSH. */
  online: boolean;
  /** Read now opens a root SSH session, so it needs manage:node. */
  canRead: boolean;
}) {
  const { data, isError } = useNodeMemoryModules(clusterId, nodeName);
  const read = storedRead(data);
  const readNow = canRead ? (
    <ReadNowButton clusterId={clusterId} nodeName={nodeName} online={online} />
  ) : undefined;
  // A query with neither data nor an error is loading, or paused in a
  // background tab: the panel holds its place until the read lands.
  if (data === undefined && !isError) {
    return (
      <Panel title="Memory modules" actions={readNow}>
        <p className="text-sm text-muted-foreground">Reading…</p>
      </Panel>
    );
  }
  if (read === null) return null;

  return (
    <Panel
      flush
      title="Memory modules"
      aside={`read ${formatDateTime(read.read_at)}`}
      actions={readNow}
    >
      <div className="space-y-2 px-[18px] pb-3">
        {read.status !== "ok" && (
          <p className="text-sm text-amber-700 dark:text-amber-400">
            The last read failed: {read.reason} Showing the read from{" "}
            <DateTime value={read.read_at} />.
          </p>
        )}
        <p className="text-sm">
          <span className="font-semibold">
            {formatBytes(read.installed_bytes)}
          </span>{" "}
          installed in {read.slots_used} of {read.slots_total} slots
          {read.max_capacity_bytes > 0 && (
            <> · firmware maximum {formatBytes(read.max_capacity_bytes)}</>
          )}
          {read.ecc !== undefined && <> · {read.ecc}</>}
        </p>
        {read.node_mem_total > read.installed_bytes && (
          <Note>
            The firmware lists {formatBytes(read.installed_bytes)}, but the node
            has {formatBytes(read.node_mem_total)}: memory hot-plugged into a
            virtual node, modules the firmware leaves out, or memory added since
            this read are missing from this table.
          </Note>
        )}
      </div>
      <Table className="whitespace-nowrap">
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
          {read.items.map((m, i) => (
            <ModuleRow key={`${m.locator}-${String(i)}`} module={m} />
          ))}
        </TableBody>
      </Table>
    </Panel>
  );
}

/**
 * Why the node page has no Memory modules panel: the layout was never read, or
 * could not be. Nothing while the read of the stored layout is out, and
 * nothing once there is one to show.
 */
export function NodeMemoryModulesNote({
  clusterId,
  nodeName,
  online,
  canRead,
}: {
  clusterId: string;
  nodeName: string;
  online: boolean;
  canRead: boolean;
}) {
  const { data, isError } = useNodeMemoryModules(clusterId, nodeName);
  if (storedRead(data) !== null) return null;
  // A query with neither data nor an error is loading, or paused in a
  // background tab: the line is left out until it has something to say.
  if (data === undefined && !isError) return null;

  return (
    <div className="space-y-1.5 text-[12.5px]">
      <div>
        <p className="font-medium">Memory modules</p>
        <Note>
          {data === undefined
            ? "Could not load the memory layout for this node."
            : data.status === "never"
              ? data.auto_read
                ? "Not read yet. Nexara reads each node's memory layout once a day with dmidecode over the cluster's SSH credentials."
                : "Not read yet. Read now reads it with dmidecode over the cluster's SSH credentials."
              : (data.reason ?? "The memory layout could not be read.")}
        </Note>
      </div>
      {canRead && (
        <ReadNowButton
          clusterId={clusterId}
          nodeName={nodeName}
          online={online}
        />
      )}
    </div>
  );
}

/** The layout when there is a stored read of it to show, else null. */
function storedRead(
  data: NodeMemoryModulesResponse | undefined,
): (NodeMemoryModulesResponse & { read_at: string }) | null {
  if (data === undefined || data.status === "never") return null;
  const { read_at } = data;
  return read_at === undefined ? null : { ...data, read_at };
}

function ReadNowButton({
  clusterId,
  nodeName,
  online,
}: {
  clusterId: string;
  nodeName: string;
  online: boolean;
}) {
  const refresh = useRefreshNodeMemoryModules(clusterId, nodeName);
  return (
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
  return <p className="text-[12.5px] text-muted-foreground">{children}</p>;
}
