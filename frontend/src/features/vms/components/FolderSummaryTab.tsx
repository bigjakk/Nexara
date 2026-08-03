import { useMemo } from "react";
import { Link } from "react-router-dom";
import { Cpu, Folder, Gauge, HardDrive, MemoryStick } from "lucide-react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { formatBytes } from "@/lib/format";
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
  isUnassigned: boolean;
}

function barColor(percent: number): string {
  if (percent >= 90) return "bg-red-500";
  if (percent >= 75) return "bg-amber-500";
  return "bg-emerald-500";
}

function UsageRow({
  icon,
  label,
  valueText,
  percent,
}: {
  icon: React.ReactNode;
  label: string;
  valueText: string;
  /** null hides the bar (metric not applicable, e.g. provisioned storage) */
  percent: number | null;
}) {
  const clamped =
    percent === null ? null : Math.max(0, Math.min(100, percent));
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
          {icon}
          {label}
        </span>
        <span className="text-sm font-medium tabular-nums">{valueText}</span>
      </div>
      {clamped !== null && (
        <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              "h-full rounded-full transition-all duration-500",
              barColor(clamped),
            )}
            style={{ width: `${String(clamped)}%` }}
          />
        </div>
      )}
    </div>
  );
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-2">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-sm font-medium tabular-nums">{value}</span>
    </div>
  );
}

export function FolderSummaryTab({
  clusterId,
  vms,
  childFolders,
  isUnassigned,
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
      templates: vms.length - guests.length,
      allocVcpu: guests.reduce((s, vm) => s + vm.cpu_count, 0),
      totalMem: guests.reduce((s, vm) => s + vm.mem_total, 0),
      totalDisk: guests.reduce((s, vm) => s + vm.disk_total, 0),
      usedVcpu,
      usedMem,
      liveSeen,
    };
  }, [vms, metrics]);

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">Folder details</CardTitle>
          {isUnassigned ? (
            <CardDescription>
              VMs discovered on the cluster that aren&apos;t assigned to any
              folder.
            </CardDescription>
          ) : (
            childFolders.length > 0 && (
              <CardDescription>Counts include subfolders.</CardDescription>
            )
          )}
        </CardHeader>
        <CardContent className="space-y-2">
          <DetailRow label="Virtual machines" value={String(usage.guests)} />
          <DetailRow label="Running" value={String(usage.running)} />
          <DetailRow label="Stopped" value={String(usage.stopped)} />
          <DetailRow label="Templates" value={String(usage.templates)} />
          {!isUnassigned && (
            <DetailRow label="Subfolders" value={String(childFolders.length)} />
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">Usage</CardTitle>
          <CardDescription>
            {usage.running === 0
              ? "No running VMs."
              : usage.liveSeen
                ? "Live usage across running VMs."
                : "Waiting for live metrics…"}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <UsageRow
            icon={<Cpu className="h-3.5 w-3.5" />}
            label="CPU"
            valueText={`${usage.usedVcpu.toFixed(1)} / ${String(usage.allocVcpu)} vCPU`}
            percent={
              usage.allocVcpu > 0
                ? (usage.usedVcpu / usage.allocVcpu) * 100
                : null
            }
          />
          <UsageRow
            icon={<MemoryStick className="h-3.5 w-3.5" />}
            label="Memory"
            valueText={`${formatBytes(usage.usedMem)} / ${formatBytes(usage.totalMem)}`}
            percent={
              usage.totalMem > 0 ? (usage.usedMem / usage.totalMem) * 100 : null
            }
          />
          <UsageRow
            icon={<HardDrive className="h-3.5 w-3.5" />}
            label="Storage provisioned"
            valueText={formatBytes(usage.totalDisk)}
            percent={null}
          />
          <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Gauge className="h-3 w-3" />
            CPU and memory are summed over the folder&apos;s running VMs.
          </p>
        </CardContent>
      </Card>

      {!isUnassigned && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">Subfolders</CardTitle>
          </CardHeader>
          <CardContent>
            {childFolders.length === 0 ? (
              <p className="text-sm text-muted-foreground">No subfolders.</p>
            ) : (
              <ul className="space-y-1">
                {childFolders.map(({ folder, vmCount }) => (
                  <li key={folder.id}>
                    <Link
                      to={`/clusters/${clusterId}/folders/${folder.id}`}
                      className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent/50 transition-colors"
                    >
                      <Folder className="h-3.5 w-3.5 shrink-0 text-violet-500" />
                      <span className="min-w-0 truncate">{folder.name}</span>
                      <span className="ml-auto text-xs tabular-nums text-muted-foreground">
                        {vmCount} VM{vmCount === 1 ? "" : "s"}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
