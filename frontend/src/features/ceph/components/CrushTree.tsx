import { Panel, PropertyList, PropertyRow } from "@/components/page/Panel";
import { cn } from "@/lib/utils";
import type { CephOSD, CephCrushRule } from "../types/ceph";

interface CrushTreeProps {
  osds: CephOSD[];
  crushRules: CephCrushRule[];
}

function osdDotClass(osd: CephOSD): string {
  if (osd.up === 0) return "bg-red-500";
  if (osd.in === 0) return "bg-amber-500";
  return "bg-emerald-500";
}

function osdStateLabel(osd: CephOSD): string {
  if (osd.up === 0) return "down";
  if (osd.in === 0) return "out";
  return "up and in";
}

/**
 * A rule's id, type and size range. Ceph releases since Quincy report no
 * size range, which arrives here as zeroes: left out, not shown as "0 to 0".
 * The same goes for a type that was not reported.
 */
function crushRuleSummary(rule: CephCrushRule): string {
  const parts = [`ID ${String(rule.rule_id)}`];
  // Ceph's rule types start at 1 (replicated); 0 is a type nobody reported.
  if (rule.type > 0) parts.push(`type ${String(rule.type)}`);
  if (rule.max_size > 0) {
    parts.push(`size ${String(rule.min_size)} to ${String(rule.max_size)}`);
  }
  return parts.join(", ");
}

/**
 * The CRUSH hierarchy as Nexara can read it: the default root, its hosts and
 * each host's OSDs, with the CRUSH rules beside it.
 */
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
    <div className="flex flex-wrap items-start gap-4">
      <Panel
        title="CRUSH topology"
        aside={`Root default: ${String(osds.length)} ${osds.length === 1 ? "OSD" : "OSDs"} across ${String(hosts.length)} ${hosts.length === 1 ? "host" : "hosts"}`}
        className="flex-[999_1_560px]"
      >
        <ul className="divide-y">
          {hosts.map(([host, hostOsds]) => {
            const sortedOsds = [...hostOsds].sort((a, b) => a.id - b.id);
            const upCount = sortedOsds.filter((o) => o.up === 1).length;
            const totalWeight = sortedOsds.reduce(
              (sum, o) => sum + o.crush_weight,
              0,
            );

            return (
              <li
                key={host}
                className="flex flex-wrap items-center gap-x-5 gap-y-2 py-3 first:pt-0 last:pb-0"
              >
                <div className="min-w-0 flex-[0_1_13rem]">
                  <div className="truncate text-sm font-medium" title={host}>
                    {host}
                  </div>
                  <div
                    className={cn(
                      "text-[12.5px] text-muted-foreground",
                      upCount < sortedOsds.length &&
                        "font-semibold text-red-700 dark:text-red-400",
                    )}
                  >
                    {`${String(upCount)} of ${String(sortedOsds.length)} up, weight ${totalWeight.toFixed(4)}`}
                  </div>
                </div>
                <ul className="flex min-w-0 flex-[1_1_16rem] flex-wrap gap-1.5">
                  {sortedOsds.map((osd) => (
                    <li
                      key={osd.id}
                      className="flex items-center gap-1.5 rounded-[5px] bg-foreground/[0.07] px-2 py-1 font-mono text-[12.5px]"
                      title={`osd.${String(osd.id)}: ${osdStateLabel(osd)}`}
                    >
                      <span
                        aria-hidden="true"
                        className={cn(
                          "h-1.5 w-1.5 shrink-0 rounded-full",
                          osdDotClass(osd),
                        )}
                      />
                      <span className="font-medium">osd.{osd.id}</span>
                      <span className="text-muted-foreground">
                        {osd.crush_weight.toFixed(4)}
                      </span>
                    </li>
                  ))}
                </ul>
              </li>
            );
          })}
        </ul>
      </Panel>

      {crushRules.length > 0 && (
        <Panel title="CRUSH rules" className="flex-[1_1_300px]">
          <PropertyList>
            {crushRules.map((rule) => (
              <PropertyRow
                key={rule.rule_id}
                label={rule.rule_name || `Rule ${String(rule.rule_id)}`}
              >
                <span className="text-muted-foreground">
                  {crushRuleSummary(rule)}
                </span>
              </PropertyRow>
            ))}
          </PropertyList>
        </Panel>
      )}
    </div>
  );
}
