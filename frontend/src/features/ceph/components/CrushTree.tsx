import { Server, HardDrive } from "lucide-react";
import type { CephOSD, CephCrushRule } from "../types/ceph";

interface CrushTreeProps {
  osds: CephOSD[];
  crushRules: CephCrushRule[];
}

function getOSDStatusColor(osd: CephOSD): string {
  if (osd.up === 0) return "text-destructive";
  if (osd.in === 0) return "text-yellow-500";
  return "text-green-500";
}

function getOSDStatusDot(osd: CephOSD): string {
  if (osd.up === 0) return "bg-destructive";
  if (osd.in === 0) return "bg-yellow-500";
  return "bg-green-500";
}

export function CrushTree({ osds, crushRules }: CrushTreeProps) {
  // Group OSDs by host
  const hostMap = new Map<string, CephOSD[]>();
  for (const osd of osds) {
    const host = osd.host || "unknown";
    const existing = hostMap.get(host);
    if (existing) {
      existing.push(osd);
    } else {
      hostMap.set(host, [osd]);
    }
  }

  const hosts = [...hostMap.entries()].sort((a, b) => a[0].localeCompare(b[0]));

  return (
    <div className="space-y-4">
      <h3 className="text-sm font-medium">CRUSH Topology</h3>

      {/* Root node */}
      <div className="rounded-md border p-3">
        <div className="mb-3 flex items-center gap-2 text-sm font-medium">
          <span className="rounded bg-primary/10 px-2 py-0.5 text-primary">root</span>
          <span>default</span>
          <span className="text-muted-foreground">({osds.length} OSDs across {hosts.length} hosts)</span>
        </div>

        <div className="ml-4 space-y-3 border-l pl-4">
          {hosts.map(([host, hostOsds]) => {
            const sortedOsds = [...hostOsds].sort((a, b) => a.id - b.id);
            const upCount = sortedOsds.filter((o) => o.up === 1).length;
            const totalWeight = sortedOsds.reduce((sum, o) => sum + o.crush_weight, 0);

            return (
              <div key={host}>
                <div className="mb-1.5 flex items-center gap-2 text-sm">
                  <Server className="h-3.5 w-3.5 text-muted-foreground" />
                  <span className="rounded bg-blue-500/10 px-1.5 py-0.5 text-xs text-blue-600 dark:text-blue-400">host</span>
                  <span className="font-medium">{host}</span>
                  <span className="text-xs text-muted-foreground">
                    {upCount}/{sortedOsds.length} up &middot; weight {totalWeight.toFixed(4)}
                  </span>
                </div>
                <div className="ml-6 grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-1">
                  {sortedOsds.map((osd) => (
                    <div
                      key={osd.id}
                      className="flex items-center gap-1.5 rounded border px-2 py-1 text-xs"
                    >
                      <span className={`inline-block h-2 w-2 rounded-full ${getOSDStatusDot(osd)}`} />
                      <HardDrive className={`h-3 w-3 ${getOSDStatusColor(osd)}`} />
                      <span className="font-mono font-medium">osd.{osd.id}</span>
                      <span className="text-muted-foreground">
                        {osd.crush_weight.toFixed(4)}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* CRUSH Rules summary */}
      {crushRules.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-sm font-medium">CRUSH Rules</h3>
          <div className="flex flex-wrap gap-2">
            {crushRules.map((rule) => (
              <div key={rule.rule_id} className="rounded-md border px-3 py-1.5 text-xs">
                <span className="font-medium">{rule.rule_name}</span>
                <span className="ml-2 text-muted-foreground">
                  ID {rule.rule_id} &middot; type {rule.type} &middot; size {rule.min_size}-{rule.max_size}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
