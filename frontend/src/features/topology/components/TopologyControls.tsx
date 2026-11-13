import { memo, useId } from "react";
import { ArrowDown, ArrowRight } from "lucide-react";
import { Segmented } from "@/components/ui/segmented";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import type { ClusterResponse } from "@/types/api";
import type { TopologyFilters } from "../lib/topology-transform";

interface TopologyControlsProps {
  clusters: ClusterResponse[];
  filters: TopologyFilters;
  onFiltersChange: (filters: TopologyFilters) => void;
  direction: "TB" | "LR";
  onDirectionChange: (dir: "TB" | "LR") => void;
  /** The power overlay's toggle, shown only with its handler (global view:ups). */
  showPower?: boolean;
  onShowPowerChange?: ((on: boolean) => void) | undefined;
  className?: string;
}

const DIRECTIONS = [
  {
    value: "TB",
    label: (
      <>
        <ArrowDown aria-hidden="true" />
        Top-down
      </>
    ),
  },
  {
    value: "LR",
    label: (
      <>
        <ArrowRight aria-hidden="true" />
        Left-right
      </>
    ),
  },
] as const;

/** A layer of the map that can be drawn or left out. */
function LayerSwitch({
  label,
  checked,
  onCheckedChange,
}: {
  label: string;
  checked: boolean;
  onCheckedChange: (on: boolean) => void;
}) {
  const id = useId();
  return (
    <div className="flex items-center gap-1.5">
      <Switch
        id={id}
        checked={checked}
        onCheckedChange={onCheckedChange}
        className="scale-[0.8]"
      />
      <label htmlFor={id} className="cursor-pointer text-[13px]">
        {label}
      </label>
    </div>
  );
}

/**
 * What the map draws and which way it runs, as one compact bar that floats
 * over the canvas.
 */
export const TopologyControls = memo(function TopologyControls({
  clusters,
  filters,
  onFiltersChange,
  direction,
  onDirectionChange,
  showPower = false,
  onShowPowerChange,
  className,
}: TopologyControlsProps) {
  return (
    <div
      role="toolbar"
      aria-label="Map options"
      className={cn(
        "flex flex-wrap items-center gap-x-3.5 gap-y-1.5 rounded-[10px] bg-card py-1.5 pl-1.5 pr-2 text-card-foreground ring-1 ring-border",
        className,
      )}
    >
      <Select
        value={filters.selectedClusterId ?? "all"}
        onValueChange={(value) => {
          onFiltersChange({
            ...filters,
            selectedClusterId: value === "all" ? null : value,
          });
        }}
      >
        <SelectTrigger
          aria-label="Cluster"
          className="h-7 w-auto max-w-[12rem] gap-1.5 border-0 bg-foreground/[0.07] px-2.5 text-[13px] font-medium shadow-none"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="all">All clusters</SelectItem>
          {clusters.map((c) => (
            <SelectItem key={c.id} value={c.id}>
              {c.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1.5">
        <LayerSwitch
          label="Guests"
          checked={filters.showVMs}
          onCheckedChange={(on) => {
            onFiltersChange({ ...filters, showVMs: on });
          }}
        />
        <LayerSwitch
          label="Storage"
          checked={filters.showStorage}
          onCheckedChange={(on) => {
            onFiltersChange({ ...filters, showStorage: on });
          }}
        />
        {onShowPowerChange !== undefined && (
          <LayerSwitch
            label="Power"
            checked={showPower}
            onCheckedChange={onShowPowerChange}
          />
        )}
      </div>

      <Segmented
        aria-label="Layout direction"
        value={direction}
        onValueChange={onDirectionChange}
        options={DIRECTIONS}
      />
    </div>
  );
});
