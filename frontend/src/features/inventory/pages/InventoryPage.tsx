import { useState } from "react";
import { AlertCircle, Loader2, Package, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useInventoryData } from "../api/inventory-queries";
import { ResourceTable } from "../components/ResourceTable";
import { CreateVMDialog } from "@/features/vms/components/CreateVMDialog";
import { CreateCTDialog } from "@/features/vms/components/CreateCTDialog";

const selectClass =
  "flex h-9 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

export function InventoryPage() {
  const { rows, isLoading, error } = useInventoryData();
  const { data: clusters } = useClusters();

  const [createVMOpen, setCreateVMOpen] = useState(false);
  const [createCTOpen, setCreateCTOpen] = useState(false);
  const [selectedCluster, setSelectedCluster] = useState("");

  // Auto-select first cluster
  const effectiveCluster =
    selectedCluster || (clusters && clusters.length > 0 && clusters[0] ? clusters[0].id : "");

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Inventory</h1>
          <p className="text-sm text-muted-foreground">
            Browse all VMs, containers, and nodes across your clusters.
          </p>
        </div>
        {clusters && clusters.length > 0 && (
          <div className="flex items-center gap-2">
            {clusters.length > 1 && (
              <select
                value={selectedCluster}
                onChange={(e) => { setSelectedCluster(e.target.value); }}
                className={selectClass}
              >
                {clusters.map((cl) => (
                  <option key={cl.id} value={cl.id}>
                    {cl.name}
                  </option>
                ))}
              </select>
            )}
            <Button
              size="sm"
              className="gap-1"
              onClick={() => { setCreateVMOpen(true); }}
            >
              <Plus className="h-4 w-4" />
              New VM
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="gap-1"
              onClick={() => { setCreateCTOpen(true); }}
            >
              <Plus className="h-4 w-4" />
              New CT
            </Button>
          </div>
        )}
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

      <CreateVMDialog
        open={createVMOpen}
        onOpenChange={setCreateVMOpen}
        clusterId={effectiveCluster}
      />
      <CreateCTDialog
        open={createCTOpen}
        onOpenChange={setCreateCTOpen}
        clusterId={effectiveCluster}
      />
    </div>
  );
}
