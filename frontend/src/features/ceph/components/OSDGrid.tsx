import type { CephOSD } from "../types/ceph";

interface OSDGridProps {
  osds: CephOSD[];
}

function getOSDColor(osd: CephOSD): string {
  if (osd.up === 0) return "bg-destructive";
  if (osd.in === 0) return "bg-yellow-500";
  return "bg-green-500";
}

function getOSDLabel(osd: CephOSD): string {
  if (osd.up === 0) return "Down";
  if (osd.in === 0) return "Out";
  return "Up/In";
}

export function OSDGrid({ osds }: OSDGridProps) {
  const sorted = [...osds].sort((a, b) => a.id - b.id);
  const upCount = sorted.filter((o) => o.up === 1).length;
  const inCount = sorted.filter((o) => o.in === 1).length;

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-4 text-sm text-muted-foreground">
        <span>{sorted.length} total</span>
        <span className="flex items-center gap-1">
          <span className="inline-block h-3 w-3 rounded-sm bg-green-500" />
          {upCount} up
        </span>
        <span className="flex items-center gap-1">
          <span className="inline-block h-3 w-3 rounded-sm bg-yellow-500" />
          {sorted.length - inCount} out
        </span>
        <span className="flex items-center gap-1">
          <span className="inline-block h-3 w-3 rounded-sm bg-destructive" />
          {sorted.length - upCount} down
        </span>
      </div>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(2rem,1fr))] gap-1">
        {sorted.map((osd) => (
          <div
            key={osd.id}
            className={`flex h-8 w-8 items-center justify-center rounded-sm text-[10px] font-medium text-white ${getOSDColor(osd)}`}
            title={`OSD ${String(osd.id)} (${osd.host}) - ${getOSDLabel(osd)}`}
          >
            {osd.id}
          </div>
        ))}
      </div>
    </div>
  );
}
