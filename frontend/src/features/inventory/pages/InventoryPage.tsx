import { AlertCircle, Loader2, Package } from "lucide-react";
import { useInventoryData } from "../api/inventory-queries";
import { ResourceTable } from "../components/ResourceTable";

export function InventoryPage() {
  const { rows, isLoading, error } = useInventoryData();

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Inventory</h1>
        <p className="text-sm text-muted-foreground">
          Browse all VMs, containers, and nodes across your clusters.
        </p>
      </div>

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        </div>
      )}

      {error && (
        <div className="flex items-center gap-2 rounded-lg border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" />
          Failed to load inventory data. Please try again.
        </div>
      )}

      {!isLoading && !error && rows.length === 0 && (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <Package className="mb-2 h-10 w-10" />
          <p className="text-sm">No resources found. Add a cluster to get started.</p>
        </div>
      )}

      {!isLoading && !error && rows.length > 0 && (
        <ResourceTable data={rows} />
      )}
    </div>
  );
}
