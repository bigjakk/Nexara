import { Settings2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { Table } from "@tanstack/react-table";
import type { InventoryRow } from "../types/inventory";

const COLUMN_LABELS: Record<string, string> = {
  type: "Type",
  name: "Name",
  status: "Status",
  clusterName: "Cluster",
  nodeName: "Node",
  vmid: "VMID",
  cpuCount: "CPUs",
  memTotal: "Memory",
  diskTotal: "Disk",
  cpuPercent: "CPU %",
  memPercent: "Mem %",
  uptime: "Uptime",
  tags: "Tags",
  haState: "HA State",
  pool: "Pool",
  template: "Template",
};

interface ColumnToggleProps {
  table: Table<InventoryRow>;
}

export function ColumnToggle({ table }: ColumnToggleProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="gap-1">
          <Settings2 className="h-4 w-4" />
          Columns
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuLabel>Toggle columns</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {table
          .getAllColumns()
          .filter((col) => col.getCanHide())
          .map((col) => (
            <DropdownMenuCheckboxItem
              key={col.id}
              checked={col.getIsVisible()}
              onCheckedChange={(value) => {
                col.toggleVisibility(value);
              }}
            >
              {COLUMN_LABELS[col.id] ?? col.id}
            </DropdownMenuCheckboxItem>
          ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
