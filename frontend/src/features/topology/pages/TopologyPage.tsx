import { useState } from "react";
import { useTopologyData } from "../api/topology-queries";
import { TopologyCanvas } from "../components/TopologyCanvas";
import { TopologyControls } from "../components/TopologyControls";
import { TopologyLegend } from "../components/TopologyLegend";
import type { TopologyFilters } from "../lib/topology-transform";

export function TopologyPage() {
  const { input, isLoading, error } = useTopologyData();

  const [filters, setFilters] = useState<TopologyFilters>({
    showVMs: true,
    showStorage: false,
    selectedClusterId: null,
  });

  const [direction, setDirection] = useState<"TB" | "LR">("TB");

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="text-muted-foreground">Loading topology...</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="text-destructive">
          Failed to load topology: {error.message}
        </div>
      </div>
    );
  }

  if (input.clusters.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="text-muted-foreground">
          No clusters configured. Add a cluster to see the topology.
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-[calc(100vh-5rem)] flex-col gap-3 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Topology</h1>
        <TopologyLegend />
      </div>

      <TopologyControls
        clusters={input.clusters}
        filters={filters}
        onFiltersChange={setFilters}
        direction={direction}
        onDirectionChange={setDirection}
      />

      <div className="flex-1 min-h-0">
        <TopologyCanvas
          input={input}
          filters={filters}
          direction={direction}
        />
      </div>
    </div>
  );
}
