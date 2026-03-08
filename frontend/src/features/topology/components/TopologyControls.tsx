import { memo } from "react";
import type { ClusterResponse } from "@/types/api";
import type { TopologyFilters } from "../lib/topology-transform";

interface TopologyControlsProps {
  clusters: ClusterResponse[];
  filters: TopologyFilters;
  onFiltersChange: (filters: TopologyFilters) => void;
  direction: "TB" | "LR";
  onDirectionChange: (dir: "TB" | "LR") => void;
}

export const TopologyControls = memo(function TopologyControls({
  clusters,
  filters,
  onFiltersChange,
  direction,
  onDirectionChange,
}: TopologyControlsProps) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border bg-card p-3 text-sm">
      {/* Cluster filter */}
      <div className="flex items-center gap-2">
        <label className="text-xs font-medium text-muted-foreground">Cluster</label>
        <select
          className="rounded border bg-background px-2 py-1 text-xs"
          value={filters.selectedClusterId ?? "all"}
          onChange={(e) => {
            onFiltersChange({
              ...filters,
              selectedClusterId: e.target.value === "all" ? null : e.target.value,
            });
          }}
        >
          <option value="all">All clusters</option>
          {clusters.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
      </div>

      <div className="h-4 w-px bg-border" />

      {/* Toggle VMs */}
      <label className="flex items-center gap-1.5 text-xs cursor-pointer">
        <input
          type="checkbox"
          checked={filters.showVMs}
          onChange={(e) => {
            onFiltersChange({ ...filters, showVMs: e.target.checked });
          }}
          className="rounded"
        />
        VMs/CTs
      </label>

      {/* Toggle Storage */}
      <label className="flex items-center gap-1.5 text-xs cursor-pointer">
        <input
          type="checkbox"
          checked={filters.showStorage}
          onChange={(e) => {
            onFiltersChange({ ...filters, showStorage: e.target.checked });
          }}
          className="rounded"
        />
        Storage
      </label>

      <div className="h-4 w-px bg-border" />

      {/* Layout direction */}
      <div className="flex items-center gap-2">
        <label className="text-xs font-medium text-muted-foreground">Layout</label>
        <select
          className="rounded border bg-background px-2 py-1 text-xs"
          value={direction}
          onChange={(e) => {
            onDirectionChange(e.target.value as "TB" | "LR");
          }}
        >
          <option value="TB">Top-Down</option>
          <option value="LR">Left-Right</option>
        </select>
      </div>
    </div>
  );
});
