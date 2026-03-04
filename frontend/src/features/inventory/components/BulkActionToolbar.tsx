import { Play, Square, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Table } from "@tanstack/react-table";
import type { InventoryRow } from "../types/inventory";

interface BulkActionToolbarProps {
  table: Table<InventoryRow>;
}

export function BulkActionToolbar({ table }: BulkActionToolbarProps) {
  const selectedCount = table.getFilteredSelectedRowModel().rows.length;

  if (selectedCount === 0) return null;

  return (
    <div className="flex items-center gap-2 rounded-lg border bg-muted/50 px-4 py-2">
      <span className="text-sm font-medium">
        {selectedCount} selected
      </span>
      <div className="flex gap-1">
        <Button variant="outline" size="sm" className="gap-1" disabled>
          <Play className="h-3 w-3" />
          Start
        </Button>
        <Button variant="outline" size="sm" className="gap-1" disabled>
          <Square className="h-3 w-3" />
          Stop
        </Button>
        <Button variant="outline" size="sm" className="gap-1 text-destructive" disabled>
          <Trash2 className="h-3 w-3" />
          Delete
        </Button>
      </div>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => { table.toggleAllRowsSelected(false); }}
        className="ml-auto gap-1"
      >
        <X className="h-3 w-3" />
        Clear
      </Button>
    </div>
  );
}
