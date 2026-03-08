import { memo } from "react";

const legendItems = [
  { color: "#22c55e", label: "Online / Running" },
  { color: "#eab308", label: "Warning / Paused" },
  { color: "#ef4444", label: "Offline / Stopped" },
  { color: "#6b7280", label: "Unknown" },
];

export const TopologyLegend = memo(function TopologyLegend() {
  return (
    <div className="flex items-center gap-4 rounded-lg border bg-card px-3 py-2 text-xs text-muted-foreground">
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
    </div>
  );
});
