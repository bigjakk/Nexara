import { cn } from "@/lib/utils";
import type { CephOSD } from "../types/ceph";

interface OSDGridProps {
  osds: CephOSD[];
}

/** Colour is state: an OSD that is up and in is a plain cell. */
function osdCellClass(osd: CephOSD): string {
  if (osd.up === 0) return "bg-red-500 text-white";
  if (osd.in === 0) return "bg-amber-500 text-white";
  return "bg-foreground/[0.08] text-muted-foreground";
}

function osdStateLabel(osd: CephOSD): string {
  if (osd.up === 0) return "down";
  if (osd.in === 0) return "out";
  return "up and in";
}

/** A cell per OSD, in id order: the ones that are down or out stand out. */
export function OSDGrid({ osds }: OSDGridProps) {
  const sorted = [...osds].sort((a, b) => a.id - b.id);

  return (
    <ul
      aria-label="OSD states"
      className="grid grid-cols-[repeat(auto-fill,minmax(1.75rem,1fr))] gap-1"
    >
      {sorted.map((osd) => (
        <li
          key={osd.id}
          className={cn(
            "flex h-7 items-center justify-center rounded-[5px] text-[11px] font-medium tabular-nums",
            osdCellClass(osd),
          )}
          title={`OSD ${String(osd.id)} on ${osd.host}: ${osdStateLabel(osd)}`}
        >
          {osd.id}
        </li>
      ))}
    </ul>
  );
}
