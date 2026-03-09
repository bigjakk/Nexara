import { useState } from "react";
import { Plus, Monitor, Container } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { CreateVMDialog } from "@/features/vms/components/CreateVMDialog";
import { CreateCTDialog } from "@/features/vms/components/CreateCTDialog";

type PendingType = "vm" | "ct" | null;

export function CreateResourceMenu() {
  const { data: clusters } = useClusters();
  const [pendingType, setPendingType] = useState<PendingType>(null);
  const [createVMOpen, setCreateVMOpen] = useState(false);
  const [createCTOpen, setCreateCTOpen] = useState(false);
  const [selectedCluster, setSelectedCluster] = useState("");

  const hasClusters = clusters && clusters.length > 0;

  function handleSelect(type: "vm" | "ct") {
    if (!hasClusters) return;

    // If only one cluster, skip the picker
    if (clusters.length === 1 && clusters[0]) {
      setSelectedCluster(clusters[0].id);
      if (type === "vm") setCreateVMOpen(true);
      else setCreateCTOpen(true);
      return;
    }

    // Multiple clusters — show picker
    setPendingType(type);
  }

  function handleClusterPick(clusterId: string) {
    setSelectedCluster(clusterId);
    setPendingType(null);
    if (pendingType === "vm") setCreateVMOpen(true);
    else setCreateCTOpen(true);
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="sm" className="gap-1.5">
            <Plus className="h-4 w-4" />
            Create
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            disabled={!hasClusters}
            onClick={() => { handleSelect("vm"); }}
          >
            <Monitor className="mr-2 h-4 w-4" />
            Virtual Machine
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={!hasClusters}
            onClick={() => { handleSelect("ct"); }}
          >
            <Container className="mr-2 h-4 w-4" />
            Container
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {/* Cluster picker for multi-cluster setups */}
      <Dialog
        open={pendingType !== null}
        onOpenChange={(open) => { if (!open) setPendingType(null); }}
      >
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>
              Select Cluster
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-2">
            <Label>
              Choose which cluster to create the{" "}
              {pendingType === "vm" ? "VM" : "container"} on
            </Label>
            <div className="space-y-1">
              {clusters?.map((cluster) => (
                <Button
                  key={cluster.id}
                  variant="outline"
                  className="w-full justify-start"
                  onClick={() => { handleClusterPick(cluster.id); }}
                >
                  {cluster.name}
                </Button>
              ))}
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <CreateVMDialog
        open={createVMOpen}
        onOpenChange={setCreateVMOpen}
        clusterId={selectedCluster}
      />
      <CreateCTDialog
        open={createCTOpen}
        onOpenChange={setCreateCTOpen}
        clusterId={selectedCluster}
      />
    </>
  );
}
