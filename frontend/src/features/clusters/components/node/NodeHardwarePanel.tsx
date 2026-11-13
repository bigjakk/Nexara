import type { ReactNode } from "react";

import { Panel, PropertyList, PropertyRow } from "@/components/page/Panel";
import { formatBytes } from "@/lib/format";
import type { NodeResponse } from "@/types/api";

/** A value that is cut short when it is long, with all of it on hover. */
function Value({ children }: { children: string }) {
  return (
    <span className="block truncate" title={children}>
      {children}
    </span>
  );
}

/**
 * What the node is made of: its processor and its memory, as the node list
 * reports them. `children` go under the rows: what the page has to say of the
 * hardware it could not read (temperatures, the memory layout).
 */
export function NodeHardwarePanel({
  node,
  children,
}: {
  node: NodeResponse;
  children?: ReactNode;
}) {
  const swapPercent =
    node.swap_total > 0
      ? ((node.swap_used / node.swap_total) * 100).toFixed(1)
      : null;

  return (
    <Panel title="Hardware">
      <PropertyList>
        <PropertyRow label="CPU model">
          <Value>{node.cpu_model || "—"}</Value>
        </PropertyRow>
        <PropertyRow label="Sockets">
          {node.cpu_sockets ? String(node.cpu_sockets) : "—"}
        </PropertyRow>
        <PropertyRow label="Cores">
          {node.cpu_cores ? `${String(node.cpu_cores)} per socket` : "—"}
        </PropertyRow>
        <PropertyRow label="Threads">
          {node.cpu_threads ? `${String(node.cpu_threads)} per core` : "—"}
        </PropertyRow>
        <PropertyRow label="Total CPUs">{String(node.cpu_count)}</PropertyRow>
        <PropertyRow label="Frequency">
          {node.cpu_mhz ? `${node.cpu_mhz} MHz` : "—"}
        </PropertyRow>
        <PropertyRow label="Total RAM">
          {formatBytes(node.mem_total)}
        </PropertyRow>
        <PropertyRow label="Swap">
          {node.swap_total
            ? `${formatBytes(node.swap_used)} of ${formatBytes(node.swap_total)} used (${swapPercent ?? "0"}%)`
            : "—"}
        </PropertyRow>
      </PropertyList>
      {children !== undefined && (
        <div className="mt-3 space-y-3 border-t pt-3 empty:hidden">
          {children}
        </div>
      )}
    </Panel>
  );
}
