import { memo } from "react";
import { TONE_COLOR } from "../lib/topology-power";

const legendItems = [
  { color: "#10b981", label: "Online / Running" },
  { color: "#f59e0b", label: "Warning / Paused" },
  { color: "#ef4444", label: "Offline" },
  { color: "#6b7280", label: "Stopped / Unknown" },
];

function PowerLine({ label, color, dashed = false }: { label: string; color: string; dashed?: boolean }) {
  return (
    <div className="flex items-center gap-1.5">
      <svg width="18" height="4" aria-hidden="true">
        <line x1="0" y1="2" x2="18" y2="2" stroke={color} strokeWidth="1.5" strokeDasharray={dashed ? "6 4" : undefined} />
      </svg>
      <span>{label}</span>
    </div>
  );
}

/** Status colours, and with the power overlay on, how its edges read. */
export const TopologyLegend = memo(function TopologyLegend({ power = false }: { power?: boolean }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border bg-card px-3 py-2 text-xs text-muted-foreground">
      <span className="font-medium">Status:</span>
      {legendItems.map((item) => (
        <div key={item.color} className="flex items-center gap-1.5">
          <div
            className="h-2.5 w-2.5 rounded-full"
            style={{ backgroundColor: item.color }}
          />
          <span>{item.label}</span>
        </div>
      ))}
      {power && (
        <>
          <span className="font-medium">Power:</span>
          <PowerLine label="On mains" color={TONE_COLOR.ok} />
          <PowerLine label="On battery" color={TONE_COLOR.warn} dashed />
        </>
      )}
    </div>
  );
});
