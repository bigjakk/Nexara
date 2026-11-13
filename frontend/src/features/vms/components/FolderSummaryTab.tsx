import { useMemo } from "react";
import { Link } from "react-router-dom";
import { ChevronRight, Folder } from "lucide-react";
import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { formatBytes, formatPercent } from "@/lib/format";
import { useClusterMetrics } from "@/hooks/useMetrics";
import type { VMFolder, VMResponse } from "@/types/api";

export interface ChildFolderSummary {
  folder: VMFolder;
  /** VM count of the child's whole subtree (matches what its page shows). */
  vmCount: number;
}

interface FolderSummaryTabProps {
  clusterId: string;
  /** VMs of this folder's subtree, templates included. */
  vms: VMResponse[];
  childFolders: ChildFolderSummary[];
}

/**
 * A folder's totals as one strip, and the folders inside it. CPU and memory
 * are summed over the running guests, from the cluster's live metrics.
 */
export function FolderSummaryTab({
  clusterId,
  vms,
  childFolders,
}: FolderSummaryTabProps) {
  const metrics = useClusterMetrics(clusterId);

  const usage = useMemo(() => {
    const guests = vms.filter((vm) => !vm.template);
    const running = guests.filter((vm) => vm.status === "running");
    let usedVcpu = 0;
    let usedMem = 0;
    let liveSeen = false;
    for (const vm of running) {
      const live = metrics?.vmMetrics.get(vm.id);
      if (!live) continue;
      liveSeen = true;
      usedVcpu += (live.cpuPercent / 100) * vm.cpu_count;
      usedMem += (live.memPercent / 100) * vm.mem_total;
    }
    return {
      guests: guests.length,
      running: running.length,
      stopped: guests.length - running.length,
      allocVcpu: guests.reduce((s, vm) => s + vm.cpu_count, 0),
      totalMem: guests.reduce((s, vm) => s + vm.mem_total, 0),
      totalDisk: guests.reduce((s, vm) => s + vm.disk_total, 0),
      usedVcpu,
      usedMem,
      liveSeen,
    };
  }, [vms, metrics]);

  // A reading exists only once a running guest has reported one: until then
  // the bar is an empty track and the number a dash, never a false 0%.
  const hasLive = usage.running > 0 && usage.liveSeen;
  const cpuPercent =
    hasLive && usage.allocVcpu > 0
      ? (usage.usedVcpu / usage.allocVcpu) * 100
      : null;
  const memPercent =
    hasLive && usage.totalMem > 0
      ? (usage.usedMem / usage.totalMem) * 100
      : null;

  return (
    <>
      <Panel
        flush
        title="Totals"
        aside={
          usage.running === 0
            ? "Nothing is running, so there is no live usage."
            : !usage.liveSeen
              ? "Waiting for live metrics."
              : childFolders.length > 0
                ? "Subfolders included. Usage is summed over the running guests."
                : "Usage is summed over the running guests."
        }
        bodyClassName="pb-4"
      >
        <StatStrip>
          <Stat
            label="Guests"
            value={String(usage.guests)}
            tone={usage.guests === 0 ? "quiet" : "default"}
            sub={
              usage.guests === 0
                ? undefined
                : `${String(usage.running)} running, ${String(usage.stopped)} stopped`
            }
          />
          <Stat
            label="CPU"
            value={cpuPercent === null ? "—" : formatPercent(cpuPercent)}
            sub={`of ${String(usage.allocVcpu)} vCPUs`}
            percent={cpuPercent}
          />
          <Stat
            label="Memory"
            value={memPercent === null ? "—" : formatPercent(memPercent)}
            sub={
              memPercent === null
                ? `of ${formatBytes(usage.totalMem)}`
                : `${formatBytes(usage.usedMem)} of ${formatBytes(usage.totalMem)}`
            }
            percent={memPercent}
          />
          <Stat
            label="Storage provisioned"
            value={formatBytes(usage.totalDisk)}
            tone={usage.totalDisk === 0 ? "quiet" : "default"}
          />
        </StatStrip>
      </Panel>

      {childFolders.length > 0 && (
        <Panel title="Subfolders" aside={String(childFolders.length)}>
          <ul className="-mx-2 grid grid-cols-[repeat(auto-fill,minmax(min(15rem,100%),1fr))] gap-x-4 gap-y-0.5">
            {childFolders.map(({ folder, vmCount }) => (
              <li key={folder.id} className="min-w-0">
                <Link
                  to={`/clusters/${clusterId}/folders/${folder.id}`}
                  className="group flex items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors hover:bg-foreground/[0.05]"
                >
                  <Folder className="h-4 w-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 truncate font-medium">
                    {folder.name}
                  </span>
                  <span className="ml-auto shrink-0 text-[12.5px] tabular-nums text-muted-foreground">
                    {vmCount} {vmCount === 1 ? "guest" : "guests"}
                  </span>
                  <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground/60 transition-colors group-hover:text-foreground" />
                </Link>
              </li>
            ))}
          </ul>
        </Panel>
      )}
    </>
  );
}
